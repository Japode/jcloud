package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The manifest the specification carries, with the three variables docs/JCLOUD.md
// abridged away put back. If this stops parsing, the specification and the schema
// have drifted and one of the two is wrong.
const valid = `
version: 1
name: skillgist
image: ghcr.io/japode/skillgist
port: 3000
health:
  path: /api/health
  probe: node
  start_period: 20s
volumes:
  - name: data
    path: /data
    sqlite: [skillgist.db]
    exclude: [tmp]
routes:
  - hosts: [skillgist.com, www.skillgist.com]
    max_body: 8MB
    log: access
    proxy:
      - header_up X-Forwarded-For {remote_host}
      - header_up -X-View-Id
  - hosts: [raw.skillgist.com]
    log: content
    proxy:
      - header_up -X-View-Id
env:
  - name: SITE_URL
    default: https://skillgist.com
    doc: What the application says about itself.
  - name: DATABASE_PATH
    value: /data/skillgist.db
  - name: GITHUB_CLIENT_SECRET
    secret: true
needs: []
`

func mustValid(t *testing.T, body string) *Manifest {
	t.Helper()
	m, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p := m.Validate(); len(p) != 0 {
		t.Fatalf("expected no problems, got:\n  %s", strings.Join(p, "\n  "))
	}
	return m
}

func TestValidManifest(t *testing.T) {
	m := mustValid(t, valid)
	if m.Name != "skillgist" || m.Port != 3000 {
		t.Fatalf("decoded wrong: %+v", m)
	}
	if len(m.Routes) != 2 || m.Routes[1].Log != "content" {
		t.Fatalf("routes decoded wrong: %+v", m.Routes)
	}
	// A fixed value and an absent default are different states, which is why
	// these are pointers: an empty default is a legal default.
	if m.Env[1].Value == nil || *m.Env[1].Value != "/data/skillgist.db" {
		t.Fatalf("value: did not survive decode: %+v", m.Env[1])
	}
	if m.Env[0].Value != nil {
		t.Fatalf("value: invented for a var that declared only a default")
	}
}

// A misspelt key must be an error. Silently ignoring it leaves the fact it was
// meant to state written by hand in the four files the manifest replaces.
func TestUnknownFieldIsRefused(t *testing.T) {
	_, err := Parse([]byte("version: 1\nname: x\nprot: 3000\n"))
	if err == nil {
		t.Fatal("expected a misspelt key to be refused")
	}
	if !strings.Contains(err.Error(), "prot") {
		t.Fatalf("the error should name the key it could not place: %v", err)
	}
}

