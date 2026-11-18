// `jcloud restore-test` — put the newest backup back and ask the result whether it
// works (JCT46, the port of Japode's scripts/restore-test.sh).
//
// A backup nobody restored is an assumption. The mistakes live on this side: a
// copy caught mid-write, a volume whose name changed under the job, a permission
// the container no longer has, and each is found during the outage unless
// something like this runs first. What it asserts, in order:
//
//  1. the newest snapshot carries every volume the compose files declare, or its
//     dump for a dumped one; this catches a volume renamed on one side only
//  2. every volume restored with something in it
//  3. every SQLite database in backup.conf restores to its path and passes
//     integrity_check and foreign_key_check, and holds tables
//  4. every dump is fed into a server started on an empty volume and comes back
//     with tables
//  5. every service starts against the restored volumes and its own healthcheck
//     passes, the only step that can catch a permission the container lost
//
// Step 4 before 5: a dump is fed to a running server, so the database comes up
// alone first, and `up --wait` on the service would start it as a dependency and
// hand the application an empty one that comes up healthy.
//
// Nothing it touches is the live stack: everything lands in a compose project of
// its own (<prefix>restore-test), and it is torn down however this exits.
// `--cleanup` is the same teardown on its own, for the unit's ExecStopPost, which
// runs after a kill the process never sees.
package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"syscall"
)

// restoreWaitTimeout is the ordinary budget for `up --wait`, the deploy's own
// (Japode's wait-budget.sh). A service whose first boot needs longer is given it
// with --wait-timeout, which the timer's run: line states.
const restoreWaitTimeout = 240

// RestoreTest runs the drill at root and answers its exit code. cleanup only
// tears down what an earlier drill left.
func RestoreTest(root string, wait int, cleanup bool, run jobRunner, w io.Writer) int {
	p, err := loadJobPlatform(root, run, w)
	if err == nil {
		err = p.refuseUndeployedWithContainers()
	}
	if err == nil {
		err = p.loadVolumes()
	}
	if err == nil && !cleanup {
		err = p.readBackupConf()
	}
	var env []string
	if err == nil && !cleanup {
		env, err = p.resticConfig()
	}
	if err != nil {
		fmt.Fprintf(w, "\n\033[31mfailed: %v\033[0m\n", err)
		return 1
	}

	if cleanup {
		p.say("removing anything a previous drill left behind")
		p.teardown("")
		for _, key := range append(append([]string{}, p.volumes...), sqliteKey, mysqlKey) {
			name := p.drillProject() + "_" + key
			if p.run(jobCmd{Name: "docker", Args: []string{"volume", "inspect", name}}) == nil {
				fmt.Fprintf(w, "    \033[31m%s survived\033[0m\n", name)
			} else {
				p.note("%s is gone", name)
			}
		}
		fmt.Fprintf(w, "\n\033[32mnothing of the drill is left\033[0m\n")
		return 0
	}

	if len(p.undeployed) > 0 {
		p.say("not in this drill: %s", strings.Join(p.undeployed, " "))
		for _, svc := range p.undeployed {
			p.note("%s has no image pinned, so the backup holds nothing of its to restore", svc)
		}
	}

	override := ""
	defer func() {
		p.say("tearing the drill down")
		p.teardown(override)
	}()
	// A kill reaches the deferred teardown only through here; ExecStopPost is the
	// other path for one this process never sees.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer func() { signal.Stop(sig); close(sig) }()
	go func() {
		if _, ok := <-sig; ok {
			p.teardown(override)
			os.Exit(1)
		}
	}()

	if code := p.drillCoverage(env); code != 0 {
		return code
	}
	if code := p.drillRestore(env); code != 0 {
		return code
	}
	p.drillDatabases()
	override = p.drillStart(wait)

	if p.failures == 0 {
		fmt.Fprintf(w, "\n\033[32mthe backup restores\033[0m\n")
		return 0
	}
	fmt.Fprintf(w, "\n\033[31m%d check(s) failed: the backup does not restore as it stands\033[0m\n", p.failures)
	return 1
}

// teardown removes everything the drill created: its compose project with its
// volumes, and by name the ones no compose file declares (the staged databases),
// which `down -v` alone would leave.
func (p *jobPlatform) teardown(override string) {
	args := []string{"-p", p.drillProject()}
	if override != "" {
		args = append(args, "-f", override)
	}
	p.run(p.compose(append(args, "down", "-v", "--remove-orphans")...))
	for _, key := range append(append([]string{}, p.volumes...), sqliteKey, mysqlKey) {
		p.run(jobCmd{Name: "docker", Args: []string{"volume", "rm", "-f", p.drillProject() + "_" + key}})
	}
	if override != "" {
		os.Remove(override)
	}
}

