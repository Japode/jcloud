// Timers host apply installs (JCT43).
//
// A server built by `jcloud install` served the site and backed nothing up: the
// timers jcloud.yml declared (Japode's backup, restore drill and health watch) were
// written by the platform's scripts/install-timers.sh, inline unit files around
// scripts the archive did not carry, so `survey` was the first to notice and
// nothing could fix it from the archive alone.
//
// So a timer entry may say what it runs and when, and host apply writes its two
// units from that and enables it:
//
//	timers:
//	  - name: japode-backup
//	    run: scripts/backup.sh                 # bash, a script under the root
//	    on: "*-*-* 03,09,15,21:20:00"          # OnCalendar
//	    timeout: 2h                            # TimeoutStartSec
//	    jitter: 15m                            # RandomizedDelaySec
//	    persistent: true                       # a run missed while off happens on boot
//	    max_age: 7h                            # survey's, as before (JCT24)
//	    env: [BACKUP_REPOSITORY, BACKUP_PASSWORD]
//	  - name: japode-restore-test
//	    run: scripts/restore-test.sh
//	    stop_post: scripts/restore-test.sh --cleanup   # ExecStopPost, runs after a kill
//	  - name: japode-healthwatch
//	    run: scripts/healthwatch.sh
//	    success_exit: [1]                      # SuccessExitStatus
//	    accuracy: 30s                          # AccuracySec
//
// The service runs as host.user when one is declared, from the root, after docker
// and the network, and requires docker.socket when requires: names docker. Each
// unit file says it was written from jcloud.yml and that the next apply rewrites it.
//
// Every script a timer runs has to travel with the service, and a script sources
// neighbours jcloud cannot follow, so the platform names whole paths for export to
// carry (jcloud.yml's `carry:`), and an export whose timer runs a script outside
// them is refused. A timer's env: names the configuration keys its script reads
// beside the service's own (where its backups go, where it alerts), so they travel
// sealed with the rest (JCT33).
//
// install applies the host twice: before anything is written, without these timers,
// because their scripts are not laid down yet; and last, once the service is up, so
// the first run finds the tree it runs in.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// installs is whether host apply writes this timer's units.
func (t HostTimer) installs() bool { return t.Run != "" }

// script is the root-relative script a command line runs, and its arguments.
func script(command string) (string, []string) {
	f := strings.Fields(command)
	if len(f) == 0 {
		return "", nil
	}
	return f[0], f[1:]
}

// validateUnit holds the fields host apply writes units from.
func (t HostTimer) validateUnit(bad func(string, ...any)) {
	if !t.installs() {
		if t.On != "" || t.Timeout != "" || t.Jitter != "" || t.Accuracy != "" || t.Persistent ||
			len(t.SuccessExit) > 0 || t.StopPost != "" || t.Description != "" || len(t.Env) > 0 {
			bad("host.timers: %s says how to run it and not what it runs; add run:, or keep only name and max_age", t.Name)
		}
		return
	}
	if t.On == "" {
		bad("host.timers: %s has run: and no on:, so nothing says when it fires", t.Name)
	}
	for key, command := range map[string]string{"run": t.Run, "stop_post": t.StopPost} {
		if command == "" {
			continue
		}
		s, _ := script(command)
		clean := path.Clean(s)
		if path.IsAbs(s) || clean == ".." || strings.HasPrefix(clean, "../") {
			bad("host.timers: %s's %s is %q, and a script it runs sits under the platform root", t.Name, key, s)
		}
	}
	for key, value := range map[string]string{"on": t.On, "description": t.Description, "run": t.Run, "stop_post": t.StopPost} {
		if strings.ContainsAny(value, "\n\r") {
			bad("host.timers: %s's %s spans lines", t.Name, key)
		}
	}
	for key, value := range map[string]string{"timeout": t.Timeout, "jitter": t.Jitter, "accuracy": t.Accuracy} {
		if d, err := time.ParseDuration(value); value != "" && (err != nil || d <= 0) {
			bad("host.timers: %s has %s %q, and it is a positive duration such as 15m", t.Name, key, value)
		}
	}
	for _, code := range t.SuccessExit {
		if code < 0 || code > 255 {
			bad("host.timers: %s has success_exit %d, and an exit status is 0 to 255", t.Name, code)
		}
	}
	for _, k := range t.Env {
		if !envKeyRE.MatchString(k) {
			bad("host.timers: %s's env names %q, which is not a configuration key", t.Name, k)
		}
	}
}

// seconds is a duration as systemd reads it unambiguously.
func seconds(d string) string {
	v, _ := time.ParseDuration(d) // validate refused a bad one
	return strconv.Itoa(int(v.Seconds())) + "s"
}

// bashLine is ExecStart's value for a script under root and its arguments.
func bashLine(root, command string) string {
	s, args := script(command)
	return strings.Join(append([]string{"/usr/bin/env", "bash", path.Join(root, path.Clean(s))}, args...), " ")
}

const unitHeader = "# Written by jcloud host apply from jcloud.yml's host.timers (JCT43).\n" +
	"# Change the declaration there, not this file: the next apply rewrites it.\n"

