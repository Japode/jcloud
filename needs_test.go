package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The same imaginary service as agree_test.go's, with a database beside it. Its
// own fixture rather than a patch of that one: what these cases break is the
// agreement between a manifest, a provider and a compose file that declares two
// services, and half of them would read as changes to a fixture about something
// else.
const needyManifest = `
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
routes:
  - hosts: [paste.example.com]
    max_body: 4MB
    proxy:
      - header_up X-Forwarded-For {remote_host}
      - header_up -X-View-Id
env:
  - name: DB_HOST
    value: paste-db:3306
  - name: DB_NAME
    value: paste
  - name: DB_PASSWORD
    secret: true
needs:
  - name: db
    type: mariadb
`

// A provider, written here rather than read from ../../addons/: a unit test that
// consulted the deployed one would fail for reasons about that file, and the
// deployed one is asserted by TestCommittedManifestsAgree.
const needyAddon = `
version: 1
type: mariadb
image: mariadb:11.4@sha256:0000000000000000000000000000000000000000000000000000000000000000
port: 3306
volume:
  path: /var/lib/mysql
  backup: mysql
health:
  test: [CMD, healthcheck.sh, --connect, --innodb_initialized]
  start_period: 20s
env:
  - MARIADB_DATABASE
  - MARIADB_USER
  - MARIADB_PASSWORD
fixed:
  MARIADB_RANDOM_ROOT_PASSWORD: 'yes'
`

// The database is written *first* on purpose. Nothing requires that order, and
// that is the point: until JC32 the compose comparison read the first
// `healthcheck:` in the file, so this fixture would have asserted the
// application's health path against the database's probe.
const needyCompose = `services:
  paste-db:
    image: mariadb:11.4@sha256:0000000000000000000000000000000000000000000000000000000000000000
    container_name: japode-paste-db
    environment:
      MARIADB_DATABASE: paste
      MARIADB_USER: paste
      MARIADB_PASSWORD: ${PASTE_DB_PASSWORD:-}
      MARIADB_RANDOM_ROOT_PASSWORD: 'yes'
    volumes:
      - paste-db:/var/lib/mysql
    healthcheck:
      test: [CMD, healthcheck.sh, --connect, --innodb_initialized]
      start_period: 20s

  paste:
    image: ${PASTE_IMAGE:?no image pinned}
    container_name: japode-paste
    depends_on:
      paste-db:
        condition: service_healthy
    environment:
      DB_HOST: paste-db:3306
      DB_NAME: paste
      DB_PASSWORD: ${PASTE_DB_PASSWORD:-}
    volumes:
      - paste-data:/data
    expose:
      - '8080'
    healthcheck:
      test: [CMD, wget, -qO-, 'http://127.0.0.1:8080/healthz']
      start_period: 10s

volumes:
  paste-data:
  paste-db:
`

const needyBackupConf = `mysql paste-db
`

const needyEnvExample = `PASTE_HOSTS=paste.example.com
PASTE_DB_PASSWORD=
`

