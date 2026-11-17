package main

import (
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

const setupProfile = `host:
  user: {name: runner, groups: [docker]}
  firewall: {kind: ufw, allow: [OpenSSH, 80/tcp, 443/udp]}
  sysctl:
    net.ipv6.conf.all.disable_ipv6:
      value: "1"
      why: right only while no name on this host has an AAAA record
    vm.swappiness: "10"
  requires: [docker, compose, rsync]
`

// JCT40: host: states the deploy user, the firewall's allow rules, kernel settings
// with their reason, and the tools the deploy runs.
func TestHostSetupParses(t *testing.T) {
	p, err := ParseProfile([]byte(setupProfile))
	if err != nil {
		t.Fatal(err)
	}
	h := p.Host
	if !reflect.DeepEqual(h.User, &HostUser{Name: "runner", Groups: []string{"docker"}}) {
		t.Fatalf("user: %+v", h.User)
	}
	if h.Firewall != "ufw" || !reflect.DeepEqual(h.FirewallAllow, []string{"OpenSSH", "80/tcp", "443/udp"}) {
		t.Fatalf("firewall: %s %v", h.Firewall, h.FirewallAllow)
	}
	want := []HostSysctl{
		{"net.ipv6.conf.all.disable_ipv6", "1", "right only while no name on this host has an AAAA record"},
		{"vm.swappiness", "10", ""},
	}
	if !reflect.DeepEqual(h.Sysctl, want) {
		t.Fatalf("sysctl: %+v", h.Sysctl)
	}
	if !reflect.DeepEqual(h.Requires, []string{"docker", "compose", "rsync"}) {
		t.Fatalf("requires: %v", h.Requires)
	}

	// The bare kind still reads as it did.
	if p, err := ParseProfile([]byte("host:\n  firewall: ufw\n")); err != nil || p.Host.Firewall != "ufw" || p.Host.FirewallAllow != nil {
		t.Fatalf("bare firewall: %v %+v", err, p)
	}
}

// JCT40: each declaration is held to one word, a misspelt key is refused, and an
// allow rule needs a firewall to hold it.
func TestHostSetupRefusesWhatItCannotCheck(t *testing.T) {
	for _, c := range []struct{ name, yml, want string }{
		{"misspelt allow", "host:\n  firewall: {kind: ufw, alow: [22/tcp]}\n", "alow"},
		{"misspelt why", "host:\n  sysctl:\n    vm.swappiness: {value: \"1\", wy: x}\n", "wy"},
		{"allow without kind", "host:\n  firewall: {allow: [22/tcp]}\n", "no kind"},
		{"rule with a space", "host:\n  firewall: {kind: ufw, allow: [\"22 tcp\"]}\n", "not one rule"},
		{"sysctl key", "host:\n  sysctl: {swappiness: \"1\"}\n", "not a sysctl key"},
		{"sysctl value", "host:\n  sysctl: {vm.swappiness: \"1 2\"}\n", "one word"},
		{"user", "host:\n  user: {name: Runner}\n", "not an account name"},
		{"tool", "host:\n  requires: [\"rm -rf\"]\n", "not a command name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseProfile([]byte(c.yml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error naming %q", err, c.want)
			}
		})
	}
}

// A host answering the commands observe asks, from a table; a command it does not
// know is not installed.
func fakeHost(answers map[string]string, fails map[string]bool) commandRunner {
	return func(name string, args ...string) (string, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		if fails[line] {
			return "", fmt.Errorf("%s: exit status 1", line)
		}
		if out, ok := answers[line]; ok {
			return out, nil
		}
		return "", fmt.Errorf("%s: %w", name, exec.ErrNotFound)
	}
}

const ufwActive = `Status: active

To                         Action      From
--                         ------      ----
OpenSSH                    ALLOW       Anywhere
80/tcp                     ALLOW       Anywhere
OpenSSH (v6)               ALLOW       Anywhere (v6)
80/tcp (v6)                ALLOW       Anywhere (v6)
8080/tcp                   DENY        Anywhere
`

// JCT40: what observe prints, survey reads back, one divergence per fact with a
// stable code: a missing group, a closed port, a sysctl that differs (with its
// reason), a tool not installed, and nothing for what holds.
func TestHostSetupObservedAndSurveyed(t *testing.T) {
	p, err := ParseProfile([]byte(setupProfile))
	if err != nil {
		t.Fatal(err)
	}
	run := fakeHost(map[string]string{
		"id -nG runner": "runner sudo\n",
		"ufw status":    ufwActive,
		"sysctl -n net.ipv6.conf.all.disable_ipv6": "0\n",
		"sysctl -n vm.swappiness":                  "10\n",
		`sh -c command -v "$1" sh docker`:          "/usr/bin/docker\n",
		"docker compose version":                   "Docker Compose version v2.30.0\n",
	}, map[string]bool{`sh -c command -v "$1" sh rsync`: true})

	lines := observeHost("/srv", p.Host, run)
	for _, w := range []string{"user runner runner,sudo", "allow ufw OpenSSH", "allow ufw 80/tcp", "sysctl net.ipv6.conf.all.disable_ipv6 0", "tool docker present", "tool compose present", "tool rsync absent"} {
		if !contains(lines, w) {
			t.Fatalf("no %q in:\n%s", w, strings.Join(lines, "\n"))
		}
	}
	if contains(lines, "allow ufw 8080/tcp") {
		t.Fatal("a DENY rule was read as allowed")
	}

	o := ParseObserved(strings.Join(lines, "\n"))
	var got []string
	for _, d := range surveySetup(p.Host, o) {
		got = append(got, d.Code+" "+d.Service)
	}
	want := []string{"user.group runner", "firewall.closed 443/udp", "sysctl.value net.ipv6.conf.all.disable_ipv6", "tool.absent rsync"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("divergences:\n got %v\nwant %v", got, want)
	}
	for _, d := range surveySetup(p.Host, o) {
		if d.Code == "sysctl.value" && !strings.Contains(d.Says, "AAAA record") {
			t.Fatalf("the reason did not travel: %s", d.Says)
		}
	}
}

// JCT40: a fact that could not be read is a divergence, never a pass; and allow
// rules are not held while the firewall is off, which firewall.off already says.
func TestHostSetupUnreadIsNotAgreement(t *testing.T) {
	p, err := ParseProfile([]byte(setupProfile))
	if err != nil {
		t.Fatal(err)
	}
	o := ParseObserved("firewall ufw inactive\nuser runner absent\n")
	var got []string
	for _, d := range surveySetup(p.Host, o) {
		got = append(got, d.Code+" "+d.Service)
	}
	want := []string{"user.absent runner", "sysctl.unread net.ipv6.conf.all.disable_ipv6", "sysctl.unread vm.swappiness",
		"tool.unread docker", "tool.unread compose", "tool.unread rsync"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("divergences:\n got %v\nwant %v", got, want)
	}

	// id not installed reads unknown, not absent.
	lines := observeSetup(Host{User: &HostUser{Name: "runner"}}, "", fakeHost(nil, nil))
	if !reflect.DeepEqual(lines, []string{"user runner unknown"}) {
		t.Fatalf("lines: %v", lines)
	}
}
