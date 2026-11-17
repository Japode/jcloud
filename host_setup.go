// What a server is built with, declared in jcloud.yml's host: (JCT40).
//
// Japode/cloud's scripts/bootstrap-server.sh set up four things host: could not say:
// a non-root deploy user in the docker group, the firewall's open ports, a sysctl
// file, and the tools the deploy runs. Kept only in that bash, they were what the
// host was built from and nothing checked them afterwards, so the script and the
// watch could drift apart. Declared here, one statement is both: `observe` reads
// each one and `survey` reports each difference with a stable code, the way the
// timers and modes before them do (JCT23), and the step that changes the machine to
// match (JCT38) reads the same declaration.
//
//	host:
//	  user: {name: runner, groups: [docker]}        # exists, in each group
//	  firewall: {kind: ufw, allow: [OpenSSH, 80/tcp, 443/tcp, 443/udp]}
//	  sysctl:
//	    net.ipv6.conf.all.disable_ipv6:
//	      value: "1"
//	      why: right only while no name on this host has an AAAA record
//	    vm.swappiness: "10"                          # or the value alone
//	  requires: [docker, compose, rsync, git]        # on PATH; compose is the plugin
//
// `firewall: ufw` alone still means what it meant: installed and active, no rule
// asserted. An allow rule is held only while the firewall is active, since an
// inactive one's rules filter nothing and firewall.off already says so.
//
// A sysctl's why: is the reason the setting is there, kept beside it so the file
// written from it carries the sentence and not only the number.
//
// This only declares and checks. Changing the machine is JCT38.
package main

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// HostUser is the account the platform deploys as, and the groups it must be in.
type HostUser struct {
	Name   string   `yaml:"name"`
	Groups []string `yaml:"groups"`
}

// HostSysctl is one kernel setting, its value, and why it is set.
type HostSysctl struct {
	Key, Value, Why string
}

// hostFirewall is `firewall:` as written: a bare kind, or {kind, allow}.
type hostFirewall struct {
	Kind  string
	Allow []string
}

func (f *hostFirewall) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		f.Kind = n.Value
		return nil
	}
	var long struct {
		Kind  string   `yaml:"kind"`
		Allow []string `yaml:"allow"`
	}
	if err := decodeStrict(n, &long); err != nil {
		return fmt.Errorf("host.firewall: %w", err)
	}
	f.Kind, f.Allow = long.Kind, long.Allow
	return nil
}

// sysctlValue is one entry's value as written: the value alone, or {value, why}.
type sysctlValue struct{ Value, Why string }

func (s *sysctlValue) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		s.Value = n.Value
		return nil
	}
	var long struct {
		Value string `yaml:"value"`
		Why   string `yaml:"why"`
	}
	if err := decodeStrict(n, &long); err != nil {
		return fmt.Errorf("host.sysctl: %w", err)
	}
	s.Value, s.Why = long.Value, long.Why
	return nil
}

// decodeStrict decodes a node refusing unknown keys, which KnownFields does not
// reach once a type decodes its own node: a misspelt `allow` would assert nothing.
func decodeStrict(n *yaml.Node, into any) error {
	out, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	dec.KnownFields(true)
	return dec.Decode(into)
}

