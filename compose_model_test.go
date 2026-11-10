package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// JCT4: fixtureCompose as `docker compose --profile '*' config --no-interpolate
// --format json` prints it (the shape was taken from a real run), with a neighbour
// declared in the same model the way a platform with one shared compose file has
// it. The neighbour carries paste's facts, so a check that matched text anywhere
// in the file would pass on it.
const fixtureModel = `{
  "name": "platform",
  "services": {
    "paste": {
      "container_name": "japode-paste",
      "environment": {
        "DATABASE_PATH": "/data/paste.db",
        "SITE_URL": "${PASTE_SITE_URL:-https://paste.example.com}"
      },
      "expose": ["8080"],
      "healthcheck": {
        "start_period": "10s",
        "test": ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
      },
      "image": "${PASTE_IMAGE:?no image pinned}",
      "volumes": [{"source": "paste-data", "target": "/data", "type": "volume", "volume": {}}]
    },
    "neighbour": {
      "container_name": "japode-neighbour",
      "profiles": ["extra"],
      "environment": {"SITE_URL": "${PASTE_SITE_URL:-x}", "DATABASE_PATH": "/data/paste.db"},
      "expose": ["8081"],
      "healthcheck": {"start_period": "10s", "test": ["CMD", "wget", "http://127.0.0.1:8080/healthz"]},
      "image": "${PASTE_IMAGE}",
      "volumes": [{"source": "paste-data", "target": "/data", "type": "volume"}]
    }
  }
}`

// agreeModel lays the Japode fixture down without the per-service compose file —
// the model is what is compared, so the file is not needed — and agrees against
// the model with one substitution applied.
func agreeModel(t *testing.T, old, new string) []string {
	t.Helper()
	model := fixtureModel
	if old != "" {
		model = strings.Replace(fixtureModel, old, new, 1)
		if model == fixtureModel {
			t.Fatal("the patch changed nothing — the fixture moved under this case")
		}
	}
	root := write(t, "", "", "")
	if err := os.Remove(filepath.Join(root, "services", "paste", "docker-compose.yml")); err != nil {
		t.Fatal(err)
	}
	m, err := Parse([]byte(fixtureManifest))
	if err != nil {
		t.Fatal(err)
	}
	return m.AgreeFrom(root, AgreeInputs{Routes: []byte(fixtureAdapted), Compose: []byte(model)})
}

