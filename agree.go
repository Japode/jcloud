// Does the manifest agree with the files that are actually deployed?
//
// This is the half of JC28 that closes its symptom. The port a service listens on
// is written in three places — `expose:` in its compose file, `reverse_proxy
// name:port` in its route, and the healthcheck that probes it — and `docker compose
// config` and `caddy validate` both pass with a route aimed at a port nothing is
// listening on. Neither tool compares the three, because neither knows they are
// meant to be the same number.
//
// docs/JCLOUD.md's answer is to render all three from the manifest, which makes the
// disagreement unrepresentable rather than checked. That is the better end state and
// it is not what this file does, for one reason worth writing down: the three files
// carry 67 lines of comment between them — why the X-Forwarded-For line stays
// despite `caddy validate` warning it is unnecessary, what the rate-limit bucket
// has to do with it, why the log paths differ — and the manifest has nowhere to put
// any of it. A generator that emits a "do not edit" header in place of that prose
// destroys the reasoning this repository exists to keep. So: check now, render when
// the manifest can carry the prose too.
//
// Everything here is read-only and textual. It parses no YAML beyond the manifest
// and no Caddyfile grammar: it asserts that specific facts appear, because a
// half-understood parse of a file another tool owns is a second grammar to keep in
// step. What it cannot see it says nothing about rather than guessing.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Agree reports every disagreement between the manifest and the files beside it.
// root is the repository root; the manifest's own directory is derived from name,
// because that convention is the schema's and not a caller's to vary.
func (m *Manifest) Agree(root string) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	read := func(rel string) (string, bool) {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			bad("%s: %v", rel, err)
			return "", false
		}
		return string(data), true
	}

	svcDir := filepath.Join("services", m.Name)
	if compose, ok := read(filepath.Join(svcDir, "docker-compose.yml")); ok {
		p = append(p, m.agreeCompose(filepath.ToSlash(filepath.Join(svcDir, "docker-compose.yml")), compose)...)
	}
	if route, ok := read(filepath.Join("caddy", "sites", m.Name+".caddy")); ok {
		p = append(p, m.agreeRoute(filepath.ToSlash(filepath.Join("caddy", "sites", m.Name+".caddy")), route)...)
	}
	// backup.conf is optional: a service with no volume needing special treatment
	// declares none and has no file. Absent with nothing to say is agreement.
	confRel := filepath.ToSlash(filepath.Join(svcDir, "backup.conf"))
	conf, err := os.ReadFile(filepath.Join(root, svcDir, "backup.conf"))
	switch {
	case err == nil:
		p = append(p, m.agreeBackupConf(confRel, string(conf))...)
	case m.wantsBackupConf():
		bad("%s: the manifest declares sqlite or exclude paths and this file is not there", confRel)
	}
	if env, ok := read(".env.example"); ok {
		p = append(p, m.agreeEnvExample(".env.example", env)...)
	}
	return p
}

func (m *Manifest) wantsBackupConf() bool {
	for _, v := range m.Volumes {
		if len(v.SQLite) > 0 || len(v.Exclude) > 0 {
			return true
		}
	}
	return false
}

// SCREAM is the manifest's short name as the variable prefix the deploy already
// uses — scripts/deploy.sh's image_var() computes the same thing, and two spellings
// of one rule is the drift this exists to remove.
func (m *Manifest) SCREAM() string {
	return strings.ToUpper(strings.ReplaceAll(m.Name, "-", "_"))
}

func (m *Manifest) agreeCompose(at, body string) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	// `expose:` is a list of quoted or bare ports; the assertion is that the
	// manifest's port is among them, not that it is the only one.
	if !regexp.MustCompile(`(?m)^\s*-\s*'?"?` + fmt.Sprint(m.Port) + `'?"?\s*$`).MatchString(body) {
		bad("%s: nothing exposes port %d, which the manifest declares", at, m.Port)
	}

	if !strings.Contains(body, "${"+m.SCREAM()+"_IMAGE") {
		bad("%s: the image is not ${%s_IMAGE}, which is what the deploy pins", at, m.SCREAM())
	}
	if want := "japode-" + m.Name; !strings.Contains(body, "container_name: "+want) {
		bad("%s: container_name is not %s, which the short name derives", at, want)
	}

	// The healthcheck has to probe the manifest's path *and* its port, because a
	// healthcheck aimed at the wrong port is the failure `docker compose config`
	// cannot see and a deploy discovers as a service that never turns healthy.
	if i := strings.Index(body, "healthcheck:"); i < 0 {
		bad("%s: no healthcheck:, so the deploy has nothing to wait on", at)
	} else {
		hc := body[i:]
		if !strings.Contains(hc, m.Health.Path) {
			bad("%s: the healthcheck does not probe %s, which the manifest declares", at, m.Health.Path)
		}
		if !strings.Contains(hc, fmt.Sprint(m.Port)) {
			bad("%s: the healthcheck does not probe port %d", at, m.Port)
		}
		if m.Health.StartPeriod != "" && !strings.Contains(hc, "start_period: "+m.Health.StartPeriod) {
			bad("%s: the healthcheck's start_period is not %s", at, m.Health.StartPeriod)
		}
	}

	for _, v := range m.Volumes {
		want := m.Name + "-" + v.Name
		if !strings.Contains(body, want+":"+v.Path) {
			bad("%s: nothing mounts %s at %s", at, want, v.Path)
		}
	}

	for _, e := range m.Env {
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(e.Name) + `:`).MatchString(body) {
			bad("%s: the environment does not set %s, which the manifest declares", at, e.Name)
			continue
		}
		switch {
		case e.Value != nil:
			// A fixed value is fixed here too: rendering it as ${…} would make it
			// configurable, which is the opposite of what value: means.
			if !strings.Contains(body, e.Name+": "+*e.Value) {
				bad("%s: %s is not the fixed value %q the manifest declares", at, e.Name, *e.Value)
			}
		default:
			if !strings.Contains(body, "${"+m.SCREAM()+"_"+e.Name) {
				bad("%s: %s does not read ${%s_%s}, so .env cannot set it", at, e.Name, m.SCREAM(), e.Name)
			}
		}
	}
	return p
}

