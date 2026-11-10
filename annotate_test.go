package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// JCT17: a workflow command is printed inside GitHub Actions and nowhere else.
// Pinned on both sides of the variable, through the real decision and not a stub
// of it.
func TestAnnotationsOnlyInsideActions(t *testing.T) {
	for _, c := range []struct {
		env  string
		want bool
	}{{"true", true}, {"", false}, {"false", false}} {
		t.Run("GITHUB_ACTIONS="+c.env, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", c.env)
			out := captureStdout(t, func() { annotate("error", "a/japode.yml", "port drifted") })
			if got := strings.Contains(out, "::error file=a/japode.yml::port drifted"); got != c.want {
				t.Fatalf("annotation printed = %v, want %v; stdout:\n%s", got, c.want, out)
			}
		})
	}
}

// And through a command: outside Actions a refused manifest's problem reaches the
// terminal once, on stderr, and stdout carries no workflow command.
func TestARefusalPrintsEachProblemOnceOutsideActions(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	path := filepath.Join(t.TempDir(), "japode.yml")
	if err := os.WriteFile(path, []byte(fixtureManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	refused := func(*Manifest) ([]string, []string) { return []string{"port drifted"}, []string{"said"} }
	out := captureStdout(t, func() { run([]string{path}, refused, "agrees", "agree") })
	if strings.Contains(out, "::") {
		t.Fatalf("a workflow command reached a terminal:\n%s", out)
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()
	done := make(chan string)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	f()
	_ = w.Close()
	return <-done
}
