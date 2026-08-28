package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A minimal tree that agrees with itself, written to a temp directory so each case
// can break exactly one thing. Deliberately smaller than skillgist's: what is being
// tested is the comparison, and a fixture that is a copy of the real files would
// fail for reasons about the real files.
const fixtureManifest = `
version: 1
name: paste
image: ghcr.io/x/paste
port: 8080
health:
  path: /healthz
  probe: wget
  start_period: 10s
volumes:
  - name: data
    path: /data
    sqlite: [paste.db]
    exclude: [tmp]
routes:
  - hosts: [paste.example.com]
    max_body: 4MB
    proxy:
      - header_up X-Forwarded-For {remote_host}
      - header_up -X-View-Id
env:
  - name: SITE_URL
    default: https://paste.example.com
  - name: DATABASE_PATH
    value: /data/paste.db
`

const fixtureCompose = `services:
  paste:
    image: ${PASTE_IMAGE:?no image pinned}
    container_name: japode-paste
    environment:
      SITE_URL: ${PASTE_SITE_URL:-https://paste.example.com}
      DATABASE_PATH: /data/paste.db
    volumes:
      - paste-data:/data
    expose:
      - '8080'
    healthcheck:
      test: [CMD, wget, -qO-, 'http://127.0.0.1:8080/healthz']
      start_period: 10s
volumes:
  paste-data:
`

// The route's source. Only the {$PASTE_HOSTS:…} reference is asserted against this;
// what the route *means* is asserted against the adapted JSON below.
const fixtureRoute = `{$PASTE_HOSTS:paste.example.com} {
	request_body {
		max_size 4MB
	}
	reverse_proxy paste:8080 {
		header_up X-Forwarded-For {remote_host}
		header_up -X-View-Id
		health_uri /healthz
	}
}
`

// The shape `caddy adapt` produces, trimmed to what route_json.go reads. Written by
// hand rather than generated, because `go test` has no caddy — the real thing is
// adapted by scripts/check-jcloud.sh and asserted by TestCommittedManifestsAgree.
const fixtureAdapted = `{"apps":{"http":{"servers":{"srv0":{"routes":[
 {"match":[{"host":["paste.example.com"]}],
  "handle":[{"handler":"subroute","routes":[{"handle":[
    {"handler":"request_body","max_size":4000000},
    {"handler":"reverse_proxy",
     "headers":{"request":{"set":{"X-Forwarded-For":["{http.request.remote.host}"]},"delete":["X-View-Id"]}},
     "health_checks":{"active":{"uri":"/healthz","interval":15000000000}},
     "upstreams":[{"dial":"paste:8080"}]}
  ]}]}]}
]}}}}}`

const fixtureBackupConf = `# what backup.sh treats specially
sqlite paste-data paste.db
exclude paste-data tmp
`

const fixtureEnvExample = `PASTE_HOSTS=paste.example.com
PASTE_SITE_URL=https://paste.example.com
`

// write lays the fixture down and applies one substitution to one file, which is
// how each case below breaks exactly one fact.
func write(t *testing.T, file, old, new string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"services/paste/japode.yml":         fixtureManifest,
		"services/paste/docker-compose.yml": fixtureCompose,
		"caddy/sites/paste.caddy":           fixtureRoute,
		"services/paste/backup.conf":        fixtureBackupConf,
		".env.example":                      fixtureEnvExample,
	}
	if file != "" {
		body, ok := files[file]
		if !ok {
			t.Fatalf("no fixture called %s", file)
		}
		patched := strings.Replace(body, old, new, 1)
		if patched == body {
			t.Fatalf("the patch changed nothing in %s — the fixture moved under this case", file)
		}
		files[file] = patched
	}
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func agreeIn(t *testing.T, root, adapted string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "services", "paste", "japode.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p := m.Validate(); len(p) != 0 {
		t.Fatalf("the fixture manifest is not even valid:\n  %s", strings.Join(p, "\n  "))
	}
	return m.AgreeWith(root, []byte(adapted))
}