func TestModelFixtureAgreesWithItself(t *testing.T) {
	if p := agreeModel(t, "", ""); len(p) != 0 {
		t.Fatalf("the model fixture should agree, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// One case per fact, each read from paste's own block. The neighbour still carries
// the right value in every case, which is the hole a substring check had.
func TestModelDisagreements(t *testing.T) {
	cases := []struct{ name, old, new, want string }{
		{"another port", `"expose": ["8080"]`, `"expose": ["8090"]`, "nothing exposes port 8080"},
		{"image variable drifted", `"${PASTE_IMAGE:?no image pinned}"`, `"ghcr.io/x/paste:latest"`, "and not ${PASTE_IMAGE}"},
		{"container_name drifted", `"container_name": "japode-paste"`, `"container_name": "paste"`, "container_name is \"paste\" and not japode-paste"},
		{"healthcheck path", `"http://127.0.0.1:8080/healthz"]
      }`, `"http://127.0.0.1:8080/status"]
      }`, "does not probe /healthz"},
		{"start_period", `"start_period": "10s",
        "test": ["CMD", "wget", "-qO-"`, `"start_period": "30s",
        "test": ["CMD", "wget", "-qO-"`, "start_period is \"30s\" and not 10s"},
		{"no healthcheck", `"healthcheck": {
        "start_period"`, `"no_healthcheck": {
        "start_period"`, "no healthcheck:"},
		{"volume renamed", `"source": "paste-data", "target": "/data", "type": "volume", "volume": {}`, `"source": "paste-files", "target": "/data", "type": "volume", "volume": {}`, "nothing mounts paste-data at /data"},
		{"a variable is not set", `"SITE_URL": "${PASTE_SITE_URL:-https://paste.example.com}"`, `"OTHER": "1"`, "does not set SITE_URL"},
		{"a fixed value became configurable", `"DATABASE_PATH": "/data/paste.db",
        "SITE_URL"`, `"DATABASE_PATH": "${PASTE_DATABASE_PATH}",
        "SITE_URL"`, "DATABASE_PATH is not the fixed value"},
		{"a variable reads nothing", `"${PASTE_SITE_URL:-https://paste.example.com}"`, `"https://paste.example.com"`, "SITE_URL does not read ${PASTE_SITE_URL}"},
		{"under a profile the manifest does not declare", `"container_name": "japode-paste",`, `"container_name": "japode-paste", "profiles": ["demo"],`, "under profile(s) [demo] and the manifest declares none"},
		{"the service is not in the model", `"paste": {`, `"pastebin": {`, "compose services.paste: the compose model has no such service"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := strings.Join(agreeModel(t, c.old, c.new), "\n")
			if !strings.Contains(p, c.want) {
				t.Fatalf("want %q, got:\n%s", c.want, p)
			}
		})
	}
}

// A manifest declaring a profile is held to it.
func TestModelProfileDeclared(t *testing.T) {
	m, err := Parse([]byte(fixtureManifest + "profile: extra\n"))
	if err != nil {
		t.Fatal(err)
	}
	model, err := parseComposeModel([]byte(fixtureModel))
	if err != nil {
		t.Fatal(err)
	}
	p := strings.Join(m.agreeComposeModel(model, nil, DefaultProfile()), "\n")
	if !strings.Contains(p, "is not under profile extra") {
		t.Fatalf("got:\n%s", p)
	}

	m.Name = "neighbour"
	model.Services["neighbour"] = model.Services["paste"]
	svc := model.Services["neighbour"]
	svc.Profiles, svc.ContainerName = []string{"extra"}, "japode-neighbour"
	svc.Image = "${NEIGHBOUR_IMAGE}"
	svc.Environment = map[string]*string{"SITE_URL": ptr("${NEIGHBOUR_SITE_URL}"), "DATABASE_PATH": ptr("/data/paste.db")}
	svc.Volumes[0].Source = "neighbour-data"
	model.Services["neighbour"] = svc
	if p := m.agreeComposeModel(model, nil, DefaultProfile()); len(p) != 0 {
		t.Fatalf("a service under its declared profile should agree, got:\n  %s", strings.Join(p, "\n  "))
	}

	if p := (&Manifest{Profile: "no spaces"}).Validate(); !strings.Contains(strings.Join(p, "\n"), "not a compose profile name") {
		t.Fatalf("a bad profile name should be refused, got:\n%s", strings.Join(p, "\n"))
	}
}

func ptr(s string) *string { return &s }

// The needs half against the model: the database is its own service in it.
const needyModel = `{"services": {
  "paste-db": {
    "container_name": "japode-paste-db",
    "environment": {"MARIADB_DATABASE": "paste", "MARIADB_USER": "paste", "MARIADB_PASSWORD": "${PASTE_DB_PASSWORD:-}", "MARIADB_RANDOM_ROOT_PASSWORD": "yes"},
    "healthcheck": {"start_period": "20s", "test": ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]},
    "image": "mariadb:11.4@sha256:0000000000000000000000000000000000000000000000000000000000000000",
    "volumes": [{"source": "paste-db", "target": "/var/lib/mysql", "type": "volume"}]
  },
  "paste": {
    "container_name": "japode-paste",
    "depends_on": {"paste-db": {"condition": "service_healthy", "required": true}},
    "environment": {"DB_HOST": "paste-db:3306", "DB_NAME": "paste", "DB_PASSWORD": "${PASTE_DB_PASSWORD:-}"},
    "expose": ["8080"],
    "healthcheck": {"start_period": "10s", "test": ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]},
    "image": "${PASTE_IMAGE:?no image pinned}",
    "volumes": [{"source": "paste-data", "target": "/data", "type": "volume"}]
  }
}}`

func agreeNeedyModel(t *testing.T, old, new string) []string {
	t.Helper()
	model := strings.Replace(needyModel, old, new, 1)
	if old != "" && model == needyModel {
		t.Fatal("the patch changed nothing — the fixture moved under this case")
	}
	root := writeNeedy(t, "", "", "")
	m, err := Parse([]byte(needyManifest))
	if err != nil {
		t.Fatal(err)
	}
	return m.AgreeFrom(root, AgreeInputs{Routes: []byte(fixtureAdapted), Compose: []byte(model)})
}

func TestNeedyModel(t *testing.T) {
	if p := agreeNeedyModel(t, "", ""); len(p) != 0 {
		t.Fatalf("the needy model should agree, got:\n  %s", strings.Join(p, "\n  "))
	}
	for _, c := range []struct{ name, old, new, want string }{
		{"the db runs another image", `"mariadb:11.4@sha256:0000`, `"mariadb:11.5@sha256:0000`, "which addons/mariadb pins"},
		{"nothing waits on it", `"condition": "service_healthy"`, `"condition": "service_started"`, "nothing waits on paste-db"},
		{"the root password is kept", `"MARIADB_RANDOM_ROOT_PASSWORD": "yes"`, `"MARIADB_RANDOM_ROOT_PASSWORD": "no"`, "MARIADB_RANDOM_ROOT_PASSWORD is not \"yes\""},
		{"its volume went", `"source": "paste-db"`, `"source": "paste-mysql"`, "nothing mounts paste-db at /var/lib/mysql"},
		{"the db is not in the model", `"paste-db": {
    "container_name"`, `"paste-sql": {
    "container_name"`, "compose services.paste-db: the compose model has no such service"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := strings.Join(agreeNeedyModel(t, c.old, c.new), "\n")
			if !strings.Contains(p, c.want) {
				t.Fatalf("want %q, got:\n%s", c.want, p)
			}
		})
	}
}

// JCT21: compose keeps an environment value written unquoted as a JSON number or
// boolean. The model still decodes, every other fact is still compared, and each
// value reads as compose hands it to the container.
func TestModelEnvironmentHoldsScalars(t *testing.T) {
	scalars := `"DATABASE_PATH": "/data/paste.db",
        "PORT": 2700, "RATIO": 1.50, "DEBUG": true, "QUIET": false, "UNSET": null,`
	if p := agreeModel(t, `"DATABASE_PATH": "/data/paste.db",`, scalars); len(p) != 0 {
		t.Fatalf("a model with numbers and booleans in its environment should agree, got:\n  %s", strings.Join(p, "\n  "))
	}
	if p := strings.Join(agreeModel(t, `"expose": ["8080"]`, `"expose": ["8090"], "environment": {"PORT": 2700}`), "\n"); !strings.Contains(p, "nothing exposes port 8080") {
		t.Fatalf("the rest of the model is still compared, got:\n%s", p)
	}

	model, err := parseComposeModel([]byte(strings.Replace(fixtureModel, `"DATABASE_PATH": "/data/paste.db",`, scalars, 1)))
	if err != nil {
		t.Fatal(err)
	}
	env := model.Services["paste"].Environment
	for name, want := range map[string]string{"PORT": "2700", "RATIO": "1.50", "DEBUG": "true", "QUIET": "false", "DATABASE_PATH": "/data/paste.db"} {
		if got := env[name]; got == nil || *got != want {
			t.Errorf("%s: want %q, got %v", name, want, got)
		}
	}
	if got, set := env["UNSET"]; !set || got != nil {
		t.Errorf("UNSET: a null value is set without one, got %v (set %v)", got, set)
	}

	if _, err := parseComposeModel([]byte(`{"services": {"x": {"environment": {"A": [1]}}}}`)); err == nil || !strings.Contains(err.Error(), "environment A") {
		t.Fatalf("a value that is not a scalar names the variable, got %v", err)
	}
}

func TestAMalformedModelIsReported(t *testing.T) {
	for _, bad := range []string{"not json", `{"name": "x"}`} {
		root := write(t, "", "", "")
		m, err := Parse([]byte(fixtureManifest))
		if err != nil {
			t.Fatal(err)
		}
		p := strings.Join(m.AgreeFrom(root, AgreeInputs{Routes: []byte(fixtureAdapted), Compose: []byte(bad)}), "\n")
		if !strings.Contains(p, "not a `docker compose config --format json` output") {
			t.Errorf("%q: got:\n%s", bad, p)
		}
	}
}

// survey: a service whose profile the collector did not report on is opt-in and
// off, not missing. Reported on, it is held to running like any other.
func TestSurveyLeavesAnOffProfileAlone(t *testing.T) {
	d := Declared{Services: []string{"demo", "paste"}, Profile: map[string]string{"demo": "demo"}}
	off := SurveyAgainst(d, ParseObserved("container paste healthy\n"))
	if len(off) != 0 {
		t.Fatalf("a service under a profile nobody switched on is not drift, got %v", off)
	}
	on := SurveyAgainst(d, ParseObserved("container paste healthy\nprofile demo on\n"))
	if len(on) != 1 || on[0].Service != "demo" || !strings.Contains(on[0].Says, "not running") {
		t.Fatalf("with its profile on, a missing service is reported, got %v", on)
	}
	up := SurveyAgainst(d, ParseObserved("container paste healthy\ncontainer demo unhealthy\n"))
	if len(up) != 1 || !strings.Contains(up[0].Says, "not healthy") {
		t.Fatalf("a running profiled service is still held to its health, got %v", up)
	}
}
