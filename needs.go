// A need, resolved against the provider that answers it, and compared against the
// compose file that was written by hand beside it.
//
// This is JC32's half of what JC28 already does for the service itself. Nothing is
// rendered: `services/<name>/docker-compose.yml` still writes the db container as
// a second compose service, and what this asserts is that the image, the volume,
// the healthcheck and the `depends_on` are the ones addons/<type>/addon.yml
// declares — because `docker compose config` passes just as happily with a
// database the application cannot reach, on an image nothing pinned, whose volume
// nothing backs up.
//
// Everything here is textual, for the reason at the top of agree.go: a
// half-understood parse of a file another tool owns is a second grammar to keep in
// step. That has one consequence worth stating, since it decides how the assertions
// below are written — none of them can be scoped to *one* compose service, so each
// asserts a string that is unique in the file by construction: the derived name is
// in it, or the pinned digest is.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// resolvedNeed is one entry of `needs:` with its provider in hand. addon is nil
// when nothing provided it, which is reported once by resolveNeeds and then
// skipped by everything downstream rather than reported again per assertion.
type resolvedNeed struct {
	need  Need
	addon *Addon
	// The derived name, which is the compose service, the volume, and the
	// container with `japode-` in front. Computed once here because five
	// assertions want it and five copies of one rule is what this file is against.
	svc string
}

// resolveNeeds reads addons/<type>/addon.yml for every need, from the repository
// root the caller is comparing against.
func (m *Manifest) resolveNeeds(root string, prof *Profile) ([]resolvedNeed, []string) {
	var out []resolvedNeed
	var problems []string
	for _, n := range m.Needs {
		r := resolvedNeed{need: n, svc: m.Name + "-" + n.Name}
		at := filepath.ToSlash(prof.AddonFile(n.Type))
		out = append(out, r)
		last := &out[len(out)-1]

		data, err := os.ReadFile(filepath.Join(root, prof.AddonFile(n.Type)))
		if err != nil {
			problems = append(problems, fmt.Sprintf(
				"%s: needs[%s].type is %q and this repository provides no such addon — a need is answered by a file here, never by something fetched (JC32)",
				at, n.Name, n.Type))
			continue
		}
		a, err := ParseAddon(data)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", at, err))
			continue
		}
		if p := a.Validate(at); len(p) > 0 {
			problems = append(problems, p...)
			continue
		}
		if a.Type != n.Type {
			// The directory is the address and the field is the file saying what
			// it is; disagreeing means one of the two was copied.
			problems = append(problems, fmt.Sprintf("%s: declares type %q and sits under addons/%s/", at, a.Type, n.Type))
			continue
		}
		last.addon = a
	}
	return out, problems
}

// The exact shape a service has to wait on its need in. A textual check cannot
// see this said another way, so the other ways are refused rather than passed —
// the same rule the derived names live under, for the same reason.
func dependsOnHealthy(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*depends_on:\s*\n\s+` + regexp.QuoteMeta(name) + `:\s*\n\s+condition:\s*service_healthy\s*$`)
}

func (m *Manifest) agreeNeeds(at, body string, needs []resolvedNeed, prof *Profile) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	for _, r := range needs {
		if r.addon == nil {
			continue // resolveNeeds already said why, once.
		}
		a := r.addon

		if want := "container_name: " + prof.Container(r.svc); !strings.Contains(body, want) {
			bad("%s: no %s — the need's container name is the service's name and its own, derived", at, want)
		}
		if !strings.Contains(body, a.Image) {
			bad("%s: nothing runs %s, which addons/%s/addon.yml pins", at, a.Image, r.need.Type)
		}
		if !strings.Contains(body, r.svc+":"+a.Volume.Path) {
			bad("%s: nothing mounts %s at %s, so the database is in the container and goes with the next deploy", at, r.svc, a.Volume.Path)
		}
		for _, word := range a.Health.Test {
			if !strings.Contains(body, word) {
				bad("%s: the healthcheck does not run %q, which addons/%s/addon.yml declares", at, word, r.need.Type)
			}
		}
		if a.Health.StartPeriod != "" && !strings.Contains(body, "start_period: "+a.Health.StartPeriod) {
			bad("%s: no start_period: %s for %s", at, a.Health.StartPeriod, r.svc)
		}

		for _, name := range a.Env {
			if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:`).MatchString(body) {
				bad("%s: the environment does not set %s, which addons/%s/addon.yml says the container reads", at, name, r.need.Type)
			}
		}
		for name, value := range a.Fixed {
			// The value may be quoted in compose — `'yes'` is how a bare yes
			// survives YAML's own booleans — so the quotes are optional here and
			// the value is not.
			if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:\s*['"]?` + regexp.QuoteMeta(value) + `['"]?\s*$`).MatchString(body) {
				bad("%s: %s is not %q, which addons/%s/addon.yml fixes", at, name, value, r.need.Type)
			}
		}

		if !dependsOnHealthy(r.svc).MatchString(body) {
			bad("%s: nothing waits on %s with `depends_on:` and `condition: service_healthy` — the application would start against a database still recovering, and `up --wait` would call that a deploy", at, r.svc)
		}

		// And the half no other assertion reaches: whether the application is
		// pointed at the thing it just declared. Every fact above can be right
		// with the service still dialling a hostname nothing serves, which is the
		// disagreement `docker compose config` is happiest about.
		if !m.reaches(r.svc) {
			bad("%s: no variable the manifest fixes names %s, so nothing says the application reaches the database it declares", at, r.svc)
		}
	}
	return p
}

// Whether any fixed value in the manifest mentions the derived name. A fixed value
// and not a default: where the host of a need is settable from .env, the name this
// file derives is not what decides it, and there is nothing here to compare.
func (m *Manifest) reaches(svc string) bool {
	for _, e := range m.Env {
		if e.Value != nil && strings.Contains(*e.Value, svc) {
			return true
		}
	}
	return false
}
