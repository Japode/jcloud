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

const fixtureRoute = `{$PASTE_HOSTS:paste.example.com} {
	request_body {
		max_size 4MB
	}
	reverse_proxy paste:8080 {
		header_up X-Forwarded-For {remote_host}
		health_uri /healthz
	}
}
`

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

func agreeIn(t *testing.T, root string) []string {
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
	return m.Agree(root)
}

func TestFixtureAgreesWithItself(t *testing.T) {
	if p := agreeIn(t, write(t, "", "", "")); len(p) != 0 {
		t.Fatalf("the fixture should agree with itself, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// One case per fact that lives in more than one file. Each is a real mistake
// somebody makes: a port changed in one place, a volume renamed on one side, a
// variable added to compose and not to .env.example.
func TestDisagreements(t *testing.T) {
	cases := []struct {
		name, file, old, new, want string
	}{
		{"route points at another port", "caddy/sites/paste.caddy",
			"reverse_proxy paste:8080", "reverse_proxy paste:8081",
			"block(s) proxy to paste:8080"},
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
		{"health_uri drifted", "caddy/sites/paste.caddy",
			"health_uri /healthz", "health_uri /livez",
			"no health_uri /healthz"},
		{"a load-bearing proxy line went", "caddy/sites/paste.caddy",
			"\t\theader_up X-Forwarded-For {remote_host}\n", "",
			"does not carry \"header_up X-Forwarded-For {remote_host}\""},
		{"max_body drifted", "caddy/sites/paste.caddy",
			"max_size 4MB", "max_size 8MB",
			"no max_size 4MB"},
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
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := strings.Join(agreeIn(t, write(t, c.file, c.old, c.new)), "\n")
			if !strings.Contains(problems, c.want) {
				t.Fatalf("expected a problem containing %q, got:\n%s", c.want, problems)
			}
		})
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
	if p := agreeIn(t, root); len(p) != 0 {
		t.Fatalf("no backup.conf and nothing needing one should agree, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// The committed tree has to agree, which is the assertion CI actually runs.
func TestCommittedManifestsAgree(t *testing.T) {
	paths, err := filepath.Glob("../../services/*/japode.yml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("no services/*/japode.yml in this tree")
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
		if p := m.Agree("../.."); len(p) != 0 {
			t.Errorf("%s does not agree with the files beside it:\n  %s", path, strings.Join(p, "\n  "))
		}
	}
}