func (m *Manifest) agreeRoute(at, body string) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	// The upstream is the compose service name and the manifest's port. This is the
	// assertion the symptom names: `caddy validate` proves the file parses and says
	// nothing about whether anything listens where it points.
	upstream := fmt.Sprintf("reverse_proxy %s:%d", m.Name, m.Port)
	if n := strings.Count(body, upstream); n != len(m.Routes) {
		bad("%s: %d block(s) proxy to %s:%d, and the manifest declares %d route(s)",
			at, n, m.Name, m.Port, len(m.Routes))
	}

	for i, r := range m.Routes {
		at := fmt.Sprintf("%s routes[%d]", at, i)
		if !strings.Contains(body, "health_uri "+m.Health.Path) {
			bad("%s: no health_uri %s — the edge would take an unhealthy upstream out of rotation on a different path than the deploy waits on", at, m.Health.Path)
		}
		for _, d := range r.Proxy {
			// Whitespace-insensitive: the route is tab-indented inside its block
			// and the manifest states the directive, not its indentation.
			if !strings.Contains(squeeze(body), squeeze(d)) {
				bad("%s: the proxy body does not carry %q, which the manifest declares", at, d)
			}
		}
		// The hosts variable, whose name the log field separates when a service has
		// more than one route.
		hostsVar := m.SCREAM() + "_HOSTS"
		if r.Log != "" && r.Log != "access" {
			hostsVar = m.SCREAM() + "_" + strings.ToUpper(r.Log) + "_HOSTS"
		}
		if !strings.Contains(body, "{$"+hostsVar+":") {
			bad("%s: no address block reads {$%s:…}, so its hosts are not settable from .env", at, hostsVar)
		}
		for _, h := range r.Hosts {
			if !strings.Contains(body, h) {
				bad("%s: %s is declared by the manifest and does not appear here", at, h)
			}
		}
		if r.MaxBody != "" && !strings.Contains(squeeze(body), "max_size "+r.MaxBody) {
			bad("%s: no max_size %s", at, r.MaxBody)
		}
	}
	return p
}

// Collapse runs of whitespace so an assertion about a directive is not an assertion
// about how the file is indented.
func squeeze(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (m *Manifest) agreeBackupConf(at, body string) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	// The volume key backup.conf names is the compose key, which is the short name
	// plus the volume's own — the same derivation the compose check makes.
	for _, v := range m.Volumes {
		key := m.Name + "-" + v.Name
		for _, f := range v.SQLite {
			if !hasDirective(body, "sqlite", key, f) {
				bad("%s: no `sqlite %s %s`, so a database the manifest declares would be copied byte for byte", at, key, f)
			}
		}
		for _, f := range v.Exclude {
			if !hasDirective(body, "exclude", key, f) {
				bad("%s: no `exclude %s %s`", at, key, f)
			}
		}
	}

	// And the other direction: a line here that the manifest does not declare is a
	// fact written in one place only, which is what this check exists to end.
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		kind, key, path := fields[0], fields[1], fields[2]
		if !m.declares(kind, key, path) {
			bad("%s: `%s %s %s` is not declared by the manifest", at, kind, key, path)
		}
	}
	return p
}

func hasDirective(body, kind, key, path string) bool {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == kind && fields[1] == key && fields[2] == path {
			return true
		}
	}
	return false
}

func (m *Manifest) declares(kind, key, path string) bool {
	for _, v := range m.Volumes {
		if m.Name+"-"+v.Name != key {
			continue
		}
		list := v.SQLite
		if kind == "exclude" {
			list = v.Exclude
		}
		for _, f := range list {
			if f == path {
				return true
			}
		}
	}
	return false
}

func (m *Manifest) agreeEnvExample(at, body string) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	has := func(key string) bool {
		return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=`).MatchString(body)
	}

	for _, e := range m.Env {
		key := m.SCREAM() + "_" + e.Name
		switch {
		case e.Value != nil:
			// A fixed value is not configuration. Its presence here would invite
			// somebody to set it and be ignored.
			if has(key) {
				bad("%s: %s= is here and the manifest fixes that value — it is not configurable", at, key)
			}
		case !has(key):
			bad("%s: no %s=, so nobody writing SERVICES_ENV learns the variable exists", at, key)
		}
	}

	for _, r := range m.Routes {
		key := m.SCREAM() + "_HOSTS"
		if r.Log != "" && r.Log != "access" {
			key = m.SCREAM() + "_" + strings.ToUpper(r.Log) + "_HOSTS"
		}
		if !has(key) {
			bad("%s: no %s=, so the addresses this route answers for cannot be changed without editing the route", at, key)
		}
	}
	return p
}
