package main

import (
	"fmt"
	"strings"
	"testing"
)

// JCT9: the Japode fixture with two per-client copies rendered by a generator, and
// a third service that matches the name template but is not a copy: it is under
// another profile, the way turing-demo is beside turing-<client>.
var instancedManifest = fixtureManifest + `instances:
  service: paste-{client}
  host: paste-{client}.example.com
  profile: client-{client}
`

// copyService is paste as a generator renders it for one client: its own
// container, profile, host-bearing variable and volume, and the product's port,
// image variable and healthcheck.
func copyService(client, profile string) string {
	return fmt.Sprintf(`"paste-%[1]s": {
      "container_name": "japode-paste-%[1]s",
      "profiles": [%[2]q],
      "environment": {"DATABASE_PATH": "/data/paste.db", "SITE_URL": "https://paste-%[1]s.example.com"},
      "expose": ["8080"],
      "healthcheck": {"start_period": "10s", "test": ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]},
      "image": "${PASTE_IMAGE:-ghcr.io/x/paste:latest}",
      "volumes": [{"source": "paste_%[1]s_data", "target": "/data", "type": "volume"}]
    }`, client, profile)
}

var instancedModel = strings.Replace(fixtureModel, `"neighbour": {`,
	copyService("acme", "client-acme")+",\n    "+copyService("globex", "client-globex")+",\n    "+
		strings.Replace(copyService("demo", "demo"), `"expose": ["8080"]`, `"expose": ["1"]`, 1)+`,
    "neighbour": {`, 1)

// The adapted Caddy config with one route per copy, each proxying to its copy.
func instancedRoutes(dial map[string]string) string {
	route := func(host, upstream string) string {
		return strings.NewReplacer("paste.example.com", host, "paste:8080", upstream).Replace(
			`{"match":[{"host":["paste.example.com"]}],
  "handle":[{"handler":"subroute","routes":[{"handle":[
    {"handler":"request_body","max_size":4000000},
    {"handler":"reverse_proxy",
     "headers":{"request":{"set":{"X-Forwarded-For":["{http.request.remote.host}"]},"delete":["X-View-Id"]}},
     "health_checks":{"active":{"uri":"/healthz","interval":15000000000}},
     "upstreams":[{"dial":"paste:8080"}]}
  ]}]}]}`)
	}
	routes := []string{route("paste.example.com", "paste:8080")}
	for _, client := range []string{"acme", "globex"} {
		upstream := "paste-" + client + ":8080"
		if d, ok := dial[client]; ok {
			upstream = d
		}
		routes = append(routes, route("paste-"+client+".example.com", upstream))
	}
	return `{"apps":{"http":{"servers":{"srv0":{"routes":[` + strings.Join(routes, ",") + `]}}}}}`
}

func agreeInstanced(t *testing.T, old, new string, dial map[string]string) ([]string, []string) {
	t.Helper()
	model := instancedModel
	if old != "" {
		model = strings.Replace(instancedModel, old, new, 1)
		if model == instancedModel {
			t.Fatal("the patch changed nothing — the fixture moved under this case")
		}
	}
	m, err := Parse([]byte(instancedManifest))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Validate(); len(p) != 0 {
		t.Fatalf("the instanced manifest is not valid:\n  %s", strings.Join(p, "\n  "))
	}
	return m.AgreeReport(write(t, "", "", ""), AgreeInputs{Routes: []byte(instancedRoutes(dial)), Compose: []byte(model)})
}

func TestEveryCopyAgreesWithTheProduct(t *testing.T) {
	problems, notes := agreeInstanced(t, "", "", nil)
	if len(problems) != 0 || len(notes) != 0 {
		t.Fatalf("got problems:\n  %s\nnotes:\n  %s", strings.Join(problems, "\n  "), strings.Join(notes, "\n  "))
	}
}