// drillCoverage is step 1.
func (p *jobPlatform) drillCoverage(env []string) int {
	p.say("repository: %s", p.env["BACKUP_REPOSITORY"])
	if err := p.run(p.restic(env, nil, "cat", "config")); err != nil {
		fmt.Fprintf(p.w, "\n\033[31mfailed: no repository at %s, or the password does not open it: nothing has been backed up yet\033[0m\n", p.env["BACKUP_REPOSITORY"])
		return 1
	}
	listing, err := p.out(p.restic(env, nil, "ls", "latest:"+snapshotRoot, "--host", p.project))
	if err != nil {
		fmt.Fprintf(p.w, "\n\033[31mfailed: the repository holds no snapshot for host %s with anything under %s: has jcloud backup ever run?\033[0m\n", p.project, snapshotRoot)
		return 1
	}
	have := map[string]bool{}
	for _, l := range strings.Split(listing, "\n") {
		have[strings.TrimSpace(l)] = true
	}
	p.say("does the newest snapshot carry every volume?")
	for _, key := range p.volumes {
		switch {
		case p.dumped(key) && have["/"+mysqlKey+"/"+key+".sql"]:
			p.pass("%s (dumped, not copied: %s/%s.sql)", key, mysqlKey, key)
		case p.dumped(key):
			p.fail("%s is dumped rather than copied and %s/%s.sql is not in the snapshot: the volume is excluded on purpose, so this dump is the only copy of that database", key, mysqlKey, key)
		case have["/"+key]:
			p.pass("%s", key)
		default:
			p.fail("%s is declared by a compose file and is not in the snapshot", key)
		}
	}
	return 0
}

// drillRestore is step 2: the newest snapshot into the drill's own volumes.
func (p *jobPlatform) drillRestore(env []string) int {
	p.say("restoring into %s", p.drillProject())
	var mounts []string
	for _, key := range append(append([]string{}, p.volumes...), sqliteKey, mysqlKey) {
		name := p.drillProject() + "_" + key
		p.run(jobCmd{Name: "docker", Args: []string{"volume", "create", name}})
		mounts = append(mounts, name+":/restore"+snapshotRoot+"/"+key)
	}
	if err := p.run(p.restic(env, mounts, "restore", "latest", "--host", p.project, "--target", "/restore")); err != nil {
		fmt.Fprintf(p.w, "\n\033[31mfailed: restic restore: %v\033[0m\n", err)
		return 1
	}
	p.pass("restored")

	// The check that catches a volume renamed on one side only: the backup and the
	// drill read the same compose file and agree about the wrong name, and what
	// notices is that the copy has nothing in it.
	p.say("is anything actually in there?")
	for _, key := range p.volumes {
		n := p.fileCount(p.drillProject() + "_" + key)
		switch {
		case n > 0:
			p.pass("%s restored %d file(s)", key, n)
		case p.holdsSQLite(key):
			p.note("%s holds only databases, which are put back below", key)
		case p.dumped(key):
			p.note("%s is dumped rather than copied, and the dump is fed in below", key)
		default:
			p.fail("%s restored empty: either the service never wrote to it, or the volume was renamed and the backup has been copying a new empty one", key)
		}
	}
	return 0
}

func (p *jobPlatform) holdsSQLite(key string) bool {
	for _, e := range p.sqliteFiles {
		if e.key == key {
			return true
		}
	}
	return false
}

