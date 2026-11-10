// The host, declared in jcloud.yml (JCT23).
//
// `survey` held a server only to what the manifests declare, and a manifest declares
// services. The timers, directory modes and firewall that JC2 found broken on Japode
// (three timers never installed, so 26 days with no copy of anything) were asserted
// in the platform's own bash, against a list written there by hand, and a second
// platform had no way to say it has timers at all.
//
// So jcloud.yml may carry a `host:` key:
//
//	host:
//	  timers:                                        # each <unit>.timer must be active
//	    - japode-healthwatch                         # and its last run have succeeded
//	    - {name: japode-backup, max_age: 7h}         # and finished within 7h (JCT24)
//	  paths:
//	    - path: .                                    # relative to the root, or absolute
//	      mode: "750"
//	      owner: runner:runner                       # optional
//	  firewall: ufw                                  # must be installed and active
//
// `observe` reads exactly what it names, and `survey` reports one divergence per fact.
// Absent, nothing about the host is asserted: a platform opts in by declaring and
// never inherits Japode's list.
//
// A fact that could not be read (ufw status without root, a stat that failed) is a
// divergence and not a pass: a declared fact nobody measured is not agreement. The
// remedy is a collector with more access, never a change to the machine.
package main

import (
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Host is what jcloud.yml says the machine must be. The zero value asserts nothing.
type Host struct {
	Timers   []HostTimer
	Paths    []HostPath
	Firewall string // "" for none asserted, or the firewall that must be active

	// Capacity (JCT25, host_capacity.go): filesystems and the free share each must
	// keep, certificate files and the days each must stay valid.
	Disks    []string
	DiskFree string
	Certs    []string
	CertDays int
}

// HostTimer is one timer that must be active (JCT23), and whose unit's last run
// must have succeeded (JCT24). An active timer is not a working job: the schedule
// can exist while the unit it fires fails every night, which is the 26 days with
// no copy that JC2 found. With max_age, the last run must also have finished
// within it, so a job that stopped being fired at all is named too.
//
// Written as a bare name, or as {name, max_age} where max_age is a Go duration:
//
//	timers: [japode-healthwatch, {name: japode-backup, max_age: 7h}]
//
// It reads timestamps and nothing else. It never opens what the job wrote, so it
// says a backup is recent, not that it restores; that stays the restore test's job.
type HostTimer struct {
	Name   string
	MaxAge string // a Go duration as written, "" for none; validate refuses a bad one
}

func (t *HostTimer) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		t.Name = n.Value
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: a timer is a name or {name, max_age}", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		switch key, value := n.Content[i].Value, n.Content[i+1].Value; key {
		case "name":
			t.Name = value
		case "max_age":
			t.MaxAge = value
		default:
			// Refused for KnownFields' reason, which does not reach a node decoded
			// by hand: a misspelt max_age would leave the age unasserted.
			return fmt.Errorf("line %d: field %s not found in a timer", n.Content[i].Line, key)
		}
	}
	return nil
}

type HostPath struct {
	Path  string `yaml:"path"`
	Mode  string `yaml:"mode"`
	Owner string `yaml:"owner"`
}

type hostFile struct {
	Timers   []HostTimer `yaml:"timers"`
	Paths    []HostPath  `yaml:"paths"`
	Firewall string      `yaml:"firewall"`
	Disks    []string    `yaml:"disks"`
	DiskFree string      `yaml:"disk_free"`
	Certs    []string    `yaml:"certs"`
	CertDays int         `yaml:"cert_days"`
}

func (f hostFile) host() Host {
	return Host{Timers: f.Timers, Paths: f.Paths, Firewall: f.Firewall,
		Disks: f.Disks, DiskFree: f.DiskFree, Certs: f.Certs, CertDays: f.CertDays}
}

// firewalls is every firewall this build can read.
var firewalls = []string{"ufw"}

var (
	modeWord  = regexp.MustCompile(`^[0-7]{3,4}$`)
	ownerWord = regexp.MustCompile(`^[^\s:]+:[^\s:]+$`)
	unitWord  = regexp.MustCompile(`^[A-Za-z0-9@_.:-]+$`)
)

