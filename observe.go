// `jcloud observe` — the collector `survey` reads, run on the host itself (JCT22).
//
// Until JCT22 the observations came from the Japode platform's
// scripts/survey-server.sh, a bash collector that knew Japode's container prefix,
// its pin file and its timers. jcloud shipped none, so a second platform adopting
// `survey` had to write its own, and every fix to one never reached the other.
//
// This prints the `<kind> <name> <value>` lines ParseObserved reads, for the facts
// `survey` compares:
//
//	container <service> <health or state>   every container docker has up
//	running   <service> <repo digest>       the image that container is on
//	pinned    <service> <reference>         what the deploy's pin file holds
//	profile   <profile> on|off              each profile a manifest declares
//
// The names come from jcloud.yml and not from Japode: the container prefix that is
// stripped, the pin file and the image variable read from it, where the manifests
// sit. The host facts (timers, path modes, a firewall) are read only as jcloud.yml's
// host: names them (JCT23, host.go), so a platform is never held to Japode's list.
//
// **It reads the machine it runs on and reaches no network**, which is JC29's
// division kept: the transport (SSH, a self-hosted runner, a local timer) is the
// platform's, because that is where the credential is. It runs `docker` from PATH
// with read-only verbs and writes nothing. A docker that cannot be asked is an
// error and never an empty answer: an empty file is a collector that gathered
// nothing, and `survey` must not read that as a server running nothing.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// commandRunner runs one read-only command and answers its stdout, kept even when
// the command exits non-zero: `systemctl is-active` does that for an inactive unit
// and still prints the state. A variable so the tests can hand back the host's
// answers without a daemon. A command not on PATH wraps exec.ErrNotFound.
type commandRunner func(name string, args ...string) (string, error)

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return string(out), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Observe reads the host for the tree at root and answers the observation lines,
// sorted so two runs over an unchanged host print the same bytes.
func Observe(root string, run commandRunner) ([]string, error) {
	prof, err := LoadProfile(root)
	if err != nil {
		return nil, err
	}
	d, err := DeclaredIn(root)
	if err != nil {
		return nil, err
	}

	var lines []string
	up, err := observeContainers(prof, run)
	if err != nil {
		return nil, err
	}
	for _, c := range up {
		lines = append(lines, fmt.Sprintf("container %s %s", c.name, c.status))
		if c.digest != "" {
			lines = append(lines, fmt.Sprintf("running %s %s", c.name, c.digest))
		}
	}

	// Absent is a host nothing has been deployed to yet, which pins nothing.
	pins, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(prof.PinFile)))
	for _, service := range d.Services {
		if ref := pinnedDigest(prof, &Manifest{Name: service}, string(pins)); ref != "" {
			lines = append(lines, fmt.Sprintf("pinned %s %s", service, ref))
		}
	}

	for p, on := range profilesOn(root, d, up) {
		state := "off"
		if on {
			state = "on"
		}
		lines = append(lines, fmt.Sprintf("profile %s %s", p, state))
	}

	lines = append(lines, observeHost(root, prof.Host, run)...)

	sort.Strings(lines)
	return lines, nil
}

type observedContainer struct{ name, status, digest string }

// observeContainers asks docker three questions in three calls, not three per
// container: what is up, how each one stands and which image it is on, and which
// repo digest each image carries. RepoDigests is the form the deploy pins, so it is
// read off the image and not off the container listing.
func observeContainers(prof *Profile, run commandRunner) ([]observedContainer, error) {
	ps, err := run("docker", "ps", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	names := strings.Fields(ps)
	if len(names) == 0 {
		return nil, nil
	}

	inspect, err := run("docker", append([]string{"inspect", "--format",
		"{{.Name}} {{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}} {{.Image}}"}, names...)...)
	if err != nil {
		return nil, err
	}
	var out []observedContainer
	var images []string
	imageOf := map[int]string{}
	for _, line := range strings.Split(inspect, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(f[0], "/"), prof.ContainerPrefix)
		out = append(out, observedContainer{name: name, status: f[1]})
		if len(f) > 2 {
			imageOf[len(out)-1] = f[2]
			if !contains(images, f[2]) {
				images = append(images, f[2])
			}
		}
	}
	if len(images) == 0 {
		return out, nil
	}

	// A local build carries no repo digest, and then no `running` line is printed:
	// survey compares a digest only when both sides were observed.
	digests, err := run("docker", append([]string{"image", "inspect", "--format",
		"{{.Id}} {{range .RepoDigests}}{{.}} {{end}}"}, images...)...)
	if err != nil {
		return nil, err
	}
	digestOf := map[string]string{}
	for _, line := range strings.Split(digests, "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			digestOf[f[0]] = f[1]
		}
	}
	for i := range out {
		out[i].digest = digestOf[imageOf[i]]
	}
	return out, nil
}

var composeProfilesLine = regexp.MustCompile(`(?m)^COMPOSE_PROFILES=(.*)$`)

// lookupEnv is os.LookupEnv, swapped by the tests so the environment they run in
// cannot switch a profile on.
var lookupEnv = os.LookupEnv

// profilesOn answers each profile a manifest declares. It is on when compose would
// start it, which is COMPOSE_PROFILES (the environment first, then the tree's
// .env, compose's own order) naming it or `*`, or when a service declared under it is
// up, which is somebody having started it by hand. Off otherwise, and survey then
// reads its services' absence as the profile being off rather than as drift.
func profilesOn(root string, d Declared, up []observedContainer) map[string]bool {
	listed, set := lookupEnv("COMPOSE_PROFILES")
	if !set {
		env, _ := os.ReadFile(filepath.Join(root, ".env"))
		if m := composeProfilesLine.FindSubmatch(env); m != nil {
			listed = strings.Trim(strings.TrimSpace(string(m[1])), `"'`)
		}
	}
	named := map[string]bool{}
	for _, p := range strings.Split(listed, ",") {
		named[strings.TrimSpace(p)] = true
	}
	running := map[string]bool{}
	for _, c := range up {
		running[c.name] = true
	}

	on := map[string]bool{}
	for service, p := range d.Profile {
		on[p] = on[p] || named[p] || named["*"] || running[service]
	}
	return on
}

// observe is the command.
func observe(args []string) int {
	root := "."
	for len(args) > 0 {
		switch {
		case args[0] == "--root" && len(args) >= 2:
			root, args = args[1], args[2:]
		default:
			fmt.Fprintln(os.Stderr, "jcloud observe [--root <dir>]\n\nRun on the host, in the deployed tree; prints what `jcloud survey` reads.")
			return 2
		}
	}
	lines, err := Observe(root, runCommand)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud observe: %v\n", err)
		return 2
	}
	// The first line says which build read the host, and also keeps an empty server
	// from printing an empty file, which `survey` refuses as a collector that
	// gathered nothing.
	fmt.Printf("# jcloud observe %s\n", Version())
	for _, line := range lines {
		fmt.Println(line)
	}
	return 0
}