// drillDatabases is step 3: each SQLite copy put where the service opens it, and
// asked three questions.
func (p *jobPlatform) drillDatabases() {
	if len(p.sqliteFiles) == 0 {
		return
	}
	p.say("putting the databases back where the service looks for them")
	for _, e := range p.sqliteFiles {
		if err := p.run(p.sh([]string{p.drillProject() + "_" + sqliteKey + ":/sqlite:ro", p.drillProject() + "_" + e.key + ":/vol"},
			`set -eu; mkdir -p "/vol/$(dirname "$2")"; cp "/sqlite/$1/$2" "/vol/$2"; rm -f "/vol/$2-wal" "/vol/$2-shm"`, e.key, e.path)); err != nil {
			p.fail("%s:/%s is not in the snapshot", e.key, e.path)
			continue
		}
		p.pass("%s:/%s", e.key, e.path)
	}
	p.say("asking the restored databases whether they are intact")
	for _, e := range p.sqliteFiles {
		mount := p.drillProject() + "_" + e.key + ":/vol:ro"
		file := path.Join("/vol", e.path)
		if answer := p.sqliteIntact(mount, file); answer != "" {
			p.fail("%s:/%s integrity_check: %s", e.key, e.path, answer)
			continue
		}
		p.pass("%s:/%s integrity_check", e.key, e.path)
		// A page-level check passes on rows that point at rows that are gone, which is
		// what a copy caught mid-write looks like from above.
		broken, err := p.out(p.sqlite([]string{mount}, "sqlite3", file, "PRAGMA foreign_key_check;"))
		switch {
		case err != nil:
			p.fail("%s:/%s foreign_key_check: could not be asked", e.key, e.path)
		case strings.TrimSpace(broken) != "":
			p.fail("%s:/%s foreign_key_check: %s", e.key, e.path, lastLines(strings.TrimSpace(broken), 3))
		default:
			p.pass("%s:/%s foreign_key_check", e.key, e.path)
		}
		// The real query: a database that restored to an empty file opens, passes
		// both pragmas and answers 0 here.
		out, _ := p.out(p.sqlite([]string{mount}, "sqlite3", file, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%';"))
		if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil && n > 0 {
			p.pass("%s:/%s holds %d table(s)", e.key, e.path, n)
		} else {
			p.fail("%s:/%s restored with no tables in it", e.key, e.path)
		}
	}
}

// drillStart is steps 4 and 5, under an override that clears each service's fixed
// container_name, or the drill would collide with what serves traffic. It answers
// the override's path, for the teardown.
func (p *jobPlatform) drillStart(wait int) string {
	p.say("starting the services against the restored volumes")
	services, err := p.composeServices()
	if err != nil {
		p.fail("the compose services could not be read: %v", err)
		return ""
	}
	f, err := os.CreateTemp("", "jcloud-restore-test-*.yml")
	if err != nil {
		p.fail("%v", err)
		return ""
	}
	fmt.Fprintln(f, "services:")
	for _, svc := range services {
		fmt.Fprintf(f, "  %s:\n    container_name: %s-%s\n", svc, p.drillProject(), svc)
	}
	f.Close()
	override := f.Name()
	drill := func(args ...string) jobCmd {
		return p.compose(append([]string{"-f", override, "-p", p.drillProject()}, args...)...)
	}
	budget := strconv.Itoa(wait)

	for _, key := range p.mysql {
		if err := p.run(drill("up", "-d", "--wait", "--wait-timeout", budget, key)); err != nil {
			p.fail("%s did not start on an empty volume, so its dump could not be fed back", key)
			continue
		}
		pr, pw := io.Pipe()
		done := make(chan error, 1)
		go func() {
			c := p.sh([]string{p.drillProject() + "_" + mysqlKey + ":/mysql:ro"}, `cat "/mysql/$1.sql"`, key)
			c.Stdout = pw
			err := p.run(c)
			pw.CloseWithError(err)
			done <- err
		}()
		feed := drill("exec", "-T", key, "sh", "-c", `MYSQL_PWD="$MARIADB_PASSWORD" exec mariadb --user="$MARIADB_USER"`)
		feed.Stdin = pr
		err := p.run(feed)
		pr.CloseWithError(io.ErrClosedPipe)
		if catErr := <-done; err != nil || catErr != nil {
			p.fail("%s: the dump is not in the snapshot, or the server refused it", key)
			continue
		}
		out, _ := p.out(drill("exec", "-T", key, "sh", "-c",
			`MYSQL_PWD="$MARIADB_PASSWORD" exec mariadb --user="$MARIADB_USER" --batch --skip-column-names -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE();" "$MARIADB_DATABASE"`))
		if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil && n > 0 {
			p.pass("%s restored %d table(s)", key, n)
		} else {
			p.fail("%s restored with no tables in it", key)
		}
	}

	if len(p.services) == 0 {
		p.note("nothing is deployed here, so there is nothing to start; every check above still ran")
		return override
	}
	if err := p.run(drill(append([]string{"up", "-d", "--wait", "--wait-timeout", budget}, p.services...)...)); err == nil {
		for _, svc := range p.services {
			p.pass("%s came up healthy on restored data", svc)
		}
		return override
	}
	for _, svc := range p.services {
		state, _ := p.out(drill("ps", "--format", "{{.State}} {{.Health}}", svc))
		if strings.TrimSpace(state) == "" {
			state = "unknown"
		}
		p.fail("%s did not come up healthy on restored data (%s)", svc, strings.TrimSpace(state))
	}
	logs := drill(append([]string{"logs", "--tail", "40"}, p.services...)...)
	logs.Stdout = p.w
	p.run(logs)
	return override
}

// restoreTestCmd is the command.
func restoreTestCmd(args []string) int {
	usage := "jcloud restore-test [--root <dir>] [--wait-timeout <seconds>] [--cleanup]"
	root, wait, cleanup := ".", restoreWaitTimeout, false
	if v, err := strconv.Atoi(os.Getenv("WAIT_TIMEOUT")); err == nil && v > 0 {
		wait = v
	}
	for len(args) > 0 {
		switch {
		case args[0] == "--cleanup":
			cleanup, args = true, args[1:]
		case args[0] == "--root" && len(args) >= 2:
			root, args = args[1], args[2:]
		case args[0] == "--wait-timeout" && len(args) >= 2:
			n, err := strconv.Atoi(args[1])
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, usage)
				return 2
			}
			wait, args = n, args[2:]
		default:
			fmt.Fprintln(os.Stderr, usage)
			return 2
		}
	}
	return RestoreTest(root, wait, cleanup, runJobCmd, os.Stdout)
}
