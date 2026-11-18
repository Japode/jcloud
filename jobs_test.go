package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeJobs stands in for docker on the host (JCT46). Each rule answers the first
// command whose argv contains its match; calls records every argv with the env it
// carried.
type fakeJobs struct {
	rules []jobRule
	calls []jobCmd
	fed   map[string]string // what a command read on stdin, by its match
}

type jobRule struct {
	match string
	out   string
	fail  bool
}

func (f *fakeJobs) on(match, out string) *fakeJobs {
	f.rules = append(f.rules, jobRule{match: match, out: out})
	return f
}

func (f *fakeJobs) failing(match string) *fakeJobs {
	f.rules = append(f.rules, jobRule{match: match, fail: true})
	return f
}

// first puts a rule ahead of every other, so it wins where two would match.
func (f *fakeJobs) first(r jobRule) *fakeJobs {
	f.rules = append([]jobRule{r}, f.rules...)
	return f
}

func (f *fakeJobs) run(c jobCmd) error {
	f.calls = append(f.calls, c)
	line := c.String()
	for _, r := range f.rules {
		if !strings.Contains(line, r.match) {
			continue
		}
		if c.Stdin != nil {
			b, _ := io.ReadAll(c.Stdin)
			if f.fed == nil {
				f.fed = map[string]string{}
			}
			f.fed[r.match] = string(b)
		}
		if r.fail {
			return errors.New(r.match + ": refused")
		}
		if c.Stdout != nil {
			io.WriteString(c.Stdout, r.out)
		}
		return nil
	}
	if c.Stdin != nil {
		io.Copy(io.Discard, c.Stdin)
	}
	return nil
}

// called answers every call whose argv contains match.
func (f *fakeJobs) called(match string) []jobCmd {
	var out []jobCmd
	for _, c := range f.calls {
		if strings.Contains(c.String(), match) {
			out = append(out, c)
		}
	}
	return out
}

// index is the position of the first call containing match, -1 for none.
func (f *fakeJobs) index(match string) int {
	for i, c := range f.calls {
		if strings.Contains(c.String(), match) {
			return i
		}
	}
	return -1
}

const jobsEnv = "PASTE_DB_PASSWORD=hunter2\nBACKUP_REPOSITORY=s3:s3.example.org/bucket\nBACKUP_PASSWORD=restic-secret\n" +
	"BACKUP_S3_ACCESS_KEY_ID=AKIA\nBACKUP_S3_SECRET_ACCESS_KEY=s3-secret\nBACKUP_PRUNE_HOUR=03\nHEALTH_GRACE_SECONDS=300\n"

// jobsTree is the Japode-shaped fixture platform with a backup.conf that names a
// dump, a SQLite file and an exclusion.
func jobsTree(t *testing.T) string {
	t.Helper()
	return exportTree(t, map[string]string{
		".env":                       jobsEnv,
		"services/paste/backup.conf": "mysql paste-db\nsqlite paste-data app.db\nexclude paste-data cache\n",
	})
}

// healthyHost answers as a host where every step works.
func healthyHost() *fakeJobs {
	return (&fakeJobs{}).
		on("config --volumes", "caddy-data\npaste-data\npaste-db\n").
		on("config --services", "caddy\npaste\npaste-db\n").
		on("ps -a --filter name=", "").
		on("PRAGMA integrity_check", "ok\n").
		on("PRAGMA foreign_key_check", "").
		on("sqlite_schema", "4\n").
		on("exec -T paste-db sh -c set -eu", "CREATE TABLE `a` (\n);\n-- Dump completed on 2026-11-18\n").
		on("wc -c", "57\n").
		on("find /vol -type f", "3\n").
		on("ls latest:/backup", "/caddy-data\n/paste-data\n/_mysql/paste-db.sql\n").
		on("information_schema.tables", "2\n")
}

