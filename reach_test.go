package main

import (
	"strings"
	"testing"
)

// JCT20: a service reached over a user network by its name, which declares no
// expose: and no ports:, and probes itself with a TCP connect and no path — the
// shape every Viglet Cloud product has.
var tcpManifest = strings.NewReplacer(
	"  path: /healthz\n  probe: wget\n", "  probe: tcp\n",
	"max_body: 4MB", "max_body: 4MiB",
).Replace(fixtureManifest)

var tcpModel = strings.NewReplacer(
	`"expose": ["8080"],
      "healthcheck": {
        "start_period": "10s",
        "test": ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
      },`,
	`"healthcheck": {
        "start_period": "10s",
        "test": ["CMD-SHELL", "bash -c '</dev/tcp/localhost/8080' 2>/dev/null || exit 1"]
      },`,
).Replace(fixtureModel)

func TestATCPProbeOnAUserNetworkAgrees(t *testing.T) {
	if tcpModel == fixtureModel {
		t.Fatal("the patch changed nothing — the fixture moved under this case")
	}
	m, err := Parse([]byte(tcpManifest))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Validate(); len(p) != 0 {
		t.Fatalf("a tcp probe needs no path:\n  %s", strings.Join(p, "\n  "))
	}
	if p := m.AgreeFrom(nginxTree(t), AgreeInputs{Routes: []byte(fixtureNginx), Compose: []byte(tcpModel)}); len(p) != 0 {
		t.Fatalf("got:\n  %s", strings.Join(p, "\n  "))
	}

	// The healthcheck still has to name the port: it is the statement now.
	wrong := strings.Replace(tcpModel, "/dev/tcp/localhost/8080", "/dev/tcp/localhost/9090", 1)
	p := strings.Join(m.AgreeFrom(nginxTree(t), AgreeInputs{Routes: []byte(fixtureNginx), Compose: []byte(wrong)}), "\n")
	if !strings.Contains(p, "nothing exposes port 8080") || !strings.Contains(p, "does not probe port 8080") {
		t.Fatalf("got:\n%s", p)
	}
}

// Japode's reading is not loosened: a service that declares an expose: still has
// to list the manifest's port there, whatever its healthcheck says.
func TestADeclaredExposeStillHasToListThePort(t *testing.T) {
	m, err := Parse([]byte(fixtureManifest))
	if err != nil {
		t.Fatal(err)
	}
	model, err := parseComposeModel([]byte(strings.Replace(fixtureModel, `"expose": ["8080"]`, `"expose": ["9000"]`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if p := strings.Join(m.agreeComposeModel(model, nil, DefaultProfile()), "\n"); !strings.Contains(p, "nothing exposes port 8080") {
		t.Fatalf("got:\n%s", p)
	}
}

// A Caddy edge probes the upstream with health_uri, which asks a path.
func TestATCPProbeBehindCaddyStillNeedsAPath(t *testing.T) {
	m, err := Parse([]byte(tcpManifest))
	if err != nil {
		t.Fatal(err)
	}
	if p := strings.Join(m.AgreeWith(write(t, "", "", ""), []byte(fixtureAdapted)), "\n"); !strings.Contains(p, "a Caddy edge's health_uri does") {
		t.Fatalf("got:\n%s", p)
	}
}

func TestOnlyATCPProbeGoesWithoutAPath(t *testing.T) {
	m, err := Parse([]byte(strings.Replace(tcpManifest, "probe: tcp", "probe: wget", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if p := strings.Join(m.Validate(), "\n"); !strings.Contains(p, "health.path: is missing") {
		t.Fatalf("got:\n%s", p)
	}
	odd, _ := Parse([]byte(strings.Replace(tcpManifest, "probe: tcp", "probe: telnet", 1)))
	if p := strings.Join(odd.Validate(), "\n"); !strings.Contains(p, "not one of node, curl, wget, tcp") {
		t.Fatalf("got:\n%s", p)
	}
}