func writeNeedy(t *testing.T, file, old, new string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"services/paste/japode.yml":         needyManifest,
		"services/paste/docker-compose.yml": needyCompose,
		"services/paste/backup.conf":        needyBackupConf,
		"caddy/sites/paste.caddy":           fixtureRoute,
		".env.example":                      needyEnvExample,
		"addons/mariadb/addon.yml":          needyAddon,
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

func agreeNeedy(t *testing.T, root string) []string {
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
	return m.AgreeWith(root, []byte(fixtureAdapted))
}

func TestNeedyFixtureAgreesWithItself(t *testing.T) {
	if p := agreeNeedy(t, writeNeedy(t, "", "", "")); len(p) != 0 {
		t.Fatalf("the fixture should agree with itself, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// One case per fact a need spreads across the manifest, the provider, the compose
// file and backup.conf. Every one of them resolves and comes up under `docker
// compose config`, which is the whole reason this comparison exists.
func TestNeedDisagreements(t *testing.T) {
	cases := []struct {
		name, file, old, new, want string
	}{
		{"nothing provides the type", "services/paste/japode.yml",
			"type: mariadb", "type: postgres",
			"provides no such addon"},
		{"the db runs an image nothing pinned", "services/paste/docker-compose.yml",
			"image: mariadb:11.4@sha256:0000000000000000000000000000000000000000000000000000000000000000",
			"image: mariadb:11",
			"nothing runs mariadb:11.4@sha256:"},
		{"the db's volume went", "services/paste/docker-compose.yml",
			"- paste-db:/var/lib/mysql", "- paste-store:/var/lib/mysql",
			"nothing mounts paste-db at /var/lib/mysql"},
		{"the db's container name drifted", "services/paste/docker-compose.yml",
			"container_name: japode-paste-db", "container_name: paste-db",
			"no container_name: japode-paste-db"},
		{"the db's healthcheck is not the provider's", "services/paste/docker-compose.yml",
			"--innodb_initialized", "--replication_io",
			`does not run "--innodb_initialized"`},
		{"a variable the provider names is not set", "services/paste/docker-compose.yml",
			"      MARIADB_USER: paste\n", "",
			"does not set MARIADB_USER"},
		{"the root account was given a password to keep", "services/paste/docker-compose.yml",
			"MARIADB_RANDOM_ROOT_PASSWORD: 'yes'", "MARIADB_ROOT_PASSWORD: ${PASTE_DB_ROOT_PASSWORD:-}",
			"MARIADB_RANDOM_ROOT_PASSWORD is not"},
		// The one `docker compose config` is happiest about: everything resolves,
		// the application starts against a server still replaying its log, and
		// `up --wait` calls that a deploy.
		{"nothing waits for the db to be healthy", "services/paste/docker-compose.yml",
			"    depends_on:\n      paste-db:\n        condition: service_healthy\n", "",
			"nothing waits on paste-db"},
		{"waits for it to merely exist", "services/paste/docker-compose.yml",
			"condition: service_healthy", "condition: service_started",
			"nothing waits on paste-db"},
		{"the application dials somewhere else", "services/paste/japode.yml",
			"value: paste-db:3306", "value: db:3306",
			"nothing says the application reaches the database it declares"},
		{"the backup would copy it byte for byte", "services/paste/backup.conf",
			"mysql paste-db\n", "",
			"no `mysql paste-db`"},
		{"backup.conf names a need the manifest does not", "services/paste/backup.conf",
			"mysql paste-db", "mysql paste-db\nmysql paste-cache",
			"`mysql paste-cache` is not declared by the manifest"},
		// The provider's own half. It is read by `agree` and never by `check`, so
		// a broken one is reported where the service that needs it is checked.
		{"the provider pins no digest", "addons/mariadb/addon.yml",
			"image: mariadb:11.4@sha256:0000000000000000000000000000000000000000000000000000000000000000",
			"image: mariadb:11.4",
			"carries no digest"},
		{"the provider is not the one in that directory", "addons/mariadb/addon.yml",
			"type: mariadb", "type: mysql",
			"sits under addons/mariadb/"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := strings.Join(agreeNeedy(t, writeNeedy(t, c.file, c.old, c.new)), "\n")
			if !strings.Contains(problems, c.want) {
				t.Fatalf("expected a problem containing %q, got:\n%s", c.want, problems)
			}
		})
	}
}

// A service with no need is not asked for a provider, and a provider nobody needs
// is not read. The regression this guards is the cheap implementation of the
// above: walking addons/ and asserting each one is used somewhere.
func TestNeedlessServiceIsUnaffected(t *testing.T) {
	if p := agreeIn(t, write(t, "", "", ""), fixtureAdapted); len(p) != 0 {
		t.Fatalf("a manifest with no needs: should still agree with itself, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// Every provider this repository carries has to be one this build can read.
// Nothing else asserts it: an addon is read by the service that names it, so one
// nobody needs yet is a file with no check on it at all.
func TestCommittedAddons(t *testing.T) {
	paths := platformGlob(t, "addons/*/addon.yml")
	if len(paths) == 0 {
		t.Skip("no addons/*/addon.yml in this tree")
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		a, err := ParseAddon(data)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if p := a.Validate(path); len(p) != 0 {
			t.Errorf("%s:\n  %s", path, strings.Join(p, "\n  "))
		}
		if want := filepath.Base(filepath.Dir(path)); a.Type != want {
			t.Errorf("%s declares type %q and sits in addons/%s/", path, a.Type, want)
		}
	}
}
