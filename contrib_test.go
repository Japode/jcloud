package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// JCT28: the templates under contrib/ are copied by a platform and never run by
// jcloud, so nothing else would notice one calling a flag that does not exist.

func TestTheWatchTemplatesCallWhatExists(t *testing.T) {
	for _, file := range []string{"contrib/systemd/jcloud-watch.sh", "contrib/github/jcloud-watch.yml"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"observe --root", "survey --root", "--since", "--json", "observed.last"} {
			if !strings.Contains(string(body), want) {
				t.Errorf("%s does not call %q", file, want)
			}
		}
	}

	var wf struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			RunsOn []string `yaml:"runs-on"`
		} `yaml:"jobs"`
	}
	body, _ := os.ReadFile("contrib/github/jcloud-watch.yml")
	if err := yaml.Unmarshal(body, &wf); err != nil {
		t.Fatalf("the workflow is not YAML: %v", err)
	}
	// observe reads the machine it runs on, so a hosted runner would watch itself.
	if got := wf.Jobs["watch"].RunsOn; len(got) == 0 || got[0] != "self-hosted" {
		t.Errorf("the watch must run on a self-hosted runner, got %v", got)
	}
	if wf.Permissions["issues"] != "write" || len(wf.Permissions) != 1 {
		t.Errorf("the workflow asks for issues: write and nothing else, got %v", wf.Permissions)
	}
}

// The script against a jcloud that answers from a table: the first run has no
// last run to compare with, the second does, and a survey that could not run
// leaves the last run in place.
func TestTheWatchScriptRotatesOnlyAfterASurvey(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("needs a POSIX bash")
	}
	script, _ := filepath.Abs("contrib/systemd/jcloud-watch.sh")
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	log := filepath.Join(dir, "calls")
	fake := filepath.Join(dir, "jcloud")
	// The fake records its arguments, prints the observation it is told to, and
	// exits with the code SURVEY_EXIT names.
	if err := os.WriteFile(fake, []byte(`#!/usr/bin/env bash
echo "$*" >>"`+log+`"
case "$1" in
observe) printf 'container paste healthy\n# run %s\n' "$RUN" ;;
survey) case " $* " in *" --json "*) echo '{"findings": []}' ;; esac; exit "${SURVEY_EXIT:-0}" ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(n, surveyExit string) int {
		cmd := exec.Command(bash, script)
		cmd.Env = append(os.Environ(), "JCLOUD_ROOT="+dir, "JCLOUD_STATE="+state, "JCLOUD="+fake, "RUN="+n, "SURVEY_EXIT="+surveyExit)
		if out, err := cmd.CombinedOutput(); err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				return e.ExitCode()
			}
			t.Fatalf("%v\n%s", err, out)
		}
		return 0
	}
	last := func() string {
		b, _ := os.ReadFile(filepath.Join(state, "observed.last"))
		return string(b)
	}

	if code := run("1", "0"); code != 0 || !strings.Contains(last(), "run 1") {
		t.Fatalf("first run: exit %d, last %q", code, last())
	}
	if code := run("2", "1"); code != 1 || !strings.Contains(last(), "run 2") {
		t.Fatalf("a diverging run is still rotated, and red: exit %d, last %q", code, last())
	}
	if code := run("3", "2"); code != 2 || !strings.Contains(last(), "run 2") {
		t.Fatalf("a survey that could not run keeps the last run: exit %d, last %q", code, last())
	}
	calls, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if strings.Contains(lines[1], "--since") || !strings.Contains(lines[4], "--since "+filepath.Join(state, "observed.last")) {
		t.Fatalf("the first survey has no last run and the second does, got:\n%s", calls)
	}
	if _, err := os.Stat(filepath.Join(state, "report.json")); err != nil {
		t.Fatal("no report.json")
	}
}
