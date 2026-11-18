package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// timersProfile is Japode's three jobs, declared the way host apply installs them,
// beside one timer survey only reads.
const timersProfile = `carry: [scripts]
host:
  user: {name: runner, groups: [docker]}
  requires: [docker]
  timers:
    - name: japode-backup
      run: scripts/backup.sh
      on: "*-*-* 03,09,15,21:20:00"
      timeout: 2h
      jitter: 15m
      persistent: true
      max_age: 7h
      env: [BACKUP_REPOSITORY, BACKUP_PASSWORD]
    - name: japode-restore-test
      run: scripts/restore-test.sh
      on: "Sun *-*-* 04:40:00"
      stop_post: scripts/restore-test.sh --cleanup
      timeout: 1h
    - name: japode-healthwatch
      run: scripts/healthwatch.sh
      on: "*:0/5"
      success_exit: [1]
      accuracy: 30s
    - japode-other
`

var timerScriptsTree = map[string]string{
	"scripts/backup.sh":       "#!/usr/bin/env bash\n. scripts/backup-lib.sh\n",
	"scripts/backup-lib.sh":   "# sourced\n",
	"scripts/restore-test.sh": "#!/usr/bin/env bash\n",
	"scripts/healthwatch.sh":  "#!/usr/bin/env bash\n",
}

// timerMachine is fakeMachine with systemd: a timer is active once enabled, every
// unit fires <name>.service, and every last run succeeded. log, when set, is shared
// with the docker the install runs, so the two can be ordered.
type timerMachine struct {
	*fakeMachine
	enabled map[string]bool
	log     *[]string
}

func newTimerMachine() *timerMachine {
	m := newFakeMachine()
	m.tools["docker"], m.tools["compose"] = true, true
	return &timerMachine{fakeMachine: m, enabled: map[string]bool{}}
}

func (m *timerMachine) run(name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	if m.log != nil {
		*m.log = append(*m.log, "host "+line)
	}
	if name != "systemctl" {
		return m.fakeMachine.run(name, args...)
	}
	m.ran = append(m.ran, line)
	var b strings.Builder
	switch {
	case args[0] == "is-active":
		for _, u := range args[1:] {
			if m.enabled[u] {
				b.WriteString("active\n")
			} else {
				b.WriteString("inactive\n")
			}
		}
	case args[0] == "enable":
		m.enabled[args[2]] = true
	case args[0] == "show" && args[1] == "-p":
		for _, u := range args[4:] {
			b.WriteString(strings.TrimSuffix(u, ".timer") + ".service\n")
		}
	case args[0] == "show":
		var blocks []string
		for range args[6:] {
			blocks = append(blocks, "Result=success\nExecMainExitTimestamp=")
		}
		b.WriteString(strings.Join(blocks, "\n\n") + "\n")
	}
	return b.String(), nil
}

