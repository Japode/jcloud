// The compose half of `agree`, asked of the resolved compose model instead of
// one file's text (JCT4).
//
// agreeCompose reads services/<name>/docker-compose.yml and asserts substrings.
// That has two holes. A platform that declares every service in one compose file,
// separated by profiles rather than directories, has no per-service file, and
// against a shared file a substring matches a neighbour's block. And a substring
// passes on a commented-out line, the hole JC33 closed for routes.
//
// So the platform's script runs
//
//	docker compose --profile '*' config --no-interpolate --format json
//
// over whatever files make up its model, and hands the JSON over with --compose,
// the way `caddy adapt` and `nginx -T` arrive with --routes. Every profile is on so
// that a service outside the default one is in the model at all. Interpolation is
// off because what is checked is the *reference*: that the image reads
// ${<NAME>_IMAGE}, that a variable reads ${<NAME>_<VAR>}. Resolving them would
// erase exactly that. Compose has already done the rest: comments are gone, the
// short volume syntax is the long one, and every service is a key of its own.
//
// Without --compose, agree keeps the textual check over the per-service file. That
// is the fallback while a platform script does not yet produce the model, not a
// second rule: every assertion below is the one agreeCompose and agreeNeeds make.
package main

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

type composeModel struct {
	Services map[string]composeService `json:"services"`
}

type composeService struct {
	ContainerName string `json:"container_name"`
	Image         string `json:"image"`
	Build         *struct {
		Context    string `json:"context"`
		Dockerfile string `json:"dockerfile"`
	} `json:"build"`
	Environment map[string]*string `json:"environment"`
	Expose      []string           `json:"expose"`
	Ports       []struct {
		Target int `json:"target"`
	} `json:"ports"`
	Healthcheck *struct {
		Test        []string `json:"test"`
		StartPeriod string   `json:"start_period"`
	} `json:"healthcheck"`
	Volumes []struct {
		Type   string `json:"type"`
		Source string `json:"source"`
		Target string `json:"target"`
	} `json:"volumes"`
	DependsOn map[string]struct {
		Condition string `json:"condition"`
	} `json:"depends_on"`
	Profiles []string `json:"profiles"`
}

func parseComposeModel(data []byte) (*composeModel, error) {
	var c composeModel
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("not a `docker compose config --format json` output: %w", err)
	}
	if c.Services == nil {
		return nil, fmt.Errorf("not a `docker compose config --format json` output: it has no services")
	}
	return &c, nil
}

func (s composeService) health() string {
	if s.Healthcheck == nil {
		return ""
	}
	return strings.Join(s.Healthcheck.Test, " ")
}

func (s composeService) mounts(source, target string) bool {
	for _, v := range s.Volumes {
		if v.Type == "volume" && v.Source == source && v.Target == target {
			return true
		}
	}
	return false
}

