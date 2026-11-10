package main

import (
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// JCT23: the host block as a platform writes it. JCT24: a timer may carry the
// oldest last run it accepts.
const hostProfile = `
host:
  timers: [viglet-restore-test, {name: viglet-volumes-backup, max_age: 26h}]
  paths:
    - path: .
      mode: "750"
      owner: runner:runner
    - path: /etc/viglet
      mode: "700"
  firewall: ufw
`

var declaredHost = Host{
	Timers:   []HostTimer{{Name: "viglet-restore-test"}, {Name: "viglet-volumes-backup", MaxAge: "26h"}},
	Paths:    []HostPath{{".", "750", "runner:runner"}, {"/etc/viglet", "700", ""}},
	Firewall: "ufw",
}

func TestHostIsReadFromTheProfile(t *testing.T) {
	prof, err := ParseProfile([]byte(hostProfile))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prof.Host, declaredHost) {
		t.Fatalf("got %+v", prof.Host)
	}
	if !reflect.DeepEqual(DefaultProfile().Host, Host{}) {
		t.Fatal("the default profile must assert nothing about the host")
	}
}

const badHost = `
host:
  timers: [backup.timer, "two words", {name: nightly, max_age: 2d}]
  paths:
    - path: "has space"
      mode: "750"
    - path: .
      mode: rwx
      owner: root
    - path: /srv
  firewall: nftables
`

