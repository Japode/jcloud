package main

import (
	"strings"
	"testing"
)

// compareValid parses the shared fixture twice, patching the second copy — the
// shape of the real question, which is always one file against a copy of itself
// that somebody else changed.
func compareValid(t *testing.T, patch func(string) string) []string {
	t.Helper()
	ours, err := Parse([]byte(valid))
	if err != nil {
		t.Fatalf("the fixture does not parse: %v", err)
	}
	theirs, err := Parse([]byte(patch(valid)))
	if err != nil {
		t.Fatalf("the patched fixture does not parse: %v", err)
	}
	return ours.CompareTo(theirs, "ours.yml", "theirs.yml")
}

// The two copies are hand-written in different repositories for different readers,
// so they will never be the same bytes. Comments and key order are exactly what is
// allowed to differ — the rule scripts/check-upstream-routes.sh already applies to
// the route, applied here to everything else.
func TestProseAndKeyOrderAreNotDrift(t *testing.T) {
	p := compareValid(t, func(s string) string {
		s = strings.Replace(s, "port: 3000", "# the port the image exposes, and nothing else\nport: 3000", 1)
		// A mapping does not have an order, and two people writing the same
		// manifest will not choose the same one.
		return strings.Replace(s,
			"name: skillgist\nimage: ghcr.io/japode/skillgist",
			"image: ghcr.io/japode/skillgist\nname: skillgist", 1)
	})
	if len(p) != 0 {
		t.Fatalf("prose and key order are not disagreements, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// `doc:` is the one prose the schema itself carries, so the rule above cannot be
// left to YAML comments alone. The service repository writes its manifest for its
// own readers — Japode/fespsp-institucional documents every variable in pt-BR — and a
// sentence nothing renders from is not a fact the two copies share.
func TestADocumentedVariableMayBeDocumentedInAnotherLanguage(t *testing.T) {
	p := compareValid(t, func(s string) string {
		return strings.Replace(s,
			"doc: What the application says about itself.",
			"doc: O que a aplicação diz sobre si mesma.", 1)
	})
	if len(p) != 0 {
		t.Fatalf("a translated doc: is not drift, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// The exclusion is one field wide. Everything else about a variable is a fact the
// server acts on, so it stays compared — this is what stops the line above from
// being read as "variables are not compared".
func TestTheRestOfAVariableIsStillCompared(t *testing.T) {
	for _, c := range []struct{ name, from, to, want string }{
		{"the name", "- name: SITE_URL", "- name: PUBLIC_URL", "PUBLIC_URL"},
		{"the secret flag", "    secret: true", "    secret: false", "secret: false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := compareValid(t, func(s string) string { return strings.Replace(s, c.from, c.to, 1) })
			if len(p) == 0 {
				t.Fatalf("%s drifted and the comparison said nothing", c.name)
			}
			if !strings.Contains(strings.Join(p, "\n"), c.want) {
				t.Errorf("should name %q\ngot:\n  %s", c.want, strings.Join(p, "\n  "))
			}
		})
	}
}

func TestComparisonFindsEveryKindOfDrift(t *testing.T) {
	cases := []struct {
		name  string
		patch func(string) string
		want  string
	}{
		// Facts that had no comparison at all before this: only the route's proxy
		// body had one, and only in its own script.
		{"the port", func(s string) string { return strings.Replace(s, "port: 3000", "port: 3001", 1) },
			"port: 3001"},
		{"the health path", func(s string) string { return strings.Replace(s, "path: /api/health", "path: /healthz", 1) },
			"path: /healthz"},
		{"a volume name", func(s string) string { return strings.Replace(s, "- name: data", "- name: store", 1) },
			"name: store"},
		{"a variable's default", func(s string) string {
			return strings.Replace(s, "default: https://skillgist.com", "default: https://skillgist.invalid", 1)
		}, "skillgist.invalid"},
		{"the proxy body", func(s string) string {
			return strings.Replace(s, "header_up -X-View-Id", "header_up -X-Nothing", 1)
		}, "X-Nothing"},
		// A list that gained an entry shifts every line after it. The region is
		// reported from both sides rather than guessed at.
		{"a variable nobody added here", func(s string) string {
			return strings.Replace(s, "env:\n", "env:\n  - name: EXTRA\n", 1)
		}, "EXTRA"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := compareValid(t, c.patch)
			if len(p) == 0 {
				t.Fatalf("%s drifted and the comparison said nothing", c.name)
			}
			if !strings.Contains(strings.Join(p, "\n"), c.want) {
				t.Errorf("should name %q\ngot:\n  %s", c.want, strings.Join(p, "\n  "))
			}
		})
	}
}

// JCT16: two differences far apart — the port near the top, a variable appended at
// the end — are reported as those two and nothing between them. Trimming only the
// common head and tail printed every line from `port:` to the end, `health:`,
// `volumes:` and `routes:` included.
func TestTwoDistantDifferencesAreReportedAlone(t *testing.T) {
	p := compareValid(t, func(s string) string {
		s = strings.Replace(s, "port: 3000", "port: 3001", 1)
		return strings.Replace(s, "    secret: true\nneeds:", "    secret: true\n  - name: MAX_UPLOAD\n    default: 10MB\nneeds:", 1)
	})
	if len(p) == 0 {
		t.Fatal("two differences and the comparison said nothing")
	}
	var sawPort, sawUpload bool
	for _, line := range p {
		field, _, _ := strings.Cut(line, ":")
		switch {
		case field == "port":
			sawPort = true
		case strings.HasPrefix(field, "env["):
			sawUpload = sawUpload || strings.Contains(line, "MAX_UPLOAD")
		default:
			t.Errorf("a line under %s differs in neither copy: %s", field, line)
		}
	}
	if !sawPort || !sawUpload {
		t.Fatalf("both differences should be named, got:\n  %s", strings.Join(p, "\n  "))
	}
	if !strings.Contains(strings.Join(p, "\n"), `port: ours.yml has "port: 3000"`) {
		t.Errorf("each side's line is named by its field, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// The field a line sits under, as the report names it.
func TestFieldsOfACanonicalRendering(t *testing.T) {
	rendered := lines([]byte("version: 1\nhealth:\n    path: /x\n    test:\n        - CMD\nenv:\n    - name: A\n      secret: true\n    - name: B\n"))
	want := []string{"version", "health", "health.path", "health.test", "health.test", "env", "env[0]", "env[0]", "env[1]"}
	got := fieldsOf(rendered)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d %q: got %q, want %q", i, rendered[i], got[i], want[i])
		}
	}
}

// Why the comparison is of canonical renderings and not of named fields: a field
// added to the schema is compared without a line changing in compare.go. If this
// fails, the comparison has grown a hand-written list of what it looks at, and the
// next field added to the manifest will drift in silence.
func TestEveryFieldOfTheSchemaIsCompared(t *testing.T) {
	m, err := Parse([]byte(valid))
	if err != nil {
		t.Fatalf("the fixture does not parse: %v", err)
	}
	canon, err := m.Canon()
	if err != nil {
		t.Fatalf("canon: %v", err)
	}
	for _, field := range []string{"version", "name", "image", "port", "health", "volumes", "routes", "env", "needs"} {
		if !strings.Contains(string(canon), field+":") {
			t.Errorf("the canonical rendering drops %q, so drift in it would be invisible", field)
		}
	}
}