// dockerfile is the file a service builds from, context and all, with forward
// slashes, or "" for one that builds nothing. The context compose prints is
// absolute on the machine that ran it, so a manifest's path is compared as its
// suffix.
func (s composeService) dockerfile() string {
	if s.Build == nil {
		return ""
	}
	file := s.Build.Dockerfile
	if file == "" {
		file = "Dockerfile"
	}
	return path.Join(strings.ReplaceAll(s.Build.Context, `\`, "/"), file)
}

// targets is a volume mounted at target, whatever it is called.
func (s composeService) targets(target string) bool {
	for _, v := range s.Volumes {
		if v.Type == "volume" && v.Target == target {
			return true
		}
	}
	return false
}

// listens is `expose:` or a published port's target: either way the container is
// meant to be reached on it.
func (s composeService) listens(port int) bool {
	for _, e := range s.Expose {
		if e == fmt.Sprint(port) || strings.HasPrefix(e, fmt.Sprint(port)+"/") {
			return true
		}
	}
	for _, p := range s.Ports {
		if p.Target == port {
			return true
		}
	}
	return false
}

// agreeComposeModel is agreeCompose and agreeNeeds against the model.
func (m *Manifest) agreeComposeModel(model *composeModel, needs []resolvedNeed, prof *Profile) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	at := "compose services." + m.Name
	svc, ok := model.Services[m.Name]
	if !ok {
		bad("%s: the compose model has no such service, so nothing deploys what the manifest declares", at)
		return p
	}

	switch {
	case m.Profile == "" && len(svc.Profiles) > 0:
		bad("%s: is under profile(s) %v and the manifest declares none, so the default profile would not start it", at, svc.Profiles)
	case m.Profile != "" && !contains(svc.Profiles, m.Profile):
		bad("%s: is not under profile %s, which the manifest declares (it is under %v)", at, m.Profile, svc.Profiles)
	}

	if !svc.listens(m.Port) {
		bad("%s: nothing exposes port %d, which the manifest declares", at, m.Port)
	}
	switch {
	case m.Build != "":
		if got := svc.dockerfile(); !strings.HasSuffix("/"+got, "/"+path.Clean(m.Build)) {
			bad("%s: builds from %q and not %s, which the manifest declares", at, got, m.Build)
		}
	default:
		if imageVar := prof.ImageVarOf(m); !strings.Contains(svc.Image, "${"+imageVar) {
			bad("%s: the image is %q and not ${%s}, which is what the deploy pins", at, svc.Image, imageVar)
		}
	}
	if want := prof.Container(m.Name); svc.ContainerName != want {
		bad("%s: container_name is %q and not %s, which the short name derives", at, svc.ContainerName, want)
	}

	if svc.Healthcheck == nil {
		bad("%s: no healthcheck:, so the deploy has nothing to wait on", at)
	} else {
		hc := svc.health()
		if !strings.Contains(hc, m.Health.Path) {
			bad("%s: the healthcheck does not probe %s, which the manifest declares", at, m.Health.Path)
		}
		if !strings.Contains(hc, fmt.Sprint(m.Port)) {
			bad("%s: the healthcheck does not probe port %d", at, m.Port)
		}
		if m.Health.StartPeriod != "" && svc.Healthcheck.StartPeriod != m.Health.StartPeriod {
			bad("%s: the healthcheck's start_period is %q and not %s", at, svc.Healthcheck.StartPeriod, m.Health.StartPeriod)
		}
	}

	for _, v := range m.Volumes {
		if want := m.Name + "-" + v.Name; !svc.mounts(want, v.Path) {
			bad("%s: nothing mounts %s at %s", at, want, v.Path)
		}
	}

	for _, e := range m.Env {
		got, set := svc.Environment[e.Name]
		switch {
		case !set:
			bad("%s: the environment does not set %s, which the manifest declares", at, e.Name)
		case e.Value != nil:
			if got == nil || *got != *e.Value {
				bad("%s: %s is not the fixed value %q the manifest declares", at, e.Name, *e.Value)
			}
		case got == nil || !strings.Contains(*got, "${"+m.SCREAM()+"_"+e.Name):
			bad("%s: %s does not read ${%s_%s}, so .env cannot set it", at, e.Name, m.SCREAM(), e.Name)
		}
	}

	for _, r := range needs {
		if r.addon != nil {
			p = append(p, m.agreeNeedModel(model, svc, r, prof)...)
		}
	}
	return p
}

func (m *Manifest) agreeNeedModel(model *composeModel, app composeService, r resolvedNeed, prof *Profile) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }
	a := r.addon
	at := "compose services." + r.svc

	// A shared need (JCT5) is the platform's service: its name, its volume's name
	// and the variables each client states inside it are the platform's to choose,
	// so only what the addon pins is held — image, a volume at its path, health,
	// fixed values — and, below, that this client waits on it and reaches it.
	shared := r.need.Shared != ""

	db, ok := model.Services[r.svc]
	if !ok {
		if shared {
			bad("%s: the compose model has no such service, which needs[%s].shared names", at, r.need.Name)
		} else {
			bad("%s: the compose model has no such service, and addons/%s brings one", at, r.need.Type)
		}
		return p
	}
	if want := prof.Container(r.svc); !shared && db.ContainerName != want {
		bad("%s: container_name is %q and not %s — the need's container name is the service's name and its own, derived", at, db.ContainerName, want)
	}
	if db.Image != a.Image {
		bad("%s: runs %q and not %s, which addons/%s pins", at, db.Image, a.Image, r.need.Type)
	}
	switch {
	case shared && !db.targets(a.Volume.Path):
		bad("%s: no volume is mounted at %s, so the database every client shares is in the container and goes with the next deploy", at, a.Volume.Path)
	case !shared && !db.mounts(r.svc, a.Volume.Path):
		bad("%s: nothing mounts %s at %s, so the database is in the container and goes with the next deploy", at, r.svc, a.Volume.Path)
	}
	hc := db.health()
	for _, word := range a.Health.Test {
		if !strings.Contains(hc, word) {
			bad("%s: the healthcheck does not run %q, which addons/%s declares", at, word, r.need.Type)
		}
	}
	if a.Health.StartPeriod != "" && (db.Healthcheck == nil || db.Healthcheck.StartPeriod != a.Health.StartPeriod) {
		bad("%s: no start_period: %s", at, a.Health.StartPeriod)
	}
	for _, name := range a.Env {
		if shared {
			break // each client's values are set by the platform, not by this service
		}
		if _, set := db.Environment[name]; !set {
			bad("%s: the environment does not set %s, which addons/%s says the container reads", at, name, r.need.Type)
		}
	}
	for name, value := range a.Fixed {
		if got := db.Environment[name]; got == nil || *got != value {
			bad("%s: %s is not %q, which addons/%s fixes", at, name, value, r.need.Type)
		}
	}
	if dep, ok := app.DependsOn[r.svc]; !ok || dep.Condition != "service_healthy" {
		bad("compose services.%s: nothing waits on %s with `depends_on:` and `condition: service_healthy` — the application would start against a database still recovering", m.Name, r.svc)
	}
	if !m.reaches(r.svc) {
		bad("%s: no variable the manifest fixes names %s, so nothing says the application reaches the database it declares", at, r.svc)
	}
	return p
}
