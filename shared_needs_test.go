package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// JCT5: the needy fixture with its database moved to the platform. One mariadb
// that several products reach by name, under a container and volume name the
// platform chose, holding a database per product the platform created.
var sharedManifest = strings.NewReplacer(
	"value: paste-db:3306", "value: mariadb:3306",
	"    type: mariadb\n", "    type: mariadb\n    shared: mariadb\n",
).Replace(needyManifest)

const sharedModel = `{"services": {
  "mariadb": {
    "container_name": "platform-mariadb",
    "environment": {"MARIADB_RANDOM_ROOT_PASSWORD": "yes"},
    "healthcheck": {"start_period": "20s", "test": ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]},
    "image": "mariadb:11.4@sha256:0000000000000000000000000000000000000000000000000000000000000000",
    "volumes": [{"source": "platform-mysql", "target": "/var/lib/mysql", "type": "volume"}]
  },
  "paste": {
    "container_name": "japode-paste",
    "depends_on": {"mariadb": {"condition": "service_healthy", "required": true}},
    "environment": {"DB_HOST": "mariadb:3306", "DB_NAME": "paste", "DB_PASSWORD": "${PASTE_DB_PASSWORD:-}"},
    "expose": ["8080"],
    "healthcheck": {"start_period": "10s", "test": ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]},
    "image": "${PASTE_IMAGE:?no image pinned}",
    "volumes": [{"source": "paste-data", "target": "/data", "type": "volume"}]
  }
}}`

// sharedTree has no backup.conf: the service's own volume needs no directive, and
// the shared database's is the platform's to back up.
func sharedTree(t *testing.T) string {
	t.Helper()
	return layTree(t, map[string]string{
		"services/paste/japode.yml": sharedManifest,
		"caddy/sites/paste.caddy":   fixtureRoute,
		".env.example":              needyEnvExample,
		"addons/mariadb/addon.yml":  needyAddon,
	})
}

func agreeShared(t *testing.T, old, new string) []string {
	t.Helper()
	model := strings.Replace(sharedModel, old, new, 1)
	if old != "" && model == sharedModel {
		t.Fatal("the patch changed nothing — the fixture moved under this case")
	}
	m, err := Parse([]byte(sharedManifest))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Validate(); len(p) != 0 {
		t.Fatalf("the shared manifest is not valid:\n  %s", strings.Join(p, "\n  "))
	}
	return m.AgreeFrom(sharedTree(t), AgreeInputs{Routes: []byte(fixtureAdapted), Compose: []byte(model)})
}

// The container and volume names are the platform's, the per-client variables are
// set inside the shared server by the platform, and no backup.conf is asked for.
func TestASharedNeedAgrees(t *testing.T) {
	if p := agreeShared(t, "", ""); len(p) != 0 {
		t.Fatalf("the shared fixture should agree, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// What the addon pins still holds the shared service, and the client still has to
// wait on it and reach it.
func TestSharedNeedDisagreements(t *testing.T) {
	for _, c := range []struct{ name, old, new, want string }{
		{"the platform runs another image", `"mariadb:11.4@sha256:0000`, `"mariadb:11.5@sha256:0000`, "which addons/mariadb pins"},
		{"its data is in the container", `"target": "/var/lib/mysql"`, `"target": "/tmp/mysql"`, "no volume is mounted at /var/lib/mysql"},
		{"its healthcheck is not the addon's", `"--innodb_initialized"]`, `"--su-mysql"]`, "does not run \"--innodb_initialized\""},
		{"a fixed value drifted", `"MARIADB_RANDOM_ROOT_PASSWORD": "yes"`, `"MARIADB_ROOT_PASSWORD": "x"`, "MARIADB_RANDOM_ROOT_PASSWORD is not \"yes\""},
		{"the client does not wait on it", `"condition": "service_healthy"`, `"condition": "service_started"`, "nothing waits on mariadb"},
		{"the platform does not run it", `"mariadb": {
    "container_name"`, `"mysql": {
    "container_name"`, "compose services.mariadb: the compose model has no such service, which needs[db].shared names"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := strings.Join(agreeShared(t, c.old, c.new), "\n")
			if !strings.Contains(p, c.want) {
				t.Fatalf("want %q, got:\n%s", c.want, p)
			}
		})
	}

	// And the application dialling its old sidecar instead.
	m, err := Parse([]byte(strings.Replace(sharedManifest, "value: mariadb:3306", "value: paste-db:3306", 1)))
	if err != nil {
		t.Fatal(err)
	}
	p := strings.Join(m.AgreeFrom(sharedTree(t), AgreeInputs{Routes: []byte(fixtureAdapted), Compose: []byte(sharedModel)}), "\n")
	if !strings.Contains(p, "no variable the manifest fixes names mariadb") {
		t.Fatalf("got:\n%s", p)
	}
}

// The service's own compose file cannot show a service the platform declares
// elsewhere, so without the model the shared need is said to be unchecked.
func TestASharedNeedWithoutTheModelIsSaid(t *testing.T) {
	root := sharedTree(t)
	compose := strings.Replace(needyCompose, "paste-db:\n        condition", "mariadb:\n        condition", 1)
	if err := os.WriteFile(filepath.Join(root, "services", "paste", "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Parse([]byte(sharedManifest))
	if err != nil {
		t.Fatal(err)
	}
	p := strings.Join(m.AgreeWith(root, []byte(fixtureAdapted)), "\n")
	if !strings.Contains(p, "needs[db] is shared with mariadb, which only the compose model can show — pass --compose") {
		t.Fatalf("got:\n%s", p)
	}
}

func TestSharedNeedValidation(t *testing.T) {
	for _, c := range []struct{ shared, want string }{
		{"Not_A_Name", "is not a short name"},
		{"paste", "is this service itself"},
	} {
		m, err := Parse([]byte(strings.Replace(sharedManifest, "shared: mariadb", "shared: "+c.shared, 1)))
		if err != nil {
			t.Fatal(err)
		}
		if p := strings.Join(m.Validate(), "\n"); !strings.Contains(p, c.want) {
			t.Errorf("shared: %s: want %q, got:\n%s", c.shared, c.want, p)
		}
	}
	// A shared need derives no volume, so its name may also be a volume's.
	m, err := Parse([]byte(strings.Replace(sharedManifest, "  - name: db\n", "  - name: data\n", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Validate(); len(p) != 0 {
		t.Fatalf("got:\n  %s", strings.Join(p, "\n  "))
	}
}

// survey counts the shared service once, however many manifests name it and
// whether or not the top-level compose file declares it too.
func TestSurveyCountsASharedServiceOnce(t *testing.T) {
	other := strings.NewReplacer("name: paste", "name: notes", "paste.example.com", "notes.example.com").Replace(sharedManifest)
	root := layTree(t, map[string]string{
		"docker-compose.yml":        "services:\n  caddy: {}\n  mariadb: {}\n",
		"services/paste/japode.yml": sharedManifest,
		"services/notes/japode.yml": other,
	})
	d, err := DeclaredIn(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"caddy", "mariadb"}; !reflect.DeepEqual(d.Others, want) {
		t.Fatalf("got %v, want %v", d.Others, want)
	}
}
