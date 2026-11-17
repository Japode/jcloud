// `jcloud host apply` — make the machine what jcloud.yml's host: declares (JCT38).
//
// Decided by the owner: jcloud prepares an empty server, and a platform's bootstrap
// script (Japode/cloud's scripts/bootstrap-server.sh) is replaced by it rather than
// kept beside it. The duplication is avoided by the declaration: the script's steps
// are host: entries (JCT40), so what survey checks and what the host is built from
// are one statement.
//
// Run as root, in the deployed tree. Every step is a no-op when it already holds,
// so running it twice changes nothing the second time:
//
//  1. Docker engine and the compose plugin, from Docker's own apt repository, when
//     requires: names docker or compose and either is missing; each other tool
//     requires: names, by apt package of the same name. Debian and Ubuntu only:
//     another distribution is refused by name, never guessed at.
//  2. The user, created when absent, and added to each group.
//  3. Each path: created when absent (a directory, or an empty file when its mode
//     gives the owner no x bit, as a 600 pin file), then its mode and owner.
//  4. The sysctl entries, in one file, /etc/sysctl.d/90-jcloud.conf, naming jcloud
//     and carrying each entry's why: above it; `sysctl --system` when it changed.
//  5. The firewall: ufw installed, each allow rule added, then enabled. An SSH rule
//     must be among the rules before ufw is enabled, never after: enabling it over
//     SSH without one locks the machine away from the person running this, which is
//     why the bootstrap script only ever advised. Missing, the enable is refused.
//
// Then it runs the observation survey would and prints whatever still differs, and
// exits 1 if anything does.
//
// `install` runs this first when the archive's jcloud.yml declares any of it, so an
// empty server needs `jcloud install <archive>` and nothing else (JCT36).
//
// Outside: the GitHub runner registration, which is the platform's way of
// deploying and not part of a move. This is a writing verb invoked by a person and
// never by the watch, so the non-goal on unattended repair holds.
package main

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sysRoot is where /etc is, swapped by the tests.
var sysRoot = "/"

// hostRun is the runner host apply changes the machine through, swapped by the tests.
var hostRun commandRunner = runCommand

// SysctlFile is the one file host apply writes kernel settings into.
const SysctlFile = "etc/sysctl.d/90-jcloud.conf"

// dockerPackages are what Docker's own instructions install from its repository.
var dockerPackages = []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"}

// sshRules are the ufw rules that let SSH in.
var sshRules = []string{"OpenSSH", "22", "22/tcp", "ssh"}

// declaresSetup is whether h asks anything of the machine host apply can make.
func (h Host) declaresSetup() bool {
	return h.User != nil || len(h.Paths) > 0 || h.Firewall != "" || len(h.Sysctl) > 0 || len(h.Requires) > 0
}

// hostApplier carries one run: what it did, and the first thing that stopped it.
type hostApplier struct {
	root string
	run  commandRunner
	did  []string
}

func (a *hostApplier) do(name string, args ...string) error {
	if _, err := a.run(name, args...); err != nil {
		return err
	}
	a.did = append(a.did, strings.Join(append([]string{name}, args...), " "))
	return nil
}

func (a *hostApplier) has(tool string) bool {
	if tool == "compose" {
		_, err := a.run("docker", "compose", "version")
		return err == nil
	}
	_, err := a.run("sh", "-c", `command -v "$1"`, "sh", tool)
	return err == nil
}

// osRelease is ID and VERSION_CODENAME from /etc/os-release.
func osRelease() (id, codename string, err error) {
	data, err := os.ReadFile(filepath.Join(sysRoot, "etc", "os-release"))
	if err != nil {
		return "", "", err
	}
	for k, v := range parseDotEnv(string(data)) {
		switch k {
		case "ID":
			id = v
		case "VERSION_CODENAME":
			codename = v
		}
	}
	return id, codename, nil
}

// apt is the package manager, and only on a distribution this build knows.
func (a *hostApplier) apt(packages ...string) error {
	id, _, err := osRelease()
	if err != nil {
		return fmt.Errorf("/etc/os-release: %v — this build installs packages on debian and ubuntu only", unwrapPath(err))
	}
	if id != "debian" && id != "ubuntu" {
		return fmt.Errorf("this host is %s, and this build installs packages on debian and ubuntu only; install %s by hand", id, strings.Join(packages, ", "))
	}
	return a.do("apt-get", append([]string{"install", "-y", "--no-install-recommends"}, packages...)...)
}