// validate holds every name to one word, because each one travels in a
// `<kind> <name> <value>` line that strings.Fields splits.
func (h Host) validate() []string {
	var out []string
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	for _, t := range h.Timers {
		switch {
		case !unitWord.MatchString(t.Name):
			bad("host.timers: %q is not a systemd unit name", t.Name)
		case strings.HasSuffix(t.Name, ".timer"):
			bad("host.timers: %q names the unit without .timer, which is added", t.Name)
		}
		if d, err := time.ParseDuration(t.MaxAge); t.MaxAge != "" && (err != nil || d <= 0) {
			bad("host.timers: %s has max_age %q, and an age is a positive duration such as 26h", t.Name, t.MaxAge)
		}
	}
	for _, p := range h.Paths {
		switch {
		case p.Path == "" || strings.ContainsAny(p.Path, " \t\n"):
			bad("host.paths: %q is empty or has a space in it", p.Path)
		case p.Mode == "" && p.Owner == "":
			bad("host.paths: %s declares neither mode nor owner, so nothing is asserted of it", p.Path)
		}
		if p.Mode != "" && !modeWord.MatchString(p.Mode) {
			bad("host.paths: %s has mode %q, and a mode is octal, such as \"750\"", p.Path, p.Mode)
		}
		if p.Owner != "" && !ownerWord.MatchString(p.Owner) {
			bad("host.paths: %s has owner %q, and an owner is user:group", p.Path, p.Owner)
		}
	}
	if h.Firewall != "" && !contains(firewalls, h.Firewall) {
		bad("host.firewall is %q, and this build reads %s", h.Firewall, strings.Join(firewalls, ", "))
	}
	h.validateCapacity(bad)
	return out
}

// observeHost reads what h names on the machine observe runs on, and nothing else.
//
//	timer    <timer>     <systemctl is-active>   unknown when systemd cannot be asked
//	lastrun  <timer>     <Result of its unit>    success, exit-code, … (JCT24)
//	finished <timer>     <unix seconds>          never, or unknown (JCT24)
//	clock    host        <unix seconds>          the host's now, which an age is from
//	disk     <path>      <percent free>          (JCT25, host_capacity.go)
//	cert     <file>      <unix NotAfter>         (JCT25, host_capacity.go)
//	path     <path>      <mode> <user:group>     unknown unknown when stat failed
//	firewall ufw         <status>                absent, or unreadable without root
func observeHost(root string, h Host, run commandRunner) []string {
	var lines []string
	if len(h.Timers) > 0 {
		lines = append(lines, observeTimers(h.Timers, run)...)
	}
	// Every age (a last run, a certificate) is counted from the host's own clock.
	if len(h.Timers) > 0 || len(h.Certs) > 0 {
		lines = append(lines, fmt.Sprintf("clock host %d", now().Unix()))
	}
	lines = append(lines, observeDisks(root, h.Disks, run)...)
	lines = append(lines, observeCerts(root, h.Certs)...)
	for _, p := range h.Paths {
		// Slashes, because stat runs on a Linux host whatever built this.
		at := p.Path
		if !path.IsAbs(at) {
			at = path.Join(filepath.ToSlash(root), at)
		}
		mode, owner := "unknown", "unknown"
		if out, err := run("stat", "-c", "%a %U:%G", at); err == nil {
			if f := strings.Fields(out); len(f) == 2 {
				mode, owner = f[0], f[1]
			}
		}
		lines = append(lines, fmt.Sprintf("path %s %s %s", p.Path, mode, owner))
	}
	if h.Firewall == "ufw" {
		out, err := run("ufw", "status")
		state := "unreadable"
		switch {
		case errors.Is(err, exec.ErrNotFound):
			state = "absent"
		default:
			for _, line := range strings.Split(out, "\n") {
				if s, ok := strings.CutPrefix(strings.TrimSpace(line), "Status: "); ok && s != "" {
					state = s
				}
			}
		}
		lines = append(lines, "firewall ufw "+state)
	}
	return lines
}

// now is the host's clock, swapped by the tests.
var now = time.Now

// observeTimers asks systemd three things in three calls: whether each timer is
// active, which unit each one fires (its Unit, usually <name>.service, read rather
// than assumed), and how that unit's last run ended and when. A count that does
// not match the units asked is an answer that cannot be lined up, and every
// value it would have given reads unknown.
func observeTimers(timers []HostTimer, run commandRunner) []string {
	var lines []string
	var units []string
	for _, t := range timers {
		units = append(units, t.Name+".timer")
	}
	// is-active exits non-zero when any unit is not active, and still prints one
	// state per unit, in order, which is the answer.
	out, _ := run("systemctl", append([]string{"is-active"}, units...)...)
	states := strings.Fields(out)
	out, _ = run("systemctl", append([]string{"show", "-p", "Unit", "--value"}, units...)...)
	fired := strings.Fields(out)

	results, finished := map[int]string{}, map[int]string{}
	if len(fired) == len(units) {
		out, _ = run("systemctl", append([]string{"show", "--timestamp=unix", "-p", "Result", "-p", "ExecMainExitTimestamp"}, fired...)...)
		if blocks := strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r", "")), "\n\n"); len(blocks) == len(units) {
			for i, block := range blocks {
				for _, line := range strings.Split(block, "\n") {
					key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
					switch key {
					case "Result":
						results[i] = value
					case "ExecMainExitTimestamp":
						switch {
						case value == "":
							finished[i] = "never"
						case strings.HasPrefix(value, "@"):
							finished[i] = value[1:]
						}
					}
				}
			}
		}
	}

	word := func(m map[int]string, i int) string {
		if v := m[i]; v != "" {
			return v
		}
		return "unknown"
	}
	for i, t := range timers {
		state := "unknown"
		if len(states) == len(units) {
			state = states[i]
		}
		lines = append(lines,
			fmt.Sprintf("timer %s %s", t.Name, state),
			fmt.Sprintf("lastrun %s %s", t.Name, word(results, i)),
			fmt.Sprintf("finished %s %s", t.Name, word(finished, i)))
	}
	return lines
}