// JCT46: backup stages the SQLite copy and the dump, ships every volume with the
// live database and the dumped volume excluded, under the project as host and tag,
// passes the restic secrets in the environment and never on argv, and prunes in
// BACKUP_PRUNE_HOUR only.
func TestBackupShipsEveryVolumeWithTheDatabasesStaged(t *testing.T) {
	root := jobsTree(t)
	was := backupNow
	backupNow = func() time.Time { return time.Date(2026, 11, 18, 3, 20, 0, 0, time.UTC) }
	t.Cleanup(func() { backupNow = was })
	// An empty repository: `cat config` fails, so the run initialises one.
	f := healthyHost().first(jobRule{match: " cat config", fail: true}).on(`cat >"/staging/$1.sql"`, "")

	var out bytes.Buffer
	if code := Backup(root, f.run, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if f.index(" init") < 0 {
		t.Fatal("an empty repository was not initialised")
	}
	backup := f.called(" backup /backup")
	if len(backup) != 1 {
		t.Fatalf("restic backup ran %d times", len(backup))
	}
	argv := backup[0].String()
	for _, w := range []string{
		"japode-cloud_paste-data:/backup/paste-data:ro", "japode-backup-staging:/backup/_sqlite:ro", "japode-backup-mysql-staging:/backup/_mysql:ro",
		"--host japode-cloud --tag japode-cloud",
		"--exclude /backup/paste-data/app.db --exclude /backup/paste-data/app.db-wal --exclude /backup/paste-data/app.db-shm",
		"--exclude /backup/paste-data/cache", "--exclude /backup/paste-db",
		"-e RESTIC_PASSWORD", "-v japode-backup-cache:/root/.cache/restic",
	} {
		if !strings.Contains(argv, w) {
			t.Fatalf("no %q in:\n%s", w, argv)
		}
	}
	if strings.Contains(argv, "restic-secret") || strings.Contains(argv, "s3-secret") {
		t.Fatal("a secret reached argv")
	}
	if !contains(backup[0].Env, "RESTIC_PASSWORD=restic-secret") || !contains(backup[0].Env, "AWS_SECRET_ACCESS_KEY=s3-secret") {
		t.Fatalf("env: %v", backup[0].Env)
	}
	if !strings.Contains(f.fed["cat >\"/staging/$1.sql\""], "-- Dump completed") {
		t.Fatalf("the dump was not piped into staging: %v", f.fed)
	}
	forget := f.called(" forget ")
	if len(forget) != 1 || !strings.Contains(forget[0].String(), "--keep-hourly 8") || !strings.Contains(forget[0].String(), "--prune") {
		t.Fatalf("forget: %v", forget)
	}

	backupNow = func() time.Time { return time.Date(2026, 11, 18, 9, 20, 0, 0, time.UTC) }
	f.calls = nil
	Backup(root, f.run, io.Discard)
	if strings.Contains(f.called(" forget ")[0].String(), "--prune") {
		t.Fatal("pruned outside BACKUP_PRUNE_HOUR")
	}
}

// JCT46: a database that is not there is one service's failure: said, its live
// file not excluded (or the volume would ship with a hole), every volume shipped,
// and the run exits 1.
func TestBackupShipsTheRestWhenADatabaseIsMissing(t *testing.T) {
	root := jobsTree(t)
	f := healthyHost().
		first(jobRule{match: `test -f "/vol/$1"`, fail: true}).
		first(jobRule{match: "find /vol -type f", out: "0\n"})
	var out bytes.Buffer
	if code := Backup(root, f.run, &out); code != 1 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "paste-data is empty: has this service ever started?") {
		t.Fatalf("output:\n%s", out.String())
	}
	backup := f.called(" backup /backup")
	if len(backup) != 1 || strings.Contains(backup[0].String(), "/backup/paste-data/app.db") {
		t.Fatalf("backup: %v", backup)
	}
}

