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
	return m.AgreeWith(root, nil)
}

// AgreeWith is Agree with Caddy's adapted configuration in hand. Without it the
// route's meaning is unchecked and said so, rather than checked weakly: the
// substring comparison that used to stand in for it passed on a commented-out
// route (JC33), and a check that can be satisfied by a comment is worse than one
// that says it did not run.
func (m *Manifest) AgreeWith(root string, adaptedConfig []byte) []string {
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

	// The providers first: what a need means is in addons/<type>/addon.yml, and
	// both the compose comparison and the backup.conf one ask it. Resolved once so
	// a type nobody provides is reported once rather than by every check that
	// would have consulted it.
	needs, problems := m.resolveNeeds(root)
	p = append(p, problems...)

	svcDir := filepath.Join("services", m.Name)
	if compose, ok := read(filepath.Join(svcDir, "docker-compose.yml")); ok {
		at := filepath.ToSlash(filepath.Join(svcDir, "docker-compose.yml"))
		p = append(p, m.agreeCompose(at, compose)...)
		p = append(p, m.agreeNeeds(at, compose, needs)...)
	}
	routeRel := filepath.ToSlash(filepath.Join("caddy", "sites", m.Name+".caddy"))
	if route, ok := read(filepath.Join("caddy", "sites", m.Name+".caddy")); ok {
		// Only the source-level assertion stays textual: whether the address block
		// reads {$SVC_HOSTS:…} is a fact about the file, and `caddy adapt` has
		// already resolved that away into the default it carries.
		p = append(p, m.agreeRouteSource(routeRel, route)...)
	}
	switch {
	// len and not nil: an empty file is as much "nobody adapted this" as a nil
	// slice is, and handing "" to the JSON parser reports a malformed config for
	// what is really a missing one.
	case len(adaptedConfig) == 0:
		bad("%s: the adapted configuration was not supplied, so what this route means is unchecked — scripts/check-jcloud.sh runs `caddy adapt` and passes it", routeRel)
	default:
		byHost, err := parseAdapted(adaptedConfig)
		if err != nil {
			bad("%s: %v", routeRel, err)
		} else {
			p = append(p, m.agreeRouteJSON(routeRel, byHost)...)
		}
	}
	// backup.conf is optional: a service with no volume needing special treatment
	// declares none and has no file. Absent with nothing to say is agreement.
	confRel := filepath.ToSlash(filepath.Join(svcDir, "backup.conf"))
	conf, err := os.ReadFile(filepath.Join(root, svcDir, "backup.conf"))
	switch {
	case err == nil:
		p = append(p, m.agreeBackupConf(confRel, string(conf), needs)...)
	case m.wantsBackupConf(needs):
		bad("%s: the manifest declares sqlite or exclude paths, or a need whose provider asks for a directive, and this file is not there", confRel)
	}
	if env, ok := read(".env.example"); ok {
		p = append(p, m.agreeEnvExample(".env.example", env)...)
	}
	return p
}

// From the first line matching re to the end, or the whole thing when nothing
// matches — an absence is reported by the assertion that was looking, not by this
// silently narrowing to nothing.
func firstFrom(body, re string) string {
	if loc := regexp.MustCompile(re).FindStringIndex(body); loc != nil {
		return body[loc[0]:]
	}
	return body
}

