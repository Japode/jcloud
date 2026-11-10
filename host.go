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
//	  timers: [japode-backup, japode-restore-test]   # each <unit>.timer must be active
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
)

// Host is what jcloud.yml says the machine must be. The zero value asserts nothing.
type Host struct {
	Timers   []string
	Paths    []HostPath
	Firewall string // "" for none asserted, or the firewall that must be active
}

type HostPath struct {
	Path  string `yaml:"path"`
	Mode  string `yaml:"mode"`
	Owner string `yaml:"owner"`
}

type hostFile struct {
	Timers   []string   `yaml:"timers"`
	Paths    []HostPath `yaml:"paths"`
	Firewall string     `yaml:"firewall"`
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
		case !unitWord.MatchString(t):
			bad("host.timers: %q is not a systemd unit name", t)
		case strings.HasSuffix(t, ".timer"):
			bad("host.timers: %q names the unit without .timer, which is added", t)
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
	return out
}

// observeHost reads what h names on the machine observe runs on, and nothing else.
//
//	timer    <unit>      <systemctl is-active>   unknown when systemd cannot be asked
//	path     <path>      <mode> <user:group>     unknown unknown when stat failed
//	firewall ufw         <status>                absent, or unreadable without root
func observeHost(root string, h Host, run commandRunner) []string {
	var lines []string
	if len(h.Timers) > 0 {
		var units []string
		for _, t := range h.Timers {
			units = append(units, t+".timer")
		}
		// is-active exits non-zero when any unit is not active, and still prints one
		// state per unit, in order, which is the answer.
		out, _ := run("systemctl", append([]string{"is-active"}, units...)...)
		states := strings.Fields(out)
		for i, t := range h.Timers {
			state := "unknown"
			if len(states) == len(units) {
				state = states[i]
			}
			lines = append(lines, fmt.Sprintf("timer %s %s", t, state))
		}
	}
	for _, p := range h.Paths {
		at := p.Path
		if !path.IsAbs(at) {
			at = filepath.Join(root, filepath.FromSlash(at))
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

// surveyHost holds what was observed to what h declares, one divergence per fact.
func surveyHost(h Host, o *Observed) []Divergence {
	var out []Divergence
	for _, t := range h.Timers {
		unit := t + ".timer"
		switch state, seen := o.Timers[t]; {
		case !seen:
			out = append(out, Divergence{unit, "is declared in jcloud.yml host.timers and the observations do not report it"})
		case state == "unknown":
			out = append(out, Divergence{unit, "could not be read — systemctl did not answer on the host"})
		case state != "active":
			out = append(out, Divergence{unit, fmt.Sprintf("is %s, not active, so the job it schedules never fires", state)})
		}
	}
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
