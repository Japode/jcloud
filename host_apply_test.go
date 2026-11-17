package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMachine is a host that changes when it is told to, so a second apply can be
// held to changing nothing.
type fakeMachine struct {
	uid     string
	tools   map[string]bool
	users   map[string][]string
	stats   map[string]string // path -> "mode user:group"
	added   []string          // ufw rules
	active  bool
	sysctls map[string]string
	ran     []string
}

func newFakeMachine() *fakeMachine {
	return &fakeMachine{uid: "0", tools: map[string]bool{"sh": true}, users: map[string][]string{},
		stats: map[string]string{}, sysctls: map[string]string{}}
}

func (m *fakeMachine) run(name string, args ...string) (string, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	m.ran = append(m.ran, line)
	fail := fmt.Errorf("%s: exit status 1", line)
	switch {
	case line == "id -u":
		return m.uid + "\n", nil
	case name == "sh":
		if m.tools[args[len(args)-1]] {
			return "/usr/bin/" + args[len(args)-1], nil
		}
		return "", fail
	case line == "docker compose version":
		if m.tools["compose"] {
			return "v2", nil
		}
		return "", fail
	case name == "apt-get" && args[0] == "install":
		for _, p := range args[3:] {
			switch p {
			case "docker-ce":
				m.tools["docker"] = true
			case "docker-compose-plugin":
				m.tools["compose"] = true
			default:
				m.tools[p] = true
			}
		}
	case name == "id" && args[0] == "-nG":
		groups, ok := m.users[args[1]]
		if !ok {
			return "", fail
		}
		return strings.Join(append([]string{args[1]}, groups...), " "), nil
	case name == "useradd":
		m.users[args[len(args)-1]] = nil
	case name == "usermod":
		m.users[args[2]] = append(m.users[args[2]], args[1])
	case name == "stat":
		if s, ok := m.stats[args[2]]; ok {
			return s, nil
		}
		return "", fail
	case name == "mkdir":
		if _, ok := m.stats[args[1]]; !ok {
			m.stats[args[1]] = "755 root:root"
		}
	case name == "touch":
		m.stats[args[0]] = "644 root:root"
	case name == "chown":
		f := strings.Fields(m.stats[args[1]])
		m.stats[args[1]] = f[0] + " " + args[0]
	case name == "chmod" && args[0] != "a+r":
		f := strings.Fields(m.stats[args[1]])
		m.stats[args[1]] = args[0] + " " + f[1]
	case line == "ufw show added":
		var b strings.Builder
		for _, r := range m.added {
			b.WriteString("ufw allow " + r + "\n")
		}
		return b.String(), nil
	case name == "ufw" && args[0] == "allow":
		m.added = append(m.added, args[1])
	case line == "ufw --force enable":
		m.active = true
	case line == "ufw status":
		if !m.tools["ufw"] {
			return "", exec.ErrNotFound
		}
		if !m.active {
			return "Status: inactive\n", nil
		}
		var b strings.Builder
		b.WriteString("Status: active\n\nTo Action From\n-- ------ ----\n")
		for _, r := range m.added {
			b.WriteString(r + " ALLOW Anywhere\n")
		}
		return b.String(), nil
	case line == "sysctl --system":
		data, _ := os.ReadFile(filepath.Join(sysRoot, filepath.FromSlash(SysctlFile)))
		for _, l := range strings.Split(string(data), "\n") {
			if k, v, ok := strings.Cut(l, " = "); ok {
				m.sysctls[k] = v
			}
		}
	case name == "sysctl" && args[0] == "-n":
		if v, ok := m.sysctls[args[1]]; ok {
			return v, nil
		}
		return "", fail
	case line == "dpkg --print-architecture":
		return "amd64\n", nil
	}
	return "", nil
}

const applyHost = `host:
  user: {name: runner, groups: [docker]}
  paths:
    - {path: ., mode: "750", owner: "runner:runner"}
    - {path: .deploy-state/images.env, mode: "600", owner: "runner:runner"}
  firewall: {kind: ufw, allow: [OpenSSH, 80/tcp, 443/udp]}
  sysctl:
    net.ipv6.conf.all.disable_ipv6: {value: "1", why: no name here has an AAAA record}
  requires: [docker, compose, rsync]
`

func withSysRoot(t *testing.T, osRelease string) string {
	t.Helper()
	dir := layTree(t, map[string]string{"etc/os-release": osRelease})
	was := sysRoot
	sysRoot = dir
	t.Cleanup(func() { sysRoot = was })
	return dir
}