func TestABadHostIsRefused(t *testing.T) {
	// A misspelt key inside host: is refused like one at the top, and inside a
	// timer too, where KnownFields does not reach.
	if _, err := ParseProfile([]byte(badHost + "  colour: blue\n")); err == nil || !strings.Contains(err.Error(), "colour") {
		t.Fatalf("got %v", err)
	}
	if _, err := ParseProfile([]byte("host:\n  timers: [{name: x, maxage: 1h}]\n")); err == nil || !strings.Contains(err.Error(), "maxage not found") {
		t.Fatalf("got %v", err)
	}
	// Every mistake at once.
	_, err := ParseProfile([]byte(badHost))
	for _, want := range []string{
		`"backup.timer" names the unit without .timer`,
		`"two words" is not a systemd unit name`,
		`nightly has max_age "2d"`,
		`"has space" is empty or has a space`,
		`mode "rwx"`,
		`owner "root"`,
		"/srv declares neither mode nor owner",
		`host.firewall is "nftables"`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
}

// A host that answers the way systemctl, stat and ufw do.
type fakeHostAnswers struct {
	active, units, runs string // stdout of is-active, show -p Unit, show -p Result…
	timerErr            error
	stat                map[string]string
	ufw                 string
	ufwErr              error
}

func (f fakeHostAnswers) run(name string, args ...string) (string, error) {
	switch {
	case name == "systemctl" && args[0] == "is-active":
		return f.active, f.timerErr
	case name == "systemctl" && contains(args, "Unit"):
		return f.units, f.timerErr
	case name == "systemctl" && contains(args, "--timestamp=unix"):
		return f.runs, f.timerErr
	case name == "stat":
		if out, ok := f.stat[args[len(args)-1]]; ok {
			return out, nil
		}
		return "", errors.New("stat: cannot statx: No such file or directory")
	case name == "ufw":
		return f.ufw, f.ufwErr
	}
	return "", fmt.Errorf("observe ran %s %v", name, args)
}

func atClock(t *testing.T, at int64) {
	t.Helper()
	was := now
	now = func() time.Time { return time.Unix(at, 0) }
	t.Cleanup(func() { now = was })
}

func TestObserveReadsOnlyWhatHostNames(t *testing.T) {
	atClock(t, 1_800_000_000)
	answers := fakeHostAnswers{
		active:   "inactive\nactive\n",
		timerErr: errors.New("exit status 3"), // is-active's answer for one inactive unit
		units:    "viglet-restore-test.service\n\nviglet-volumes-backup.service\n",
		runs:     "Result=success\nExecMainExitTimestamp=\n\nResult=exit-code\nExecMainExitTimestamp=@1799990000\n",
		stat:     map[string]string{"/srv/platform": "750 runner:runner\n", "/etc/viglet": "755 root:root\n"},
		ufw:      "Status: inactive\n",
	}
	lines := observeHost("/srv/platform", declaredHost, answers.run)
	want := []string{
		"timer viglet-restore-test inactive",
		"lastrun viglet-restore-test success",
		"finished viglet-restore-test never",
		"timer viglet-volumes-backup active",
		"lastrun viglet-volumes-backup exit-code",
		"finished viglet-volumes-backup 1799990000",
		"clock host 1800000000",
		"path . 750 runner:runner",
		"path /etc/viglet 755 root:root",
		"firewall ufw inactive",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("got\n  %s", strings.Join(lines, "\n  "))
	}
	if got := observeHost("/srv/platform", Host{}, answers.run); len(got) != 0 {
		t.Fatalf("an undeclared host is not read, got %v", got)
	}
}

// What could not be measured is said so, never reported as a value.
func TestObserveNamesWhatItCouldNotRead(t *testing.T) {
	missing := fmt.Errorf("exec: %w", exec.ErrNotFound)
	lines := observeHost("/srv/platform", declaredHost, fakeHostAnswers{timerErr: missing, ufwErr: missing}.run)
	for _, want := range []string{
		"timer viglet-volumes-backup unknown",
		"lastrun viglet-volumes-backup unknown",
		"finished viglet-volumes-backup unknown",
		"path . unknown unknown",
		"firewall ufw absent",
	} {
		if !contains(lines, want) {
			t.Errorf("want %q in %v", want, lines)
		}
	}
	rootless := fakeHostAnswers{ufw: "", ufwErr: errors.New("ERROR: You need to be root to run this script")}
	if got := observeHost("/r", Host{Firewall: "ufw"}, rootless.run); !reflect.DeepEqual(got, []string{"firewall ufw unreadable"}) {
		t.Errorf("got %v", got)
	}
	// A systemd too old for --timestamp=unix prints a date this does not parse.
	old := fakeHostAnswers{active: "active\n", units: "a.service\n", runs: "Result=success\nExecMainExitTimestamp=Tue 2026-11-10 03:20:05 UTC\n"}
	if got := observeTimers([]HostTimer{{Name: "a"}}, old.run); !contains(got, "finished a unknown") {
		t.Errorf("got %v", got)
	}
}

const agreeingHost = `timer viglet-restore-test active
lastrun viglet-restore-test success
timer viglet-volumes-backup active
lastrun viglet-volumes-backup success
finished viglet-volumes-backup 1799990000
clock host 1800000000
path . 0750 runner:runner
path /etc/viglet 700 root:root
firewall ufw active
`

func TestSurveyHoldsTheHostToWhatIsDeclared(t *testing.T) {
	if got := surveyHost(declaredHost, ParseObserved(agreeingHost)); len(got) != 0 {
		t.Fatalf("an agreeing host reported %+v", got)
	}
	for _, c := range []struct{ name, old, new, service, want string }{
		{"a timer never installed", "timer viglet-restore-test active", "timer viglet-restore-test inactive", "viglet-restore-test.timer", "is inactive, not active"},
		{"a timer not reported", "timer viglet-restore-test active\n", "", "viglet-restore-test.timer", "do not report it"},
		{"systemd could not be asked", "timer viglet-restore-test active", "timer viglet-restore-test unknown", "viglet-restore-test.timer", "could not be read"},
		{"the job fails every run", "lastrun viglet-restore-test success", "lastrun viglet-restore-test exit-code", "viglet-restore-test.timer", "ended in exit-code"},
		{"the last run went unread", "lastrun viglet-restore-test success", "lastrun viglet-restore-test unknown", "viglet-restore-test.timer", "could not be read"},
		{"the last run not reported", "lastrun viglet-restore-test success\n", "", "viglet-restore-test.timer", "do not say how its last run ended"},
		// 1799900000 is 27h46m40s before the clock, and rounds to the minute.
		{"the copy is too old", "finished viglet-volumes-backup 1799990000", "finished viglet-volumes-backup 1799900000", "viglet-volumes-backup.timer", "last finished 27h47m0s ago, older than the 26h"},
		{"it never ran", "finished viglet-volumes-backup 1799990000", "finished viglet-volumes-backup never", "viglet-volumes-backup.timer", "never finished a run"},
		{"its finish went unread", "finished viglet-volumes-backup 1799990000", "finished viglet-volumes-backup unknown", "viglet-volumes-backup.timer", "could not be read"},
		{"a mode drifted", "path . 0750", "path . 755", ".", "is mode 755 and not 750"},
		{"an owner drifted", "runner:runner", "root:root", ".", "owned by root:root and not runner:runner"},
		{"stat failed", "path /etc/viglet 700 root:root", "path /etc/viglet unknown unknown", "/etc/viglet", "stat failed"},
		{"the firewall is off", "firewall ufw active", "firewall ufw inactive", "ufw", "is inactive"},
		{"no firewall", "firewall ufw active", "firewall ufw absent", "ufw", "not installed"},
		{"read without root", "firewall ufw active", "firewall ufw unreadable", "ufw", "needs root"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := surveyHost(declaredHost, ParseObserved(strings.Replace(agreeingHost, c.old, c.new, 1)))
			if len(got) != 1 || got[0].Service != c.service || !strings.Contains(got[0].Says, c.want) {
				t.Fatalf("want one %s divergence saying %q, got %+v", c.service, c.want, got)
			}
		})
	}

	// An inactive timer is one fault, not three: its last run is not also reported.
	inactive := strings.NewReplacer("timer viglet-volumes-backup active", "timer viglet-volumes-backup inactive",
		"lastrun viglet-volumes-backup success", "lastrun viglet-volumes-backup exit-code",
		"finished viglet-volumes-backup 1799990000", "finished viglet-volumes-backup never").Replace(agreeingHost)
	if got := surveyHost(declaredHost, ParseObserved(inactive)); len(got) != 1 {
		t.Fatalf("got %+v", got)
	}

	// Without the host's clock the age is counted from survey's own.
	atClock(t, 1_800_000_000+100*3600)
	noClock := strings.Replace(agreeingHost, "clock host 1800000000\n", "", 1)
	if got := surveyHost(declaredHost, ParseObserved(noClock)); len(got) != 1 || !strings.Contains(got[0].Says, "older than the 26h") {
		t.Fatalf("got %+v", got)
	}
}

// Through the commands: jcloud.yml's host: reaches observe and survey both.
func TestObserveAndSurveyCarryTheHost(t *testing.T) {
	noEnv(t)
	root := observeTree(t, map[string]string{"jcloud.yml": "host:\n  timers: [japode-backup]\n"})
	docker := fakeDocker(upPasteAndEdge, map[string]string{"sha256:img1": "ghcr.io/x/paste@sha256:aaaa"})
	answers := fakeHostAnswers{active: "active\n", units: "japode-backup.service\n", runs: "Result=exit-code\nExecMainExitTimestamp=@1\n"}
	run := func(name string, args ...string) (string, error) {
		if name == "systemctl" {
			return answers.run(name, args...)
		}
		return docker(name, args...)
	}
	lines, err := Observe(root, run)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(lines, "lastrun japode-backup exit-code") {
		t.Fatalf("got %v", lines)
	}
	d, err := DeclaredIn(root)
	if err != nil {
		t.Fatal(err)
	}
	got := SurveyAgainst(d, ParseObserved(strings.Join(lines, "\n")))
	last := got[len(got)-1]
	if last.Service != "japode-backup.timer" || !strings.Contains(last.Says, "ended in exit-code") {
		t.Fatalf("got %+v", got)
	}
}
