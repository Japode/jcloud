// Per-client copies of one service (JCT9).
//
// A platform may render one product once per client from a registry: Viglet
// Cloud's scripts/gen-clients writes a compose service, an nginx server block and
// a Keycloak client for every product each client has. That is a renderer, which
// the Japode platform refused (JC34) and that platform chose, and its own `--check`
// already proves the rendered files match the registry.
//
// What no generator check can see is its template drifting from the product: the
// port, the healthcheck, the image variable and the proxy headers are written once
// in the generator and once for the shared service, and nothing compares the two.
// So a manifest names its copies by template (`instances:`), and agree finds every
// one of them in the compose model (JCT4) and, given --routes, in the edge's
// configuration (JCT3), and holds each to the product's own facts.
//
// The registry is never read. The copies are what the model and the edge carry, so
// those are where they are found, and a client in the registry the generator did
// not render is the generator's own check to report. Nor is anything compared that
// is meant to differ between copies: the host, the client key in variables, the
// volume names the generator chooses. Each variable the manifest declares has to be
// set, and its value is the copy's own.
package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var clientKeyRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// instanceNames finds this manifest's copies in the model: every service the
// template matches whose {client} is a key, under the instance profile when one is
// declared, and not itself another manifest's service (turing-demo is not a client
// called demo when it has a manifest of its own).
func (m *Manifest) instanceNames(model *composeModel, others []*Manifest) map[string]string {
	in := m.Instances
	before, after, _ := strings.Cut(in.Service, "{client}")
	owned := map[string]bool{}
	for _, o := range others {
		owned[o.Name] = true
	}
	out := map[string]string{} // service -> client
	for name, svc := range model.Services {
		if owned[name] || len(name) <= len(before)+len(after) || !strings.HasPrefix(name, before) || !strings.HasSuffix(name, after) {
			continue
		}
		client := name[len(before) : len(name)-len(after)]
		if !clientKeyRE.MatchString(client) {
			continue
		}
		if in.Profile != "" && !contains(svc.Profiles, strings.ReplaceAll(in.Profile, "{client}", client)) {
			continue
		}
		out[name] = client
	}
	return out
}

// agreeInstances holds every copy to the product. byHost is the edge's routes, or
// nil when they were not supplied — the product's own route check has already
// said so.
func (m *Manifest) agreeInstances(model *composeModel, byHost map[string]*proxy, needs []resolvedNeed, prof *Profile, others []*Manifest) (problems, notes []string) {
	found := m.instanceNames(model, others)
	if len(found) == 0 {
		return nil, []string{fmt.Sprintf("instances: no compose service matches %s — no client has a copy yet, or the template is not the generator's", m.Instances.Service)}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		client := found[name]
		svc := model.Services[name]
		at := fmt.Sprintf("compose services.%s (client %s)", name, client)
		bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

		if want := prof.Container(name); svc.ContainerName != want {
			bad("%s: container_name is %q and not %s", at, svc.ContainerName, want)
		}
		if !svc.listens(m.Port) {
			bad("%s: nothing exposes port %d, which %s declares", at, m.Port, m.Name)
		}
		if m.Build == "" {
			if imageVar := prof.ImageVarOf(m); !strings.Contains(svc.Image, "${"+imageVar) {
				bad("%s: the image is %q and not ${%s}, so this copy does not run the version the product is pinned at", at, svc.Image, imageVar)
			}
		}
		if svc.Healthcheck == nil {
			bad("%s: no healthcheck:, so nothing waits on this copy", at)
		} else {
			hc := svc.health()
			// A tcp probe's path is "", which every healthcheck contains (JCT20).
			if !strings.Contains(hc, m.Health.Path) || !strings.Contains(hc, fmt.Sprint(m.Port)) {
				bad("%s: the healthcheck is %q and does not probe %s on port %d as %s does", at, hc, m.Health.Path, m.Port, m.Name)
			}
			if m.Health.StartPeriod != "" && svc.Healthcheck.StartPeriod != m.Health.StartPeriod {
				bad("%s: start_period is %q and not %s", at, svc.Healthcheck.StartPeriod, m.Health.StartPeriod)
			}
		}
		for _, e := range m.Env {
			if _, set := svc.Environment[e.Name]; !set {
				bad("%s: the environment does not set %s, which %s declares", at, e.Name, m.Name)
			}
		}
		for _, r := range needs {
			if r.need.Shared == "" {
				continue // a copy's own backing service is the generator's to name
			}
			if dep, ok := svc.DependsOn[r.svc]; !ok || dep.Condition != "service_healthy" {
				bad("%s: nothing waits on %s with `condition: service_healthy`", at, r.svc)
			}
		}

		// The copy's route is the product's first route at the copy's host,
		// proxying to the copy: the same assertions, against another server block.
		if m.Instances.Host != "" && byHost != nil && len(m.Routes) > 0 {
			copyOf := *m
			copyOf.Name = name
			route := m.Routes[0]
			route.Hosts = []string{strings.ReplaceAll(m.Instances.Host, "{client}", client)}
			copyOf.Routes = []Route{route}
			problems = append(problems, copyOf.agreeRouteJSON(fmt.Sprintf("route %s (client %s)", route.Hosts[0], client), byHost)...)
		}
	}
	return problems, notes
}