// surveyTimer holds one timer to what jcloud.yml declares of it. An inactive timer
// is the end of what can be said: its last run is a second complaint about one
// fault, the way survey does not compare the digest of a container that is not up.
func surveyTimer(t HostTimer, o *Observed) []Divergence {
	unit := t.Name + ".timer"
	one := func(says string) []Divergence { return []Divergence{{unit, says}} }
	switch state, seen := o.Timers[t.Name]; {
	case !seen:
		return one("is declared in jcloud.yml host.timers and the observations do not report it")
	case state == "unknown":
		return one("could not be read — systemctl did not answer on the host")
	case state != "active":
		return one(fmt.Sprintf("is %s, not active, so the job it schedules never fires", state))
	}

	var out []Divergence
	switch result, seen := o.LastRun[t.Name]; {
	case !seen:
		out = append(out, Divergence{unit, "is active and the observations do not say how its last run ended"})
	case result == "unknown":
		out = append(out, Divergence{unit, "is active and how its last run ended could not be read"})
	case result != "success":
		out = append(out, Divergence{unit, fmt.Sprintf("is active and its last run ended in %s — the schedule exists and the job fails", result)})
	}

	if t.MaxAge == "" {
		return out
	}
	maxAge, _ := time.ParseDuration(t.MaxAge) // validate refused a bad one
	clock := now()
	if c, ok := o.Clock["host"]; ok {
		clock = time.Unix(c, 0)
	}
	switch at, seen := o.Finished[t.Name]; {
	case !seen || at == "unknown":
		out = append(out, Divergence{unit, fmt.Sprintf("has a max_age of %s and when its last run finished could not be read", t.MaxAge)})
	case at == "never":
		out = append(out, Divergence{unit, fmt.Sprintf("has never finished a run, and jcloud.yml accepts none older than %s", t.MaxAge)})
	default:
		var secs int64
		if _, err := fmt.Sscan(at, &secs); err != nil {
			out = append(out, Divergence{unit, fmt.Sprintf("finished at %q, which is not a time", at)})
		} else if age := clock.Sub(time.Unix(secs, 0)); age > maxAge {
			out = append(out, Divergence{unit, fmt.Sprintf("last finished %s ago, older than the %s jcloud.yml accepts", age.Round(time.Minute), t.MaxAge)})
		}
	}
	return out
}

// surveyHost holds what was observed to what h declares, one divergence per fact.
func surveyHost(h Host, o *Observed) []Divergence {
	var out []Divergence
	for _, t := range h.Timers {
		out = append(out, surveyTimer(t, o)...)
	}
	out = append(out, surveyCapacity(h, o)...)
	for _, p := range h.Paths {
		got, seen := o.Paths[p.Path]
		switch {
		case !seen:
			out = append(out, Divergence{p.Path, "is declared in jcloud.yml host.paths and the observations do not report it"})
			continue
		case got[0] == "unknown":
			out = append(out, Divergence{p.Path, "could not be read — stat failed on the host"})
			continue
		}
		if p.Mode != "" && strings.TrimLeft(got[0], "0") != strings.TrimLeft(p.Mode, "0") {
			out = append(out, Divergence{p.Path, fmt.Sprintf("is mode %s and not %s, which jcloud.yml declares", got[0], p.Mode)})
		}
		if p.Owner != "" && got[1] != p.Owner {
			out = append(out, Divergence{p.Path, fmt.Sprintf("is owned by %s and not %s, which jcloud.yml declares", got[1], p.Owner)})
		}
	}
	if h.Firewall != "" {
		switch state, seen := o.Firewalls[h.Firewall]; {
		case !seen:
			out = append(out, Divergence{h.Firewall, "is declared in jcloud.yml host.firewall and the observations do not report it"})
		case state == "active":
		case state == "absent":
			out = append(out, Divergence{h.Firewall, "is not installed, so nothing on this host filters anything"})
		case state == "unreadable":
			out = append(out, Divergence{h.Firewall, "could not be read — its status needs root, and the collector ran without it"})
		default:
			out = append(out, Divergence{h.Firewall, fmt.Sprintf("is %s, so nothing on this host filters anything", state)})
		}
	}
	return out
}
