package main

import (
	"strings"
	"testing"
)

// JCT18: a route states the authorisation subrequest its edge makes, and agree
// compares it both ways.

var gatedManifest = strings.Replace(fixtureManifest, "    max_body: 4MB\n", "    max_body: 4MB\n    gate: /oauth2/auth\n", 1)

// The nginx fixture with the gate on its catch-all location.
var gatedNginx = strings.Replace(fixtureNginx, "        set $paste_upstream paste;", "        auth_request /oauth2/auth;\n        set $paste_upstream paste;", 1)

func gateIn(t *testing.T, manifest, conf string) string {
	t.Helper()
	m, err := Parse([]byte(strings.Replace(manifest, "max_body: 4MB", "max_body: 4MiB", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Validate(); len(p) != 0 {
		t.Fatalf("not valid:\n  %s", strings.Join(p, "\n  "))
	}
	return strings.Join(m.AgreeWith(nginxTree(t), []byte(conf)), "\n")
}

func TestNginxGateBothWays(t *testing.T) {
	if p := gateIn(t, gatedManifest, gatedNginx); p != "" {
		t.Fatalf("a declared gate the route runs agrees, got:\n%s", p)
	}
	if p := gateIn(t, gatedManifest, fixtureNginx); !strings.Contains(p, "declares gate: /oauth2/auth and the route runs none, so it is open to anyone") {
		t.Fatalf("got:\n%s", p)
	}
	if p := gateIn(t, fixtureManifest, gatedNginx); !strings.Contains(p, "gates every request through /oauth2/auth and the manifest states no gate") {
		t.Fatalf("got:\n%s", p)
	}
	other := strings.Replace(gatedNginx, "auth_request /oauth2/auth;", "auth_request /_client_gate;", 1)
	if p := gateIn(t, gatedManifest, other); !strings.Contains(p, "gates through /_client_gate and the manifest declares gate: /oauth2/auth") {
		t.Fatalf("got:\n%s", p)
	}
}

// A gate set on the server is inherited by its location, as nginx inherits it.
func TestNginxGateIsInherited(t *testing.T) {
	routes, err := parseNginxRoutes([]byte("server { server_name a.example; auth_request /g; location / { proxy_pass http://a:1; } }\nserver { server_name b.example; auth_request /g; location / { auth_request off; proxy_pass http://b:1; } }"))
	if err != nil {
		t.Fatal(err)
	}
	if routes["a.example"].Gate != "/g" || routes["b.example"].Gate != "" {
		t.Fatalf("a inherits /g, b switches it off: %q, %q", routes["a.example"].Gate, routes["b.example"].Gate)
	}
}

// Caddy's forward_auth, in the shape `caddy adapt` gives it (taken from a real
// run): a reverse_proxy of its own, rewritten to the gate's URI. Its upstream and
// its headers are the gate's and must not be read as the route's.
const gatedAdapted = `{"apps":{"http":{"servers":{"srv0":{"routes":[
 {"match":[{"host":["paste.example.com"]}],
  "handle":[{"handler":"subroute","routes":[{"handle":[
    {"handler":"reverse_proxy",
     "handle_response":[{"match":{"status_code":[2]},"routes":[{"handle":[{"handler":"vars"}]}]}],
     "headers":{"request":{"set":{"X-Forwarded-Method":["{http.request.method}"],"X-Forwarded-Uri":["{http.request.uri}"]}}},
     "rewrite":{"method":"GET","uri":"/oauth2/auth"},
     "upstreams":[{"dial":"gate:8080"}]},
    {"handler":"request_body","max_size":4000000},
    {"handler":"reverse_proxy",
     "headers":{"request":{"set":{"X-Forwarded-For":["{http.request.remote.host}"]},"delete":["X-View-Id"]}},
     "health_checks":{"active":{"uri":"/healthz","interval":15000000000}},
     "upstreams":[{"dial":"paste:8080"}]}
  ]}]}]}
]}}}}}`

func TestCaddyForwardAuthIsTheGate(t *testing.T) {
	m, err := Parse([]byte(gatedManifest))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.AgreeWith(write(t, "", "", ""), []byte(gatedAdapted)); len(p) != 0 {
		t.Fatalf("forward_auth to the declared gate agrees, and its upstream and headers are not the route's; got:\n  %s", strings.Join(p, "\n  "))
	}
	plain, _ := Parse([]byte(fixtureManifest))
	if p := strings.Join(plain.AgreeWith(write(t, "", "", ""), []byte(gatedAdapted)), "\n"); !strings.Contains(p, "gates every request through /oauth2/auth") {
		t.Fatalf("got:\n%s", p)
	}
}

func TestAGateIsAPath(t *testing.T) {
	m, err := Parse([]byte(strings.Replace(gatedManifest, "gate: /oauth2/auth", "gate: oauth2-proxy:4180", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if p := strings.Join(m.Validate(), "\n"); !strings.Contains(p, "routes[0].gate") {
		t.Fatalf("got:\n%s", p)
	}
}