func TestEmptyManifestNamesEveryMissingField(t *testing.T) {
	m, err := Parse([]byte("{}\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	problems := strings.Join(m.Validate(), "\n")
	for _, want := range []string{"version:", "name:", "image:", "port:", "health.path:", "health:"} {
		if !strings.Contains(problems, want) {
			t.Errorf("an empty manifest should name %s\ngot:\n%s", want, problems)
		}
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name  string
		patch func(string) string
		want  string
	}{
		{"tagged image",
			func(s string) string {
				return strings.Replace(s, "image: ghcr.io/japode/skillgist", "image: ghcr.io/japode/skillgist:latest", 1)
			},
			"carries a tag or a digest"},
		{"digest image",
			func(s string) string {
				return strings.Replace(s, "image: ghcr.io/japode/skillgist", "image: ghcr.io/japode/skillgist@sha256:00", 1)
			},
			"carries a tag or a digest"},
		// The spelling GitHub hands a workflow, written into a manifest. GHCR
		// answers `repository name must be lowercase` and the pull fails on the
		// server, which is why it is refused at the file instead.
		{"image with a capital in it",
			func(s string) string {
				return strings.Replace(s, "image: ghcr.io/japode/skillgist", "image: ghcr.io/Japode/skillgist", 1)
			},
			"is not a repository a registry will serve"},
		{"image with a capital and no host",
			func(s string) string {
				return strings.Replace(s, "image: ghcr.io/japode/skillgist", "image: Japode/skillgist", 1)
			},
			"is not a repository a registry will serve"},
		{"name not a short name",
			func(s string) string { return strings.Replace(s, "name: skillgist", "name: SkillGist", 1) },
			"is not a short name"},
		{"port out of range",
			func(s string) string { return strings.Replace(s, "port: 3000", "port: 70000", 1) },
			"is not a port"},
		{"probe and test together",
			func(s string) string {
				return strings.Replace(s, "  probe: node", "  probe: node\n  test: [CMD, true]", 1)
			},
			"would have to win silently"},
		{"unknown probe",
			func(s string) string { return strings.Replace(s, "probe: node", "probe: httpie", 1) },
			"not one of node, curl, wget"},
		{"bad start_period",
			func(s string) string { return strings.Replace(s, "start_period: 20s", "start_period: 20 seconds", 1) },
			"is not a duration"},
		{"bad health interval",
			func(s string) string { return strings.Replace(s, "start_period: 20s", "interval: every 15s", 1) },
			"health.interval: \"every 15s\" is not a duration"},
		{"relative health path",
			func(s string) string { return strings.Replace(s, "path: /api/health", "path: api/health", 1) },
			"is not a path"},
		{"absolute sqlite path",
			func(s string) string {
				return strings.Replace(s, "sqlite: [skillgist.db]", "sqlite: [/data/skillgist.db]", 1)
			},
			"relative to the volume root"},
		{"two routes claim one host",
			func(s string) string {
				return strings.Replace(s, "hosts: [raw.skillgist.com]", "hosts: [skillgist.com]", 1)
			},
			"claimed by another route"},
		{"two routes share a log name",
			func(s string) string { return strings.Replace(s, "log: content", "log: access", 1) },
			"used by another route"},
		{"bad max_body",
			func(s string) string { return strings.Replace(s, "max_body: 8MB", "max_body: 8 megabytes", 1) },
			"is not a size"},
		{"default and value together",
			func(s string) string {
				return strings.Replace(s, "    value: /data/skillgist.db", "    value: /data/skillgist.db\n    default: /tmp/x.db", 1)
			},
			"a fixed value is not configurable"},
		{"secret with a value",
			func(s string) string {
				return strings.Replace(s, "    secret: true", "    secret: true\n    default: hunter2", 1)
			},
			"never in this repository"},
		{"lower case variable",
			func(s string) string { return strings.Replace(s, "name: SITE_URL", "name: site_url", 1) },
			"is not a variable name"},
		// JC32 made `needs:` mean something, so what is refused is no longer the
		// field but the three ways an entry cannot be answered. Whether
		// addons/<type>/ exists is not among them: the schema says what a manifest
		// must state, and which providers a tree carries is `agree`'s question.
		{"a need with no type",
			func(s string) string {
				return strings.Replace(s, "needs: []", "needs:\n  - name: db", 1)
			},
			"needs[0].type: is missing"},
		{"a need named twice",
			func(s string) string {
				return strings.Replace(s, "needs: []",
					"needs:\n  - name: db\n    type: mariadb\n  - name: db\n    type: mariadb", 1)
			},
			"is declared twice"},
		// Both derive `skillgist-data`, one as a volume and one as a compose
		// service that also declares a volume — so the file that is merged second
		// takes the other's storage.
		{"a need with a volume's name",
			func(s string) string {
				return strings.Replace(s, "needs: []", "needs:\n  - name: data\n    type: mariadb", 1)
			},
			"is also a volume's name"},
		{"wrong schema version",
			func(s string) string { return strings.Replace(s, "version: 1", "version: 2", 1) },
			"this build accepts 1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := c.patch(valid)
			if body == valid {
				t.Fatal("the patch changed nothing — the fixture moved under this case")
			}
			m, err := Parse([]byte(body))
			if err != nil {
				// Some malformed cases are refused at decode, which is also a
				// refusal; assert the message still points at the right thing.
				if !strings.Contains(err.Error(), c.want) {
					t.Fatalf("refused at decode but not for the stated reason: %v", err)
				}
				return
			}
			problems := strings.Join(m.Validate(), "\n")
			if !strings.Contains(problems, c.want) {
				t.Fatalf("expected a problem containing %q, got:\n%s", c.want, problems)
			}
		})
	}
}

// A single route needs no log name; two do. The rule exists because the log name is
// what separates the derived hosts variable and log file of two routes on one
// service.
func TestSingleRouteNeedsNoLogName(t *testing.T) {
	body := strings.Replace(valid, `  - hosts: [raw.skillgist.com]
    log: content
    proxy:
      - header_up -X-View-Id
`, "", 1)
	body = strings.Replace(body, "    log: access\n", "", 1)
	mustValid(t, body)
}

func TestTwoRoutesNeedLogNames(t *testing.T) {
	body := strings.Replace(valid, "    log: access\n", "", 1)
	m, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p := strings.Join(m.Validate(), "\n"); !strings.Contains(p, "with more than one route") {
		t.Fatalf("expected the missing log name to be named, got:\n%s", p)
	}
}

// The manifests actually committed here have to pass. This is the test that would
// have caught the schema and the repository disagreeing.
func TestCommittedManifests(t *testing.T) {
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
		if p := m.Validate(); len(p) != 0 {
			t.Errorf("%s:\n  %s", path, strings.Join(p, "\n  "))
		}
	}
}
