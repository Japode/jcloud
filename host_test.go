package main

import (
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// JCT23: the host block as a platform writes it.
const hostProfile = `
host:
  timers: [viglet-volumes-backup, viglet-restore-test]
  paths:
    - path: .
      mode: "750"
      owner: runner:runner
    - path: /etc/viglet
      mode: "700"
  firewall: ufw
`

func TestHostIsReadFromTheProfile(t *testing.T) {
	prof, err := ParseProfile([]byte(hostProfile))
	if err != nil {
		t.Fatal(err)
	}
	want := Host{
		Timers:   []string{"viglet-volumes-backup", "viglet-restore-test"},
		Paths:    []HostPath{{".", "750", "runner:runner"}, {"/etc/viglet", "700", ""}},
		Firewall: "ufw",
	}
	if !reflect.DeepEqual(prof.Host, want) {
		t.Fatalf("got %+v", prof.Host)
	}
	if !reflect.DeepEqual(DefaultProfile().Host, Host{}) {
		t.Fatal("the default profile must assert nothing about the host")
	}
}

const badHost = `
host:
  timers: [backup.timer, "two words"]
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
	// A misspelt key inside host: is refused like one at the top.
	if _, err := ParseProfile([]byte(badHost + "  colour: blue\n")); err == nil || !strings.Contains(err.Error(), "colour") {
		t.Fatalf("got %v", err)
	}
	// Every mistake at once.
	_, err := ParseProfile([]byte(badHost))
	for _, want := range []string{
		`"backup.timer" names the unit without .timer`,
		`"two words" is not a systemd unit name`,
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
	timers   string // systemctl is-active's stdout
	timerErr error
	stat     map[string]string
	ufw      string
	ufwErr   error
}

func (f fakeHostAnswers) run(name string, args ...string) (string, error) {
	switch name {
	case "systemctl":
		return f.timers, f.timerErr
	case "stat":
		if out, ok := f.stat[args[len(args)-1]]; ok {
			return out, nil
		}
		return "", errors.New("stat: cannot statx: No such file or directory")
	case "ufw":
		return f.ufw, f.ufwErr
	}
	return "", fmt.Errorf("observe ran %s", name)
}

var declaredHost = Host{
	Timers:   []string{"viglet-volumes-backup", "viglet-restore-test"},
	Paths:    []HostPath{{".", "750", "runner:runner"}, {"/etc/viglet", "700", ""}},
	Firewall: "ufw",
}

func TestObserveReadsOnlyWhatHostNames(t *testing.T) {
	answers := fakeHostAnswers{
		timers:   "active\ninactive\n",
		timerErr: errors.New("exit status 3"), // is-active's answer for one inactive unit
		stat:     map[string]string{"/srv/platform": "750 runner:runner\n", "/etc/viglet": "755 root:root\n"},
		ufw:      "Status: inactive\n",
	}
	lines := observeHost("/srv/platform", declaredHost, answers.run)
	want := []string{
		"timer viglet-volumes-backup active",
		"timer viglet-restore-test inactive",
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
	for _, want := range []string{"timer viglet-volumes-backup unknown", "path . unknown unknown", "firewall ufw absent"} {
		if !contains(lines, want) {
			t.Errorf("want %q in %v", want, lines)
		}
	}
	rootless := fakeHostAnswers{ufw: "", ufwErr: errors.New("ERROR: You need to be root to run this script")}
	if got := observeHost("/r", Host{Firewall: "ufw"}, rootless.run); !reflect.DeepEqual(got, []string{"firewall ufw unreadable"}) {
		t.Errorf("got %v", got)
	}
}

func TestSurveyHoldsTheHostToWhatIsDeclared(t *testing.T) {
	agreeing := "timer viglet-volumes-backup active\ntimer viglet-restore-test active\n" +
		"path . 0750 runner:runner\npath /etc/viglet 700 root:root\nfirewall ufw active\n"
	if got := surveyHost(declaredHost, ParseObserved(agreeing)); len(got) != 0 {
		t.Fatalf("an agreeing host reported %+v", got)
	}
	for _, c := range []struct{ name, old, new, service, want string }{
		{"a timer never installed", "timer viglet-restore-test active", "timer viglet-restore-test inactive", "viglet-restore-test.timer", "is inactive, not active"},
		{"a timer not reported", "timer viglet-restore-test active\n", "", "viglet-restore-test.timer", "do not report it"},
		{"systemd could not be asked", "timer viglet-restore-test active", "timer viglet-restore-test unknown", "viglet-restore-test.timer", "could not be read"},
		{"a mode drifted", "path . 0750", "path . 755", ".", "is mode 755 and not 750"},
		{"an owner drifted", "runner:runner", "root:root", ".", "owned by root:root and not runner:runner"},
		{"stat failed", "path /etc/viglet 700 root:root", "path /etc/viglet unknown unknown", "/etc/viglet", "stat failed"},
		{"the firewall is off", "firewall ufw active", "firewall ufw inactive", "ufw", "is inactive"},
		{"no firewall", "firewall ufw active", "firewall ufw absent", "ufw", "not installed"},
		{"read without root", "firewall ufw active", "firewall ufw unreadable", "ufw", "needs root"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := surveyHost(declaredHost, ParseObserved(strings.Replace(agreeing, c.old, c.new, 1)))
			if len(got) != 1 || got[0].Service != c.service || !strings.Contains(got[0].Says, c.want) {
				t.Fatalf("want one %s divergence saying %q, got %+v", c.service, c.want, got)
			}
		})
	}
}

// Through the commands: jcloud.yml's host: reaches observe and survey both.
func TestObserveAndSurveyCarryTheHost(t *testing.T) {
	noEnv(t)
	root := observeTree(t, map[string]string{"jcloud.yml": "host:\n  timers: [japode-backup]\n"})
	docker := fakeDocker(upPasteAndEdge, map[string]string{"sha256:img1": "ghcr.io/x/paste@sha256:aaaa"})
	run := func(name string, args ...string) (string, error) {
		if name == "systemctl" {
			return "failed\n", errors.New("exit status 3")
		}
		return docker(name, args...)
	}
	lines, err := Observe(root, run)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(lines, "timer japode-backup failed") {
		t.Fatalf("got %v", lines)
	}
	d, err := DeclaredIn(root)
	if err != nil {
		t.Fatal(err)
	}
	got := SurveyAgainst(d, ParseObserved(strings.Join(lines, "\n")))
	last := got[len(got)-1]
	if last.Service != "japode-backup.timer" || !strings.Contains(last.Says, "is failed") {
		t.Fatalf("got %+v", got)
	}
}