// JCT46: a backup.conf that does not parse refuses the run before anything
// ships, every problem at once with its line; a service whose pin is gone while
// its containers remain is refused too.
func TestBackupRefusesWhatItCannotReadWhole(t *testing.T) {
	root := exportTree(t, map[string]string{
		".env":                       jobsEnv,
		"services/paste/backup.conf": "mysql paste-db\nsqlite nowhere app.db\nsnapshot paste-data\nmysql paste-data\n",
	})
	f := healthyHost()
	var out bytes.Buffer
	if code := Backup(root, f.run, &out); code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, w := range []string{
		"services/paste/backup.conf:2: no compose file declares a volume called 'nowhere'",
		"services/paste/backup.conf:3: unknown directive 'snapshot'",
		"services/paste/backup.conf:4: no compose file declares a service called 'paste-data'",
	} {
		if !strings.Contains(out.String(), w) {
			t.Fatalf("no %q in:\n%s", w, out.String())
		}
	}
	if f.index("restic/restic") >= 0 {
		t.Fatal("restic ran on a backup.conf that does not parse")
	}

	unpinned := exportTree(t, map[string]string{".env": jobsEnv, ".deploy-state/images.env": "OTHER_IMAGE=x\n"})
	f = (&fakeJobs{}).on("ps -a --filter name=^japode-paste", "japode-paste\n")
	out.Reset()
	if code := Backup(unpinned, f.run, &out); code != 1 || !strings.Contains(out.String(), "paste has no image pinned and yet has containers") {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}

// JCT46: the drill checks the snapshot carries every volume or its dump, restores
// into a project of its own, puts the SQLite copy back and asks it three
// questions, starts the database alone and feeds it the dump before the service,
// starts the service under its own container names, and tears down whatever
// happened.
func TestRestoreTestRestoresAndStartsInItsOwnProject(t *testing.T) {
	root := jobsTree(t)
	f := healthyHost()
	var out bytes.Buffer
	if code := RestoreTest(root, 240, false, f.run, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if f.index("restore latest --host japode-cloud --target /restore") < 0 {
		t.Fatal("no restore")
	}
	db, feed, svc := f.index("up -d --wait --wait-timeout 240 paste-db"), f.index("exec -T paste-db sh -c MYSQL_PWD"), f.index("up -d --wait --wait-timeout 240 paste")
	if db < 0 || feed < db {
		t.Fatalf("the dump was not fed to a started database: up %d, feed %d", db, feed)
	}
	if last := f.called("up -d --wait --wait-timeout 240 paste"); !strings.HasSuffix(last[len(last)-1].String(), " paste") || svc < 0 {
		t.Fatalf("the service was not started: %v", last)
	}
	if !strings.Contains(f.called("up -d")[0].String(), "-p japode-restore-test") {
		t.Fatal("the drill did not run in its own project")
	}
	down := f.index("down -v --remove-orphans")
	if down < 0 || f.index("volume rm -f japode-restore-test__sqlite") < down {
		t.Fatal("the drill was not torn down, or its staged volumes were left")
	}

	// A volume the snapshot does not carry fails the drill.
	f = healthyHost().first(jobRule{match: "ls latest:/backup", out: "/paste-data\n/_mysql/paste-db.sql\n"})
	out.Reset()
	if code := RestoreTest(root, 240, false, f.run, &out); code != 1 || !strings.Contains(out.String(), "caddy-data is declared by a compose file and is not in the snapshot") {
		t.Fatalf("exit %d\n%s", code, out.String())
	}

	// --cleanup tears down and nothing else.
	f = healthyHost()
	out.Reset()
	if code := RestoreTest(root, 240, true, f.run, &out); code != 0 || f.index("restic/restic") >= 0 || f.index("down -v") < 0 {
		t.Fatalf("cleanup: exit %d, calls %v", code, f.calls)
	}
}

// JCT46: the reason the jobs are verbs. On a site's own repository with a jcloud/
// folder (JCT44's shape), backup reads the compose file and backup.conf where
// service_dir puts them, which a script walking services/ never found.
func TestBackupReadsTheLayoutJcloudYmlGives(t *testing.T) {
	root := outsideTree(t)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(jobsEnv), 0o600); err != nil {
		t.Fatal(err)
	}
	f := healthyHost()
	var out bytes.Buffer
	if code := Backup(root, f.run, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	compose := f.called("config --volumes")[0].String()
	if !strings.Contains(compose, filepath.Join(root, "service", "docker-compose.yml")) {
		t.Fatalf("compose did not read service/docker-compose.yml: %s", compose)
	}
	if f.index("exec -T paste-db sh -c set -eu") < 0 {
		t.Fatal("service/backup.conf's mysql directive was not read")
	}
}

// JCT46: the health watch says nothing on its first look and nothing within the
// grace, says DOWN once when a service has been bad past it, to stdout and the
// webhook as ASCII, and UP when it comes back; it exits 1 while anything is bad.
func TestHealthwatchReportsTransitionsOnce(t *testing.T) {
	root := exportTree(t, map[string]string{".env": jobsEnv + "ALERT_WEBHOOK_URL=https://hooks.example.org/x\n"})
	var posted []string
	wasPost, wasNow := healthPost, healthNow
	healthPost = func(url string, body []byte) error {
		var m map[string]string
		json.Unmarshal(body, &m)
		posted = append(posted, m["text"])
		return nil
	}
	clock := time.Unix(1_800_000_000, 0)
	healthNow = func() time.Time { return clock }
	t.Cleanup(func() { healthPost, healthNow = wasPost, wasNow })

	state := "unhealthy"
	host := func() *fakeJobs {
		return (&fakeJobs{}).
			on("config --services", "caddy\npaste\n").
			on("com.docker.compose.service=caddy", "c1\n").
			on("com.docker.compose.service=paste", "p1\n").
			on("{{.State.Status}} c1", "running\n").
			on("{{.State.Status}} p1", "running\n").
			on("{{else}}none{{end}} c1", "healthy\n").
			on("{{else}}none{{end}} p1", state+"\n")
	}
	run := func() int { return Healthwatch(root, host().run, io.Discard) }

	if code := run(); code != 1 || len(posted) != 0 {
		t.Fatalf("first look: exit %d, posted %v", code, posted)
	}
	clock = clock.Add(100 * time.Second)
	if run(); len(posted) != 0 {
		t.Fatalf("within grace: posted %v", posted)
	}
	clock = clock.Add(250 * time.Second)
	run()
	if len(posted) != 1 || posted[0] != "DOWN: japode-cloud/paste has been unhealthy for 5m, which is longer than a restart takes" {
		t.Fatalf("posted %q", posted)
	}
	clock = clock.Add(300 * time.Second)
	if run(); len(posted) != 1 {
		t.Fatalf("said twice: %v", posted)
	}
	state = "healthy"
	if code := run(); code != 0 || len(posted) != 2 || posted[1] != "UP: japode-cloud/paste is healthy again" {
		t.Fatalf("exit %d, posted %v", code, posted)
	}
	if data, err := os.ReadFile(filepath.Join(root, ".deploy-state", "health", "paste")); err != nil || !strings.HasPrefix(string(data), "1 ") {
		t.Fatalf("state file: %q %v", data, err)
	}
	if asciiLine("down — now") != "down ? now" || humanSecs(7300) != "2h1m" {
		t.Fatal("asciiLine or humanSecs")
	}
}