// JCT43: a timer that says what it runs gets its two units, written from the
// declaration and saying so, and is enabled; one that does not stays survey's; and
// a second apply changes nothing.
func TestHostApplyInstallsTheTimersOnce(t *testing.T) {
	sys := withSysRoot(t, "ID=debian\nVERSION_CODENAME=bookworm\n")
	p, err := ParseProfile([]byte(timersProfile))
	if err != nil {
		t.Fatal(err)
	}
	root := layTree(t, timerScriptsTree)
	m := newTimerMachine()
	did, still, err := HostApply(root, p.Host, m.run)
	if err != nil || len(still) > 0 {
		t.Fatalf("err %v, still %v\nran:\n%s", err, still, strings.Join(m.ran, "\n"))
	}
	abs := filepath.ToSlash(root)
	unit := func(name string) string {
		return string(mustRead(t, filepath.Join(sys, "etc", "systemd", "system", name)))
	}
	backup := unit("japode-backup.service")
	for _, w := range []string{"jcloud host apply", "User=runner\n", "WorkingDirectory=" + abs + "\n",
		"ExecStart=/usr/bin/env bash " + abs + "/scripts/backup.sh\n", "Requires=docker.socket\n", "TimeoutStartSec=7200s\n"} {
		if !strings.Contains(backup, w) {
			t.Fatalf("no %q in japode-backup.service:\n%s", w, backup)
		}
	}
	for file, w := range map[string]string{
		"japode-backup.timer":         "OnCalendar=*-*-* 03,09,15,21:20:00\nRandomizedDelaySec=900s\nPersistent=true\n",
		"japode-restore-test.service": "ExecStopPost=/usr/bin/env bash " + abs + "/scripts/restore-test.sh --cleanup\n",
		"japode-healthwatch.service":  "SuccessExitStatus=1\n",
		"japode-healthwatch.timer":    "OnCalendar=*:0/5\nAccuracySec=30s\n",
		"japode-restore-test.timer":   "WantedBy=timers.target\n",
	} {
		if got := unit(file); !strings.Contains(got, w) {
			t.Fatalf("no %q in %s:\n%s", w, file, got)
		}
	}
	joined := strings.Join(did, "\n")
	for _, w := range []string{"systemctl daemon-reload", "systemctl enable --now japode-backup.timer", "systemctl enable --now japode-healthwatch.timer"} {
		if !strings.Contains(joined, w) {
			t.Fatalf("no %q in:\n%s", w, joined)
		}
	}
	if strings.Contains(joined, "japode-other") {
		t.Fatal("a timer that says nothing about what it runs was installed")
	}

	did, still, err = HostApply(root, p.Host, m.run)
	if err != nil || len(still) > 0 || len(did) > 0 {
		t.Fatalf("a second apply changed %v (err %v, still %v)", did, err, still)
	}
}

// JCT43: a timer whose script is not under the root is refused before any unit
// points at it.
func TestHostApplyRefusesATimerWithNoScript(t *testing.T) {
	withSysRoot(t, "ID=debian\nVERSION_CODENAME=bookworm\n")
	p, err := ParseProfile([]byte(timersProfile))
	if err != nil {
		t.Fatal(err)
	}
	m := newTimerMachine()
	if _, _, err := HostApply(t.TempDir(), p.Host, m.run); err == nil || !strings.Contains(err.Error(), "lay the tree down first") {
		t.Fatalf("got %v", err)
	}
	for _, l := range m.ran {
		if strings.HasPrefix(l, "systemctl enable") {
			t.Fatalf("enabled a timer with no script: %s", l)
		}
	}
}

// JCT43: what a timer runs and when is held at parse time, not at its first run.
func TestTimerDeclarationsAreValidated(t *testing.T) {
	for _, c := range []struct{ yml, want string }{
		{"host:\n  timers:\n    - {name: a, run: scripts/a.sh}\n", "no on:"},
		{"host:\n  timers:\n    - {name: a, on: daily}\n", "not what it runs"},
		{"host:\n  timers:\n    - {name: a, run: /usr/bin/a.sh, on: daily}\n", "sits under the platform root"},
		{"host:\n  timers:\n    - {name: a, run: scripts/a.sh, on: daily, jitter: soon}\n", "jitter \"soon\""},
		{"host:\n  timers:\n    - {name: a, run: scripts/a.sh, on: daily, env: [lower]}\n", "not a configuration key"},
		{"host:\n  timers:\n    - {name: a, run: scripts/a.sh, on: daily, schedule: x}\n", "field schedule not found"},
		{"carry: [../elsewhere]\n", "leaves the platform root"},
	} {
		if _, err := ParseProfile([]byte(c.yml)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%q: got %v, want %q", c.yml, err, c.want)
		}
	}
}