func (m *Manifest) wantsBackupConf(needs []resolvedNeed) bool {
	for _, v := range m.Volumes {
		if len(v.SQLite) > 0 || len(v.Exclude) > 0 {
			return true
		}
	}
	for _, n := range needs {
		if n.addon != nil && n.addon.Volume.Backup != "" {
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

// The variable an address block reads its hosts from. The log field is what
// separates two routes on one service, so a manifest with one route spells it
// `<SVC>_HOSTS` and a second route called `content` spells it
// `<SVC>_CONTENT_HOSTS`.
//
// Derived here once because three checks ask for it — the route's source, the
// .env.example entry, and the scan for keys nothing declares — and three copies of
// one rule is the drift the whole comparison exists to end.
func (m *Manifest) hostsVar(r Route) string {
	if r.Log == "" || r.Log == "access" {
		return m.SCREAM() + "_HOSTS"
	}
	return m.SCREAM() + "_" + strings.ToUpper(r.Log) + "_HOSTS"
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
	//
	// Read from this service's own `container_name:` forward, and not from the top
	// of the file: since JC32 a compose file here can declare a second service —
	// the need's database, with a healthcheck of its own — and the first
	// `healthcheck:` in the file was then whichever of the two somebody wrote
	// first. Anchored to the line so that `japode-paste` does not match
	// `japode-paste-db`.
	own := firstFrom(body, `(?m)^\s*container_name: `+regexp.QuoteMeta("japode-"+m.Name)+`\s*$`)
	if i := strings.Index(own, "healthcheck:"); i < 0 {
		bad("%s: no healthcheck:, so the deploy has nothing to wait on", at)
	} else {
		hc := own[i:]
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

// What the route's *source* has to say, as opposed to what it means. Everything
// here is about a variable reference that the adapted configuration resolves away,
// so it can only be asked of the text — and a `#` cannot fake it into passing,
// because a missing variable is what fails, not a present string.
func (m *Manifest) agreeRouteSource(at, body string) []string {
	var p []string
	for i, r := range m.Routes {
		hostsVar := m.hostsVar(r)
		if !strings.Contains(body, "{$"+hostsVar+":") {
			p = append(p, fmt.Sprintf("%s routes[%d]: no address block reads {$%s:…}, so its hosts are not settable from .env",
				at, i, hostsVar))
		}
	}
	return p
}

func (m *Manifest) agreeBackupConf(at, body string, needs []resolvedNeed) []string {
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

	// A need's volume takes whichever directive its provider asks for, and that
	// directive names one word: the volume, which is also the compose service, so
	// the line that says how to copy a database says it once. Which word it is
	// belongs to the addon, because whoever writes `needs: [{type: mariadb}]` has
	// no way of knowing that a live InnoDB directory cannot be copied.
	for _, n := range needs {
		if n.addon == nil || n.addon.Volume.Backup == "" {
			continue
		}
		if !hasDirective(body, n.addon.Volume.Backup, n.svc) {
			bad("%s: no `%s %s`, so the database addons/%s/addon.yml brings would be copied byte for byte, under its own writer",
				at, n.addon.Volume.Backup, n.svc, n.need.Type)
		}
	}

	// And the other direction: a line here that the manifest does not declare is a
	// fact written in one place only, which is what this check exists to end.
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if !m.declaresDirective(fields, needs) {
			bad("%s: `%s` is not declared by the manifest", at, strings.Join(fields, " "))
		}
	}
	return p
}

// hasDirective is one line, whole: the directive word and every field after it.
// Variadic because a volume's directives take a path and a need's do not, and a
// second function would be one more place the field count is written down.
func hasDirective(body, kind string, want ...string) bool {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) != len(want)+1 || fields[0] != kind {
			continue
		}
		same := true
		for i, w := range want {
			if fields[i+1] != w {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

func (m *Manifest) declaresDirective(fields []string, needs []resolvedNeed) bool {
	kind, key := fields[0], fields[1]
	if len(fields) == 2 {
		for _, n := range needs {
			if n.addon != nil && n.addon.Volume.Backup == kind && n.svc == key {
				return true
			}
		}
		return false
	}
	return m.declares(kind, key, fields[2])
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
		key := m.hostsVar(r)
		if !has(key) {
			bad("%s: no %s=, so the addresses this route answers for cannot be changed without editing the route", at, key)
		}
	}

	p = append(p, m.orphanEnvKeys(at, body)...)
	return p
}

// The other direction, which backup.conf has had all along and this did not: a key
// carrying this service's prefix that the manifest does not declare.
//
// It matters because .env.example is what a person reads when writing SERVICES_ENV.
// A variable removed from a service leaves its key behind, and an orphan there is
// worse than an absence — it looks deliberate, so somebody sets it and nothing
// reads it.
//
// Scoped to this service's own prefix. A manifest knows what it declares and
// nothing about what any other service or the edge does, so `ACME_EMAIL` and
// `BACKUP_*` are not its business. Two services cannot collide here either: the
// prefix ends in `_`, so `SKILL_` is not a prefix of `SKILLGIST_HOSTS`.
func (m *Manifest) orphanEnvKeys(at, body string) []string {
	var p []string

	prefix := m.SCREAM() + "_"
	// Every key the manifest accounts for, including the fixed ones. A fixed value
	// has no business being here and the check above already says so by name —
	// listing it as unaccounted for would report one mistake twice.
	known := map[string]bool{}
	for _, e := range m.Env {
		known[prefix+e.Name] = true
	}
	for _, r := range m.Routes {
		known[m.hostsVar(r)] = true
	}

	key := regexp.MustCompile(`^([A-Z][A-Z0-9_]*)=`)
	for _, line := range strings.Split(body, "\n") {
		match := key.FindStringSubmatch(line)
		if match == nil || !strings.HasPrefix(match[1], prefix) || known[match[1]] {
			continue
		}
		p = append(p, fmt.Sprintf("%s: %s= is not declared by the manifest — a key nobody reads is an invitation to set it", at, match[1]))
	}
	return p
}