// tools is step 1.
func (a *hostApplier) tools(h Host) error {
	wantDocker := contains(h.Requires, "docker") || contains(h.Requires, "compose")
	if wantDocker && (!a.has("docker") || !a.has("compose")) {
		if err := a.dockerRepository(); err != nil {
			return fmt.Errorf("docker: %w", err)
		}
		if err := a.apt(dockerPackages...); err != nil {
			return fmt.Errorf("docker: %w", err)
		}
	}
	var missing []string
	for _, t := range h.Requires {
		if t != "docker" && t != "compose" && !a.has(t) {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		if err := a.do("apt-get", "update"); err != nil {
			return err
		}
		return a.apt(missing...)
	}
	return nil
}

// dockerRepository adds Docker's apt repository the way its own instructions do:
// the key under /etc/apt/keyrings and one sources line signed by it.
func (a *hostApplier) dockerRepository() error {
	id, codename, err := osRelease()
	if err != nil {
		return fmt.Errorf("/etc/os-release: %v", unwrapPath(err))
	}
	if id != "debian" && id != "ubuntu" {
		return fmt.Errorf("this host is %s, and this build installs docker on debian and ubuntu only; see https://docs.docker.com/engine/install/", id)
	}
	if codename == "" {
		return errors.New("/etc/os-release names no VERSION_CODENAME")
	}
	arch, err := a.run("dpkg", "--print-architecture")
	if err != nil {
		return err
	}
	steps := [][]string{
		{"apt-get", "update"},
		{"apt-get", "install", "-y", "--no-install-recommends", "ca-certificates", "curl"},
		{"install", "-m", "0755", "-d", "/etc/apt/keyrings"},
		{"curl", "-fsSL", "https://download.docker.com/linux/" + id + "/gpg", "-o", "/etc/apt/keyrings/docker.asc"},
		{"chmod", "a+r", "/etc/apt/keyrings/docker.asc"},
	}
	for _, s := range steps {
		if err := a.do(s[0], s[1:]...); err != nil {
			return err
		}
	}
	line := fmt.Sprintf("deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n",
		strings.TrimSpace(arch), id, codename)
	if err := a.write("etc/apt/sources.list.d/docker.list", line); err != nil {
		return err
	}
	return a.do("apt-get", "update")
}

// write puts content at a path under sysRoot, and says it did only when it changed.
func (a *hostApplier) write(rel, content string) error {
	dest := filepath.Join(sysRoot, filepath.FromSlash(rel))
	if old, err := os.ReadFile(dest); err == nil && string(old) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		return err
	}
	a.did = append(a.did, "wrote /"+rel)
	return nil
}