// timersTree is the export fixture as a platform that declares its timers.
func timersTree(t *testing.T, profile string) string {
	t.Helper()
	extra := map[string]string{
		ProfileFile: profile,
		".env":      "PASTE_DB_PASSWORD=hunter2\nPASTE_HOSTS=paste.example.org\nBACKUP_REPOSITORY=s3:example/b\nBACKUP_PASSWORD=restic-secret\n",
	}
	for k, v := range timerScriptsTree {
		extra[k] = v
	}
	return exportTree(t, extra)
}

// JCT43: export carries what carry: names, as the platform's, and the keys the
// timers read, sealed; a timer running a script no carry: path covers is refused.
func TestExportCarriesTheTimersScriptsAndKeys(t *testing.T) {
	root := timersTree(t, timersProfile)
	id := testRecipient(t)
	idx, files := exportAndRead(t, root, id.Recipient().String())
	for _, s := range []string{"scripts/backup.sh", "scripts/backup-lib.sh", "scripts/restore-test.sh", "scripts/healthwatch.sh"} {
		if _, ok := files[s]; !ok {
			t.Fatalf("%s not carried", s)
		}
	}
	for _, f := range idx.Files {
		if strings.HasPrefix(f.Path, "scripts/") && !f.Shared {
			t.Fatalf("%s is not marked the platform's", f.Path)
		}
	}
	env := openedEnv(t, files[ExportEnvMember], id)
	if env["BACKUP_REPOSITORY"] != "s3:example/b" || env["BACKUP_PASSWORD"] != "restic-secret" {
		t.Fatalf("the timers' keys did not travel: %v", env)
	}

	bare := timersTree(t, strings.Replace(timersProfile, "carry: [scripts]\n", "", 1))
	_, _, problems := ExportSet(bare, "paste")
	if !strings.Contains(strings.Join(problems, "\n"), "host.timers runs scripts/backup.sh, and no carry: path covers it") {
		t.Fatalf("problems: %v", problems)
	}
}

// JCT43: install prepares the host without the timers, lays the tree down and
// starts the service, and installs the timers last, so their scripts are there.
func TestInstallInstallsTheTimersLast(t *testing.T) {
	withSysRoot(t, "ID=debian\nVERSION_CODENAME=bookworm\n")
	root := timersTree(t, timersProfile)
	id := testRecipient(t)
	archive := filepath.Join(t.TempDir(), "paste.tar.gz")
	if code := export([]string{"paste", "--root", root, "--recipient", id.Recipient().String(), "-o", archive}); code != 0 {
		t.Fatalf("export: exit %d", code)
	}
	t.Setenv("JCLOUD_IDENTITY", id.String())

	var log []string
	m := newTimerMachine()
	m.log = &log
	was := hostRun
	hostRun = m.run
	t.Cleanup(func() { hostRun = was })
	d := newFakeInstall()
	docker := func(stdin io.Reader, stdout io.Writer, name string, args ...string) error {
		log = append(log, "docker "+strings.Join(args, " "))
		return d.run(stdin, stdout, name, args...)
	}

	target := t.TempDir()
	if code := runInstall(archive, target, "", 60, registryLogin{}, docker); code != 0 {
		t.Fatalf("exit %d\n%s", code, strings.Join(log, "\n"))
	}
	first := func(prefix string) int {
		for i, l := range log {
			if strings.HasPrefix(l, prefix) {
				return i
			}
		}
		return -1
	}
	user, up, enable := first("host useradd"), -1, first("host systemctl enable --now japode-backup.timer")
	for i, l := range log {
		if strings.HasPrefix(l, "docker compose") && strings.Contains(l, " up ") {
			up = i
		}
	}
	if user < 0 || up < 0 || enable < 0 || !(user < up && up < enable) {
		t.Fatalf("order: useradd %d, last up %d, enable %d\n%s", user, up, enable, strings.Join(log, "\n"))
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(target, ".env"))), "BACKUP_PASSWORD=restic-secret\n") {
		t.Fatal("the timers' keys were not merged into .env")
	}
}
