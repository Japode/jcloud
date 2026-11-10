package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// JCT26: two runs compared by what each finding is, never by how it reads.
func TestSinceSplitsNewStandingAndGone(t *testing.T) {
	d := Declared{Services: []string{"paste", "notes"}, Others: []string{"caddy"},
		Host: Host{Disks: []string{"/"}, DiskFree: "15%"}}
	before := ParseObserved("container paste healthy\ncontainer caddy unhealthy\ndisk / 9\n")
	after := ParseObserved("container paste healthy\ncontainer caddy healthy\ncontainer notes healthy\ncontainer stray healthy\ndisk / 8\n")

	changes, gone := Since(SurveyAgainst(d, before), SurveyAgainst(d, after))
	state := map[string]string{}
	for _, c := range changes {
		state[c.Key()] = c.State
	}
	want := map[string]string{
		// The disk's sentence changed (9% to 8%) and the fact did not.
		"disk.low /": "standing",
		// A container nothing declares is new.
		"container.undeclared stray": "new",
	}
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("got %v", state)
	}
	var keys []string
	for _, g := range gone {
		keys = append(keys, g.Key())
	}
	// notes came up and caddy became healthy: both are gone.
	if want := []string{"container.unhealthy caddy", "container.missing notes"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("gone: got %v, want %v", keys, want)
	}
}

// Every finding carries a code, so no finding can be keyed on its prose alone.
func TestEveryFindingHasACode(t *testing.T) {
	d := Declared{Services: []string{"paste", "gone"}, Others: []string{"caddy"}, Host: declaredHost}
	o := ParseObserved("container paste unhealthy\npinned paste a@sha256:1\nrunning paste a@sha256:2\ncontainer odd healthy\n" +
		strings.NewReplacer("timer viglet-restore-test active", "timer viglet-restore-test failed",
			"firewall ufw active", "firewall ufw inactive", "path . 0750", "path . 755").Replace(agreeingHost))
	got := SurveyAgainst(d, o)
	if len(got) < 6 {
		t.Fatalf("the case should raise one finding of each kind, got %+v", got)
	}
	for _, f := range got {
		if f.Code == "" || !strings.Contains(f.Code, ".") {
			t.Errorf("%s %q has no kind.state code", f.Service, f.Says)
		}
	}
}

// Through the command: --since names the last run, the exit code stays this run's.
func TestSurveyCommandSince(t *testing.T) {
	noEnv(t)
	root := observeTree(t, nil)
	write := func(name, body string) string {
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	healthy := "container paste healthy\ncontainer caddy healthy\n"
	broken := write("before.txt", "container caddy healthy\n")
	fixed := write("after.txt", healthy)

	if code := survey([]string{"--root", root, "--since", broken, fixed}); code != 0 {
		t.Fatalf("a run with only gone findings is clean, got exit %d", code)
	}
	if code := survey([]string{"--root", root, "--since", fixed, broken}); code != 1 {
		t.Fatalf("a run with a new finding is red, got exit %d", code)
	}
	if code := survey([]string{"--root", root, "--since", write("empty.txt", " \n"), fixed}); code != 2 {
		t.Fatalf("an empty last run is refused like an empty run, got exit %d", code)
	}
	if code := survey([]string{"--root", root, "--since"}); code != 2 {
		t.Fatalf("--since with no path, got exit %d", code)
	}

	// JCT27: the same report as a document a scheduler keys on, and nothing else
	// on stdout, inside Actions too.
	t.Setenv("GITHUB_ACTIONS", "true")
	stray := write("stray.txt", healthy+"container stray healthy\n")
	out, code := surveyStdout(t, "--json", "--root", root, "--since", broken, stray)
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	var doc struct {
		Findings []map[string]string `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	want := []map[string]string{
		{"code": "container.undeclared", "subject": "stray", "says": "running on the server and declared by nothing here", "state": "new"},
		{"code": "container.missing", "subject": "paste", "says": "declared here and not running on the server", "state": "gone"},
	}
	if !reflect.DeepEqual(doc.Findings, want) {
		t.Fatalf("got %v", doc.Findings)
	}

	out, code = surveyStdout(t, "--root", root, "--json", fixed)
	if code != 0 || strings.TrimSpace(out) != "{\n  \"findings\": []\n}" {
		t.Fatalf("a clean run is an empty list, got %d %q", code, out)
	}
}

// surveyStdout is annotate_test.go's captureStdout, keeping the exit code.
func surveyStdout(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var code int
	out := captureStdout(t, func() { code = survey(args) })
	return out, code
}