// user is step 2.
func (a *hostApplier) user(u *HostUser) error {
	if u == nil {
		return nil
	}
	out, err := a.run("id", "-nG", u.Name)
	if err != nil {
		if err := a.do("useradd", "--create-home", "--shell", "/bin/bash", u.Name); err != nil {
			return err
		}
		out, _ = a.run("id", "-nG", u.Name)
	}
	in := strings.Fields(out)
	for _, g := range u.Groups {
		if !contains(in, g) {
			if err := a.do("usermod", "-aG", g, u.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// paths is step 3.
func (a *hostApplier) paths(paths []HostPath) error {
	for _, p := range paths {
		at := p.Path
		if !path.IsAbs(at) {
			at = path.Join(filepath.ToSlash(a.root), at)
		}
		out, err := a.run("stat", "-c", "%a %U:%G", at)
		f := strings.Fields(out)
		if err != nil || len(f) != 2 {
			if err := a.create(at, p.Mode); err != nil {
				return err
			}
			f = []string{"", ""}
		}
		if p.Owner != "" && f[1] != p.Owner {
			if err := a.do("chown", p.Owner, at); err != nil {
				return err
			}
		}
		if p.Mode != "" && strings.TrimLeft(f[0], "0") != strings.TrimLeft(p.Mode, "0") {
			if err := a.do("chmod", p.Mode, at); err != nil {
				return err
			}
		}
	}
	return nil
}

// create makes a missing path: a directory, unless its mode gives the owner no x
// bit, which no directory anybody enters has.
func (a *hostApplier) create(at, mode string) error {
	if mode != "" {
		if n, err := strconv.ParseUint(mode, 8, 32); err == nil && n&0o100 == 0 {
			if err := a.do("mkdir", "-p", path.Dir(at)); err != nil {
				return err
			}
			return a.do("touch", at)
		}
	}
	return a.do("mkdir", "-p", at)
}

// sysctlConf is the file step 4 writes.
func sysctlConf(entries []HostSysctl) string {
	var b strings.Builder
	b.WriteString("# Written by jcloud host apply from jcloud.yml's host.sysctl (JCT38).\n")
	b.WriteString("# Change the declaration there, not this file: the next apply rewrites it.\n")
	for _, s := range entries {
		b.WriteString("\n")
		if s.Why != "" {
			fmt.Fprintf(&b, "# %s\n", s.Why)
		}
		fmt.Fprintf(&b, "%s = %s\n", s.Key, s.Value)
	}
	return b.String()
}

// sysctl is step 4.
func (a *hostApplier) sysctl(entries []HostSysctl) error {
	if len(entries) == 0 {
		return nil
	}
	before := len(a.did)
	if err := a.write(SysctlFile, sysctlConf(entries)); err != nil {
		return err
	}
	if len(a.did) == before {
		return nil
	}
	return a.do("sysctl", "--system")
}

// firewall is step 5. The rules first and the enable last, and no enable without
// an SSH rule among them.
func (a *hostApplier) firewall(h Host) error {
	if h.Firewall != "ufw" {
		return nil
	}
	if !a.has("ufw") {
		if err := a.apt("ufw"); err != nil {
			return err
		}
	}
	added, _ := a.run("ufw", "show", "added")
	have := map[string]bool{}
	for _, line := range strings.Split(added, "\n") {
		if rule, ok := strings.CutPrefix(strings.TrimSpace(line), "ufw allow "); ok && len(strings.Fields(rule)) > 0 {
			have[strings.Fields(rule)[0]] = true
		}
	}
	for _, r := range h.FirewallAllow {
		if !have[r] {
			if err := a.do("ufw", "allow", r); err != nil {
				return err
			}
			have[r] = true
		}
	}
	status, _ := a.run("ufw", "status")
	if strings.Contains(status, "Status: active") {
		return nil
	}
	ssh := false
	for _, r := range sshRules {
		ssh = ssh || have[r]
	}
	if !ssh {
		return fmt.Errorf("ufw is inactive and no SSH rule is among its rules, so enabling it would lock this machine away from whoever is on it over SSH — add OpenSSH to host.firewall.allow")
	}
	return a.do("ufw", "--force", "enable")
}

// HostApply makes the machine match h, step by step, and answers what it did and
// what still differs afterwards.
func HostApply(root string, h Host, run commandRunner) (did []string, still []Divergence, err error) {
	if out, err := run("id", "-u"); err != nil || strings.TrimSpace(out) != "0" {
		return nil, nil, errors.New("host apply changes the machine and has to run as root (sudo jcloud host apply)")
	}
	a := &hostApplier{root: root, run: run}
	steps := []struct {
		name string
		fn   func() error
	}{
		{"tools", func() error { return a.tools(h) }},
		{"user", func() error { return a.user(h.User) }},
		{"paths", func() error { return a.paths(h.Paths) }},
		{"sysctl", func() error { return a.sysctl(h.Sysctl) }},
		{"firewall", func() error { return a.firewall(h) }},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return a.did, nil, fmt.Errorf("%s: %w", s.name, err)
		}
	}
	// What this made, asked back the way survey asks it; the timers, disks and
	// certificates are survey's alone and nothing here makes them.
	made := Host{Paths: h.Paths, Firewall: h.Firewall, User: h.User, FirewallAllow: h.FirewallAllow, Sysctl: h.Sysctl, Requires: h.Requires}
	o := ParseObserved(strings.Join(observeHost(root, made, run), "\n"))
	still = surveyHost(made, o)
	sort.SliceStable(still, func(i, j int) bool { return still[i].Key() < still[j].Key() })
	return a.did, still, nil
}

// applyHostReport runs HostApply and prints it; 0 when the machine now matches.
func applyHostReport(root string, h Host, run commandRunner) int {
	did, still, err := HostApply(root, h, run)
	for _, d := range did {
		fmt.Printf("      %s\n", d)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m host apply: %v\n", err)
		return 1
	}
	if len(still) > 0 {
		fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m host apply ran, and the host still differs from jcloud.yml:\n")
		for _, d := range still {
			fmt.Fprintf(os.Stderr, "      %s %s: %s\n", d.Code, d.Service, d.Says)
		}
		return 1
	}
	if len(did) == 0 {
		fmt.Printf("  \033[32m✓\033[0m the host already matches jcloud.yml's host:; nothing changed\n")
	} else {
		fmt.Printf("  \033[32m✓\033[0m the host now matches jcloud.yml's host: (%d change(s))\n", len(did))
	}
	return 0
}

// hostCmd is `jcloud host <verb>`.
func hostCmd(args []string) int {
	usage := "jcloud host apply [--root <dir>]"
	if len(args) == 0 || args[0] != "apply" {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	root := "."
	args = args[1:]
	for len(args) > 0 {
		if args[0] == "--root" && len(args) >= 2 {
			root, args = args[1], args[2:]
			continue
		}
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	prof, err := LoadProfile(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud host apply: %v\n", err)
		return 2
	}
	if !prof.Host.declaresSetup() {
		fmt.Printf("  jcloud.yml declares nothing host apply can make; nothing changed\n")
		return 0
	}
	return applyHostReport(root, prof.Host, hostRun)
}