func TestFixtureAgreesWithItself(t *testing.T) {
	if p := agreeIn(t, write(t, "", "", ""), fixtureAdapted); len(p) != 0 {
		t.Fatalf("the fixture should agree with itself, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// Without the adapted configuration the route's meaning is unchecked, and that is
// said rather than passed over. This is the regression guard for JC33: the answer
// to "I could not look" must never be a tick.
func TestUnadaptedRouteIsReportedAndNotAssumed(t *testing.T) {
	p := strings.Join(agreeIn(t, write(t, "", "", ""), ""), "\n")
	if !strings.Contains(p, "what this route means is unchecked") {
		t.Fatalf("expected the missing adapted config to be reported, got:\n%s", p)
	}
}

// One case per fact that lives in more than one file. Each is a real mistake
// somebody makes: a port changed in one place, a volume renamed on one side, a
// variable added to compose and not to .env.example.
func TestDisagreements(t *testing.T) {
	cases := []struct {
		name, file, old, new, want string
	}{
		{"compose exposes another port", "services/paste/docker-compose.yml",
			"- '8080'", "- '8081'",
			"nothing exposes port 8080"},
		{"healthcheck probes another path", "services/paste/docker-compose.yml",
			"/healthz'", "/livez'",
			"does not probe /healthz"},
		{"healthcheck probes another port", "services/paste/docker-compose.yml",
			"127.0.0.1:8080", "127.0.0.1:9090",
			"does not probe port 8080"},
		{"start_period drifted", "services/paste/docker-compose.yml",
			"start_period: 10s", "start_period: 30s",
			"start_period is not 10s"},
		{"volume renamed in compose", "services/paste/docker-compose.yml",
			"- paste-data:/data", "- paste-store:/data",
			"nothing mounts paste-data at /data"},
		{"container_name drifted", "services/paste/docker-compose.yml",
			"container_name: japode-paste", "container_name: paste",
			"container_name is not japode-paste"},
		{"image variable drifted", "services/paste/docker-compose.yml",
			"${PASTE_IMAGE:?no image pinned}", "ghcr.io/x/paste:latest",
			"is not ${PASTE_IMAGE}"},
		{"a declared variable is not set", "services/paste/docker-compose.yml",
			"      SITE_URL: ${PASTE_SITE_URL:-https://paste.example.com}\n", "",
			"does not set SITE_URL"},
		{"a fixed value became configurable", "services/paste/docker-compose.yml",
			"DATABASE_PATH: /data/paste.db", "DATABASE_PATH: ${PASTE_DATABASE_PATH:-/data/paste.db}",
			"is not the fixed value"},
		{"hosts variable renamed", "caddy/sites/paste.caddy",
			"{$PASTE_HOSTS:", "{$PASTE_ADDRESSES:",
			"no address block reads {$PASTE_HOSTS:"},
		{"sqlite line went", "services/paste/backup.conf",
			"sqlite paste-data paste.db\n", "",
			"no `sqlite paste-data paste.db`"},
		{"exclude line went", "services/paste/backup.conf",
			"exclude paste-data tmp\n", "",
			"no `exclude paste-data tmp`"},
		{"backup.conf names something the manifest does not", "services/paste/backup.conf",
			"exclude paste-data tmp", "exclude paste-data cache",
			"is not declared by the manifest"},
		{"env.example is missing a key", ".env.example",
			"PASTE_SITE_URL=https://paste.example.com\n", "",
			"no PASTE_SITE_URL="},
		{"env.example is missing the hosts key", ".env.example",
			"PASTE_HOSTS=paste.example.com\n", "",
			"no PASTE_HOSTS="},
		{"env.example offers a fixed value", ".env.example",
			"PASTE_HOSTS=paste.example.com",
			"PASTE_HOSTS=paste.example.com\nPASTE_DATABASE_PATH=/data/paste.db",
			"it is not configurable"},
		{"env.example carries a key nothing declares", ".env.example",
			"PASTE_SITE_URL=https://paste.example.com",
			"PASTE_SITE_URL=https://paste.example.com\nPASTE_LEGACY_FLAG=",
			"PASTE_LEGACY_FLAG= is not declared by the manifest"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := strings.Join(agreeIn(t, write(t, c.file, c.old, c.new), fixtureAdapted), "\n")
			if !strings.Contains(problems, c.want) {
				t.Fatalf("expected a problem containing %q, got:\n%s", c.want, problems)
			}
		})
	}
}

// The route half, asked of what Caddy would run. Each case mutates the adapted
// configuration, which is what the Caddyfile *means* — the file's text is not
// touched at all, so none of these could be faked by a comment.
func TestRouteDisagreements(t *testing.T) {
	cases := []struct {
		name, old, new, want string
	}{
		{"the block is commented out, so nothing answers for the host",
			`{"match":[{"host":["paste.example.com"]}],`, `{"match":[{"host":["other.example.com"]}],`,
			"nothing in the adapted config answers for paste.example.com"},
		{"proxies to another port",
			`"dial":"paste:8080"`, `"dial":"paste:8081"`,
			"proxies to [paste:8081], and the manifest declares paste:8080"},
		{"proxies to another service",
			`"dial":"paste:8080"`, `"dial":"pastebin:8080"`,
			"and the manifest declares paste:8080"},
		{"health_uri drifted",
			`"uri":"/healthz"`, `"uri":"/livez"`,
			`health_uri is "/livez", and the manifest declares "/healthz"`},
		{"max_size drifted",
			`"max_size":4000000`, `"max_size":8000000`,
			"max_size is 8000000 bytes, and the manifest declares 4MB (4000000)"},
		{"the X-Forwarded-For override went",
			`"set":{"X-Forwarded-For":["{http.request.remote.host}"]},`, `"set":{},`,
			"does not set X-Forwarded-For"},
		{"X-Forwarded-For is set to something else",
			`["{http.request.remote.host}"]`, `["{http.request.header.X-Forwarded-For}"]`,
			"is set to [{http.request.header.X-Forwarded-For}]"},
		{"the X-View-Id strip went",
			`"delete":["X-View-Id"]`, `"delete":[]`,
			"does not strip X-View-Id"},
		{"nothing proxies at all",
			`"handler":"reverse_proxy"`, `"handler":"file_server"`,
			"proxies to nothing"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adapted := strings.Replace(fixtureAdapted, c.old, c.new, 1)
			if adapted == fixtureAdapted {
				t.Fatal("the patch changed nothing — the adapted fixture moved under this case")
			}
			problems := strings.Join(agreeIn(t, write(t, "", "", ""), adapted), "\n")
			if !strings.Contains(problems, c.want) {
				t.Fatalf("expected a problem containing %q, got:\n%s", c.want, problems)
			}
		})
	}
}

// A directive the translator does not understand is reported, not skipped. Passing
// over what it cannot verify is the same false negative in a different place.
func TestUnknownProxyDirectiveIsReported(t *testing.T) {
	root := write(t, "services/paste/japode.yml",
		"      - header_up -X-View-Id", "      - lb_policy round_robin")
	problems := strings.Join(agreeIn(t, root, fixtureAdapted), "\n")
	if !strings.Contains(problems, "is not a directive this can check") {
		t.Fatalf("expected the unknown directive to be reported, got:\n%s", problems)
	}
}

// The scan for keys nothing declares is scoped to this service's prefix, and the
// ways that goes wrong are all in one direction: flagging a key that is somebody
// else's. Each of these must produce no problem at all.
func TestOrphanScanMindsItsOwnBusiness(t *testing.T) {
	for _, extra := range []string{
		"ACME_EMAIL=ci@example.invalid",            // the edge's
		"BACKUP_KEEP_HOURLY=8",                     // the platform's
		"SKILLGIST_SITE_URL=https://skillgist.com", // another service's
		"PASTEBIN_TOKEN=",                          // a longer name, not this prefix
		"PASTE=",                                   // the bare name, no separator
		"# PASTE_LEGACY_FLAG=",                     // commented out is not a key
		"  PASTE_INDENTED=",                        // not at the start of a line
	} {
		t.Run(extra, func(t *testing.T) {
			root := write(t, ".env.example",
				"PASTE_SITE_URL=https://paste.example.com",
				"PASTE_SITE_URL=https://paste.example.com\n"+extra)
			if p := agreeIn(t, root, fixtureAdapted); len(p) != 0 {
				t.Fatalf("%q is not this manifest's to complain about, got:\n  %s",
					extra, strings.Join(p, "\n  "))
			}
		})
	}
}

// A second route's hosts variable is declared by the route, not by env[], and the
// scan has to know that or it reports the file it just asked for.
func TestSecondRouteHostsKeyIsDeclared(t *testing.T) {
	root := write(t, ".env.example",
		"PASTE_HOSTS=paste.example.com",
		"PASTE_HOSTS=paste.example.com\nPASTE_CONTENT_HOSTS=raw.paste.example.com")
	// The fixture manifest has one route, so this key belongs to nobody yet.
	if p := strings.Join(agreeIn(t, root, fixtureAdapted), "\n"); !strings.Contains(p, "PASTE_CONTENT_HOSTS") {
		t.Fatalf("with one route declared, a content hosts key is an orphan; got:\n%s", p)
	}
	// Declare the second route and it becomes accounted for. The route also has to
	// exist in the source and the adapted config, so this asserts only that the
	// *key* stops being reported as undeclared.
	root = write(t, "services/paste/japode.yml",
		"    proxy:\n      - header_up X-Forwarded-For {remote_host}\n      - header_up -X-View-Id\n",
		"    log: access\n    proxy:\n      - header_up X-Forwarded-For {remote_host}\n      - header_up -X-View-Id\n  - hosts: [raw.paste.example.com]\n    log: content\n    proxy:\n      - header_up -X-View-Id\n")
	data, err := os.ReadFile(filepath.Join(root, "services", "paste", "japode.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := m.hostsVar(m.Routes[1]); got != "PASTE_CONTENT_HOSTS" {
		t.Fatalf("hostsVar for the content route = %q, want PASTE_CONTENT_HOSTS", got)
	}
	if p := strings.Join(m.orphanEnvKeys(".env.example",
		"PASTE_HOSTS=x\nPASTE_CONTENT_HOSTS=y\nPASTE_SITE_URL=z\n"), "\n"); p != "" {
		t.Fatalf("both hosts keys are declared by routes; got:\n%s", p)
	}
}

// A fixed value in .env.example is one mistake and must be reported once: the
// specific check names it, and the orphan scan must not add a second line about
// the same key.
func TestFixedValueKeyIsReportedOnce(t *testing.T) {
	root := write(t, ".env.example",
		"PASTE_HOSTS=paste.example.com",
		"PASTE_HOSTS=paste.example.com\nPASTE_DATABASE_PATH=/data/paste.db")
	var about int
	for _, problem := range agreeIn(t, root, fixtureAdapted) {
		if strings.Contains(problem, "PASTE_DATABASE_PATH") {
			about++
		}
	}
	if about != 1 {
		t.Fatalf("expected exactly one problem about PASTE_DATABASE_PATH, got %d", about)
	}
}

func TestBytesOf(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
	}{{"8MB", 8000000}, {"4MB", 4000000}, {"20MiB", 20 * 1024 * 1024}, {"512B", 512}, {"1GB", 1000000000}} {
		got, err := bytesOf(c.in)
		if err != nil || got != c.want {
			t.Errorf("bytesOf(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	if _, err := bytesOf("8 megabytes"); err == nil {
		t.Error("expected a unit this does not understand to be an error")
	}
}

// A service with no sqlite and no exclude declares no backup.conf, and its absence
// is agreement rather than a missing file.
func TestBackupConfIsOptionalWhenNothingNeedsIt(t *testing.T) {
	root := t.TempDir()
	manifest := strings.Replace(fixtureManifest, "    sqlite: [paste.db]\n    exclude: [tmp]\n", "", 1)
	files := map[string]string{
		"services/paste/japode.yml":         manifest,
		"services/paste/docker-compose.yml": fixtureCompose,
		"caddy/sites/paste.caddy":           fixtureRoute,
		".env.example":                      fixtureEnvExample,
	}
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if p := agreeIn(t, root, fixtureAdapted); len(p) != 0 {
		t.Fatalf("no backup.conf and nothing needing one should agree, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// The committed tree has to agree, which is the assertion CI actually runs.
// scripts/check-jcloud.sh adapts the real Caddyfile before calling go test, so the
// route half is checked there; run on its own, `go test` has no caddy and says so
// rather than passing on a weaker check.
func TestCommittedManifestsAgree(t *testing.T) {
	paths, err := filepath.Glob("../../services/*/japode.yml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("no services/*/japode.yml in this tree")
	}
	adapted, err := os.ReadFile("../../.deploy-state/caddy-adapt.json")
	if err != nil {
		t.Skip("no ../../.deploy-state/caddy-adapt.json — run scripts/check-jcloud.sh, which adapts it first")
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		m, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if p := m.AgreeWith("../..", adapted); len(p) != 0 {
			t.Errorf("%s does not agree with the files beside it:\n  %s", path, strings.Join(p, "\n  "))
		}
	}
}