// timerUnits is the service and timer unit files for t, with root absolute.
func timerUnits(t HostTimer, h Host, root string) (service, timer string) {
	desc := t.Description
	if desc == "" {
		desc = "jcloud: " + t.Name
	}
	var s strings.Builder
	s.WriteString(unitHeader)
	s.WriteString("[Unit]\nDescription=" + desc + "\n")
	s.WriteString("After=docker.service network-online.target\nWants=network-online.target\n")
	if contains(h.Requires, "docker") {
		s.WriteString("Requires=docker.socket\n")
	}
	s.WriteString("\n[Service]\nType=oneshot\n")
	if h.User != nil {
		s.WriteString("User=" + h.User.Name + "\n")
	}
	s.WriteString("WorkingDirectory=" + root + "\n")
	s.WriteString("ExecStart=" + bashLine(root, t.Run) + "\n")
	if t.StopPost != "" {
		s.WriteString("ExecStopPost=" + bashLine(root, t.StopPost) + "\n")
	}
	if len(t.SuccessExit) > 0 {
		var codes []string
		for _, c := range t.SuccessExit {
			codes = append(codes, strconv.Itoa(c))
		}
		s.WriteString("SuccessExitStatus=" + strings.Join(codes, " ") + "\n")
	}
	if t.Timeout != "" {
		s.WriteString("TimeoutStartSec=" + seconds(t.Timeout) + "\n")
	}

	var tm strings.Builder
	tm.WriteString(unitHeader)
	tm.WriteString("[Unit]\nDescription=" + desc + ", " + t.On + "\n\n[Timer]\nOnCalendar=" + t.On + "\n")
	if t.Jitter != "" {
		tm.WriteString("RandomizedDelaySec=" + seconds(t.Jitter) + "\n")
	}
	if t.Accuracy != "" {
		tm.WriteString("AccuracySec=" + seconds(t.Accuracy) + "\n")
	}
	if t.Persistent {
		tm.WriteString("Persistent=true\n")
	}
	tm.WriteString("\n[Install]\nWantedBy=timers.target\n")
	return s.String(), tm.String()
}

// timers is host apply's last step: each installing timer's units written, systemd
// reloaded when any changed, and each timer enabled, or restarted when its units
// changed under it.
func (a *hostApplier) timers(h Host) error {
	var todo []HostTimer
	for _, t := range h.Timers {
		if t.installs() {
			todo = append(todo, t)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	root, err := filepath.Abs(a.root)
	if err != nil {
		return err
	}
	root = filepath.ToSlash(root)
	if strings.ContainsAny(root, " \t") {
		return fmt.Errorf("the root %q has a space in it, which a unit's ExecStart would split", root)
	}
	// Every script is there before any unit points at it: a timer firing a script
	// that is not laid down fails at its first run, which nobody is watching.
	for _, t := range todo {
		for _, command := range []string{t.Run, t.StopPost} {
			if command == "" {
				continue
			}
			s, _ := script(command)
			if _, err := os.Stat(filepath.Join(a.root, filepath.FromSlash(s))); errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%s runs %s, which is not under %s; lay the tree down first", t.Name, s, root)
			}
		}
	}
	changed := map[string]bool{}
	for _, t := range todo {
		service, timer := timerUnits(t, h, root)
		before := len(a.did)
		if err := a.write("etc/systemd/system/"+t.Name+".service", service); err != nil {
			return err
		}
		if err := a.write("etc/systemd/system/"+t.Name+".timer", timer); err != nil {
			return err
		}
		changed[t.Name] = len(a.did) > before
	}
	for _, c := range changed {
		if c {
			if err := a.do("systemctl", "daemon-reload"); err != nil {
				return err
			}
			break
		}
	}
	for _, t := range todo {
		state, _ := a.run("systemctl", "is-active", t.Name+".timer")
		switch {
		case strings.TrimSpace(state) != "active":
			if err := a.do("systemctl", "enable", "--now", t.Name+".timer"); err != nil {
				return err
			}
		case changed[t.Name]:
			if err := a.do("systemctl", "restart", t.Name+".timer"); err != nil {
				return err
			}
		}
	}
	return nil
}

// withoutInstalls is h with no timer installed, for the apply install makes before
// the tree its timers run in is laid down.
func (h Host) withoutInstalls() Host {
	out := h
	out.Timers = nil
	for _, t := range h.Timers {
		if !t.installs() {
			out.Timers = append(out.Timers, t)
		}
	}
	return out
}

// installsTimers is whether any timer of h is one host apply writes.
func (h Host) installsTimers() bool {
	for _, t := range h.Timers {
		if t.installs() {
			return true
		}
	}
	return false
}

// installedTimers is what host apply holds its timers to afterwards: active, and
// a last run that did not fail. Not their age: a timer installed a second ago has
// never run, which is no divergence of the apply's.
func (h Host) installedTimers() []HostTimer {
	var out []HostTimer
	for _, t := range h.Timers {
		if t.installs() {
			out = append(out, HostTimer{Name: t.Name})
		}
	}
	return out
}

// timerScripts is every root-relative script h's timers run.
func (h Host) timerScripts() []string {
	var out []string
	for _, t := range h.Timers {
		for _, command := range []string{t.Run, t.StopPost} {
			if s, _ := script(command); s != "" && !contains(out, path.Clean(s)) {
				out = append(out, path.Clean(s))
			}
		}
	}
	return out
}

// timerEnv is every configuration key h's timers read.
func (h Host) timerEnv() []string {
	var out []string
	for _, t := range h.Timers {
		for _, k := range t.Env {
			if !contains(out, k) {
				out = append(out, k)
			}
		}
	}
	return out
}