// JCT38: an empty Debian host is built from host: — docker from Docker's own
// repository, rsync, the user in docker, the paths with their modes (the 600 pin
// file as a file), the sysctl file with its reason, and ufw with SSH allowed before
// it is enabled — and a second apply changes nothing.
func TestHostApplyBuildsAnEmptyHostOnce(t *testing.T) {
	sys := withSysRoot(t, "ID=debian\nVERSION_CODENAME=bookworm\n")
	p, err := ParseProfile([]byte(applyHost))
	if err != nil {
		t.Fatal(err)
	}
	m := newFakeMachine()
	did, still, err := HostApply("/srv/cloud", p.Host, m.run)
	if err != nil || len(still) > 0 {
		t.Fatalf("err %v, still %v\nran:\n%s", err, still, strings.Join(m.ran, "\n"))
	}
	joined := strings.Join(did, "\n")
	for _, w := range []string{
		"curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc",
		"wrote /etc/apt/sources.list.d/docker.list",
		"apt-get install -y --no-install-recommends docker-ce",
		"apt-get install -y --no-install-recommends rsync",
		"useradd --create-home --shell /bin/bash runner",
		"usermod -aG docker runner",
		"touch /srv/cloud/.deploy-state/images.env",
		"chmod 600 /srv/cloud/.deploy-state/images.env",
		"wrote /" + SysctlFile,
		"sysctl --system",
	} {
		if !strings.Contains(joined, w) {
			t.Fatalf("no %q in:\n%s", w, joined)
		}
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(sys, "etc", "apt", "sources.list.d", "docker.list"))), "arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable") {
		t.Fatal("the docker sources line")
	}
	conf := string(mustRead(t, filepath.Join(sys, filepath.FromSlash(SysctlFile))))
	if !strings.Contains(conf, "# no name here has an AAAA record\nnet.ipv6.conf.all.disable_ipv6 = 1\n") || !strings.Contains(conf, "jcloud") {
		t.Fatalf("sysctl file:\n%s", conf)
	}
	var allowAt, enableAt int
	for i, l := range did {
		if l == "ufw allow OpenSSH" {
			allowAt = i
		}
		if l == "ufw --force enable" {
			enableAt = i
		}
	}
	if enableAt == 0 || allowAt > enableAt {
		t.Fatalf("SSH was not allowed before ufw was enabled:\n%s", joined)
	}

	did, still, err = HostApply("/srv/cloud", p.Host, m.run)
	if err != nil || len(still) > 0 || len(did) > 0 {
		t.Fatalf("a second apply changed %v (err %v, still %v)", did, err, still)
	}
}

// JCT38: ufw is never enabled without an SSH rule among its rules.
func TestHostApplyNeverLocksSSHOut(t *testing.T) {
	withSysRoot(t, "ID=ubuntu\nVERSION_CODENAME=noble\n")
	m := newFakeMachine()
	m.tools["ufw"] = true
	_, _, err := HostApply("/srv", Host{Firewall: "ufw", FirewallAllow: []string{"80/tcp"}}, m.run)
	if err == nil || !strings.Contains(err.Error(), "add OpenSSH to host.firewall.allow") {
		t.Fatalf("got %v", err)
	}
	for _, l := range m.ran {
		if l == "ufw --force enable" {
			t.Fatal("enabled ufw with no SSH rule")
		}
	}
}

// JCT38: another distribution is refused by name, and so is a caller not root.
func TestHostApplyRefusesWhatItCannotDo(t *testing.T) {
	withSysRoot(t, "ID=fedora\nVERSION_CODENAME=\n")
	m := newFakeMachine()
	if _, _, err := HostApply("/srv", Host{Requires: []string{"docker"}}, m.run); err == nil || !strings.Contains(err.Error(), "this host is fedora") {
		t.Fatalf("got %v", err)
	}
	m.uid = "1000"
	if _, _, err := HostApply("/srv", Host{Requires: []string{"docker"}}, m.run); err == nil || !strings.Contains(err.Error(), "as root") {
		t.Fatalf("got %v", err)
	}
}

// JCT38: install prepares the host first when the archive's jcloud.yml declares
// host:, and a host that cannot be prepared stops it before anything is written.
func TestInstallPreparesTheHostFirst(t *testing.T) {
	root := exportTree(t, map[string]string{"jcloud.yml": "host:\n  requires: [docker]\n"})
	id := testRecipient(t)
	archive := filepath.Join(t.TempDir(), "paste.tar.gz")
	if code := export([]string{"paste", "--root", root, "--recipient", id.Recipient().String(), "-o", archive}); code != 0 {
		t.Fatalf("export: exit %d", code)
	}
	m := newFakeMachine()
	m.uid = "1000"
	was := hostRun
	hostRun = m.run
	t.Cleanup(func() { hostRun = was })

	target := t.TempDir()
	d := newFakeInstall()
	if code := runInstall(archive, target, "", 60, registryLogin{}, d.run); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(d.calls) != 0 {
		t.Fatalf("docker was asked before the host was ready: %v", d.calls)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Fatalf("wrote %v", entries)
	}
}