// Each copy is held to the product's fixed facts, and only those: its host, its
// variable values and its volume names are its own.
func TestACopyThatDriftedFromTheProduct(t *testing.T) {
	acme := `"container_name": "japode-paste-acme"`
	for _, c := range []struct{ name, old, new, want string }{
		{"another port", acme + `,
      "profiles": ["client-acme"],
      "environment": {"DATABASE_PATH": "/data/paste.db", "SITE_URL": "https://paste-acme.example.com"},
      "expose": ["8080"]`, acme + `,
      "profiles": ["client-acme"],
      "environment": {"DATABASE_PATH": "/data/paste.db", "SITE_URL": "https://paste-acme.example.com"},
      "expose": ["8090"]`, "compose services.paste-acme (client acme): nothing exposes port 8080"},
		{"a literal image", `"${PASTE_IMAGE:-ghcr.io/x/paste:latest}",
      "volumes": [{"source": "paste_acme_data"`, `"ghcr.io/x/paste:1.0",
      "volumes": [{"source": "paste_acme_data"`, "does not run the version the product is pinned at"},
		{"a variable the generator forgot", `{"DATABASE_PATH": "/data/paste.db", "SITE_URL": "https://paste-acme.example.com"}`, `{"SITE_URL": "https://paste-acme.example.com"}`, "(client acme): the environment does not set DATABASE_PATH"},
		{"another container name", acme, `"container_name": "acme-paste"`, "container_name is \"acme-paste\" and not japode-paste-acme"},
	} {
		t.Run(c.name, func(t *testing.T) {
			problems, _ := agreeInstanced(t, c.old, c.new, nil)
			if p := strings.Join(problems, "\n"); !strings.Contains(p, c.want) {
				t.Fatalf("want %q, got:\n%s", c.want, p)
			}
		})
	}
}

// The copy's route is the product's, at the copy's host, proxying to the copy.
func TestACopysRouteIsTheProducts(t *testing.T) {
	problems, _ := agreeInstanced(t, "", "", map[string]string{"globex": "paste:8080"})
	if p := strings.Join(problems, "\n"); !strings.Contains(p, "route paste-globex.example.com (client globex)") || !strings.Contains(p, "proxies to [paste:8080], and the manifest declares paste-globex:8080") {
		t.Fatalf("got:\n%s", p)
	}
}

func TestNoCopyIsANoteAndNoModelIsANote(t *testing.T) {
	m, err := Parse([]byte(strings.Replace(instancedManifest, "paste-{client}\n", "notes-{client}\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	root := write(t, "", "", "")
	_, notes := m.AgreeReport(root, AgreeInputs{Routes: []byte(instancedRoutes(nil)), Compose: []byte(instancedModel)})
	if n := strings.Join(notes, "\n"); !strings.Contains(n, "no compose service matches notes-{client}") {
		t.Fatalf("got:\n%s", n)
	}
	_, notes = m.AgreeReport(root, AgreeInputs{Routes: []byte(fixtureAdapted)})
	if n := strings.Join(notes, "\n"); !strings.Contains(n, "--compose was not given, so none was checked") {
		t.Fatalf("got:\n%s", n)
	}
}

// Another manifest's service that happens to match the template is that
// manifest's, not a copy — even with no profile template to tell them apart.
func TestASiblingIsNotACopy(t *testing.T) {
	m, err := Parse([]byte(strings.Replace(instancedManifest, "  profile: client-{client}\n", "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	model, err := parseComposeModel([]byte(instancedModel))
	if err != nil {
		t.Fatal(err)
	}
	found := m.instanceNames(model, []*Manifest{{Name: "paste-demo"}})
	if _, ok := found["paste-demo"]; ok || len(found) != 2 {
		t.Fatalf("got %v", found)
	}
}

func TestInstanceTemplatesAreValidated(t *testing.T) {
	for _, c := range []struct{ old, new, want string }{
		{"service: paste-{client}", "service: paste", "instances.service"},
		{"host: paste-{client}.example.com", "host: paste.example.com", "instances.host"},
		{"profile: client-{client}", "profile: clients", "instances.profile"},
	} {
		m, err := Parse([]byte(strings.Replace(instancedManifest, c.old, c.new, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if p := strings.Join(m.Validate(), "\n"); !strings.Contains(p, c.want) {
			t.Errorf("%s: got:\n%s", c.new, p)
		}
	}
}