func sysctlList(m map[string]sysctlValue) []HostSysctl {
	var out []HostSysctl
	for k, v := range m {
		out = append(out, HostSysctl{Key: k, Value: v.Value, Why: v.Why})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

var (
	accountWord = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	sysctlWord  = regexp.MustCompile(`^[a-z0-9_]+(\.[A-Za-z0-9_-]+)+$`)
	ruleWord    = regexp.MustCompile(`^[A-Za-z0-9/:._-]+$`)
	toolWord    = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)
)

// validateSetup holds the JCT40 declarations to one word each, for the
// `<kind> <name> <value>` line each travels in.
func (h Host) validateSetup(bad func(string, ...any)) {
	if h.User != nil {
		if !accountWord.MatchString(h.User.Name) {
			bad("host.user: %q is not an account name", h.User.Name)
		}
		for _, g := range h.User.Groups {
			if !accountWord.MatchString(g) {
				bad("host.user: group %q is not a group name", g)
			}
		}
	}
	if len(h.FirewallAllow) > 0 && h.Firewall == "" {
		bad("host.firewall: allow names rules and no kind says which firewall holds them")
	}
	for _, r := range h.FirewallAllow {
		if !ruleWord.MatchString(r) {
			bad("host.firewall: allow %q is not one rule, such as 443/tcp or OpenSSH", r)
		}
	}
	for _, s := range h.Sysctl {
		switch {
		case !sysctlWord.MatchString(s.Key):
			bad("host.sysctl: %q is not a sysctl key, such as net.ipv4.ip_forward", s.Key)
		case s.Value == "" || strings.ContainsAny(s.Value, " \t\n"):
			bad("host.sysctl: %s has value %q, and a value is one word", s.Key, s.Value)
		}
	}
	for _, t := range h.Requires {
		if !toolWord.MatchString(t) {
			bad("host.requires: %q is not a command name", t)
		}
	}
}

// observeSetup reads what the JCT40 declarations name.
//
//	user    <name>  <group,group,…>   absent, or unknown when id cannot be run
//	allow   ufw     <rule>            one per ALLOW rule ufw status lists
//	sysctl  <key>   <value>           unknown when sysctl could not read it
//	tool    <name>  present|absent    unknown when no shell could ask
func observeSetup(h Host, ufwStatus string, run commandRunner) []string {
	var lines []string
	if h.User != nil {
		out, err := run("id", "-nG", h.User.Name)
		state := strings.Join(strings.Fields(out), ",")
		switch {
		case errors.Is(err, exec.ErrNotFound):
			state = "unknown"
		case err != nil || state == "":
			state = "absent"
		}
		lines = append(lines, fmt.Sprintf("user %s %s", h.User.Name, state))
	}
	if h.Firewall == "ufw" && len(h.FirewallAllow) > 0 {
		for _, rule := range ufwAllowed(ufwStatus) {
			lines = append(lines, "allow ufw "+rule)
		}
	}
	for _, s := range h.Sysctl {
		value := "unknown"
		if out, err := run("sysctl", "-n", s.Key); err == nil && len(strings.Fields(out)) == 1 {
			value = strings.TrimSpace(out)
		}
		lines = append(lines, fmt.Sprintf("sysctl %s %s", s.Key, value))
	}
	for _, t := range h.Requires {
		var err error
		if t == "compose" {
			_, err = run("docker", "compose", "version")
		} else {
			_, err = run("sh", "-c", `command -v "$1"`, "sh", t)
		}
		state := "present"
		switch {
		case t != "compose" && errors.Is(err, exec.ErrNotFound):
			state = "unknown"
		case err != nil:
			state = "absent"
		}
		lines = append(lines, fmt.Sprintf("tool %s %s", t, state))
	}
	return lines
}

// ufwAllowed is every rule `ufw status` lists as allowed, once each: its v6 twin
// ("443/tcp (v6)") is the same rule.
func ufwAllowed(status string) []string {
	var out []string
	rules := false
	for _, line := range strings.Split(status, "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && strings.HasPrefix(f[0], "--") {
			rules = true
			continue
		}
		if !rules || len(f) < 2 {
			continue
		}
		for _, w := range f[1:] {
			if w == "ALLOW" && !contains(out, f[0]) {
				out = append(out, f[0])
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// surveySetup holds what was observed to the JCT40 declarations.
func surveySetup(h Host, o *Observed) []Divergence {
	var out []Divergence
	if u := h.User; u != nil {
		switch got, seen := o.Users[u.Name]; {
		case !seen || got == "unknown":
			out = append(out, Divergence{u.Name, "is declared in jcloud.yml host.user and could not be read", "user.unread"})
		case got == "absent":
			out = append(out, Divergence{u.Name, "does not exist, and jcloud.yml says the platform deploys as it", "user.absent"})
		default:
			in := strings.Split(got, ",")
			for _, g := range u.Groups {
				if !contains(in, g) {
					out = append(out, Divergence{u.Name, fmt.Sprintf("is not in the group %s, which jcloud.yml declares", g), "user.group"})
				}
			}
		}
	}
	if h.Firewall != "" && o.Firewalls[h.Firewall] == "active" {
		for _, r := range h.FirewallAllow {
			if !o.Allowed[h.Firewall+" "+r] {
				out = append(out, Divergence{r, fmt.Sprintf("is not allowed by %s, which jcloud.yml says lets it through", h.Firewall), "firewall.closed"})
			}
		}
	}
	for _, s := range h.Sysctl {
		switch got, seen := o.Sysctls[s.Key]; {
		case !seen || got == "unknown":
			out = append(out, Divergence{s.Key, "is declared in jcloud.yml host.sysctl and could not be read", "sysctl.unread"})
		case got != s.Value:
			says := fmt.Sprintf("is %s and not %s, which jcloud.yml declares", got, s.Value)
			if s.Why != "" {
				says += " (" + s.Why + ")"
			}
			out = append(out, Divergence{s.Key, says, "sysctl.value"})
		}
	}
	for _, t := range h.Requires {
		switch got, seen := o.Tools[t]; {
		case !seen || got == "unknown":
			out = append(out, Divergence{t, "is declared in jcloud.yml host.requires and could not be looked for", "tool.unread"})
		case got == "absent":
			out = append(out, Divergence{t, "is not installed, and jcloud.yml says the platform needs it", "tool.absent"})
		}
	}
	return out
}
