package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

const newDigest = "ghcr.io/x/paste@sha256:bbbb"

// deployHost answers as a host where paste runs the digest pinned before.
func deployHost() *fakeJobs {
	return (&fakeJobs{}).
		on("ps -a --filter name=", "").
		on("ps -q paste", "cid1\n").
		on("{{.Image}} cid1", "img1\n").
		on("{{index .RepoDigests 0}}{{end}} img1", "ghcr.io/x/paste@sha256:aaaa\n")
}

func pinOf(t *testing.T, root string) string {
	t.Helper()
	return string(mustRead(t, filepath.Join(root, ".deploy-state", "images.env")))
}

// JCT49: a deploy pins the digest beside the other services' pins, checks the
// configuration (compose and caddy validate) before anything comes up, pulls,
// brings the stack up under the wait budget, reloads the edge and prunes only
// dangling images, in that order.
func TestDeployPinsChecksPullsWaitsAndReloads(t *testing.T) {
	root := jobsTree(t)
	f := deployHost()
	var out bytes.Buffer
	if code := Deploy(root, deployOpts{targets: []string{"paste"}, image: newDigest, wait: 240}, f.run, &out); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if pins := pinOf(t, root); pins != "OTHER_IMAGE=ghcr.io/x/other@sha256:bbbb\nPASTE_IMAGE="+newDigest+"\n" {
		t.Fatalf("pins:\n%s", pins)
	}
	order := []string{"config --quiet", "--entrypoint caddy caddy validate --config /etc/caddy/Caddyfile", "pull --quiet",
		"up -d --remove-orphans --wait --wait-timeout 240", "exec -T caddy caddy reload", "image prune -f"}
	last := -1
	for _, step := range order {
		i := f.index(step)
		if i <= last {
			t.Fatalf("%q at %d, after %d:\n%v", step, i, last, f.calls)
		}
		last = i
	}
	if !strings.Contains(out.String(), "paste  "+newDigest) {
		t.Fatalf("output:\n%s", out.String())
	}
}

// JCT49: an expired wait asks docker once. An unwell container rolls back to the
// recorded digest and the deploy fails; all healthy is a deploy that passed late;
// a first boot gets --first-boot and has nowhere to roll back to.
func TestDeployTriagesAnExpiredWait(t *testing.T) {
	root := jobsTree(t)
	f := deployHost().
		first(jobRule{match: "up -d --remove-orphans", fail: true}).
		on("ps --all --format", "japode-paste paste running unhealthy\njapode-caddy caddy running healthy\njapode-cloud-caddy-run-0af3 caddy exited\n")
	var out bytes.Buffer
	if code := Deploy(root, deployOpts{targets: []string{"paste"}, image: newDigest, wait: 240}, f.run, &out); code != 1 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if !strings.Contains(pinOf(t, root), "PASTE_IMAGE=ghcr.io/x/paste@sha256:aaaa\n") || f.index("up -d --wait --wait-timeout 240") < 0 {
		t.Fatalf("not rolled back:\n%s\n%s", pinOf(t, root), out.String())
	}
	if !strings.Contains(out.String(), "reverted to the previous state") || f.index("caddy reload") >= 0 {
		t.Fatalf("output:\n%s", out.String())
	}

	root = jobsTree(t)
	f = deployHost().
		first(jobRule{match: "up -d --remove-orphans", fail: true}).
		on("ps --all --format", "japode-paste paste running healthy\njapode-caddy caddy running \n")
	out.Reset()
	if code := Deploy(root, deployOpts{targets: []string{"paste"}, image: newDigest, wait: 240}, f.run, &out); code != 0 {
		t.Fatalf("late: exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "passed late, not badly") || f.index("caddy reload") < 0 || !strings.Contains(pinOf(t, root), newDigest) {
		t.Fatalf("late:\n%s", out.String())
	}

	root = jobsTree(t)
	f = (&fakeJobs{}).
		first(jobRule{match: "up -d --remove-orphans", fail: true}).
		on("ps --all --format", "japode-paste paste running starting\n")
	out.Reset()
	if code := Deploy(root, deployOpts{targets: []string{"paste"}, image: newDigest, wait: 240, firstBoot: 420}, f.run, &out); code != 1 {
		t.Fatalf("first boot: exit %d", code)
	}
	if f.index("--wait-timeout 420") < 0 || !strings.Contains(out.String(), "first deploy of these services; nothing was restored") {
		t.Fatalf("first boot:\n%s", out.String())
	}
}

// JCT49: what the deploy refuses before it changes anything.
func TestDeployRefusesBeforeChangingAnything(t *testing.T) {
	root := jobsTree(t)
	before := pinOf(t, root)
	for _, c := range []struct {
		o    deployOpts
		want string
	}{
		{deployOpts{targets: []string{"paste"}, image: "ghcr.io/x/paste:latest"}, "must be a digest"},
		{deployOpts{targets: []string{"nowhere"}}, "unknown service: nowhere"},
		{deployOpts{targets: []string{"paste", "other"}, image: newDigest}, "--image pins one service"},
	} {
		f := deployHost()
		var out bytes.Buffer
		if code := Deploy(root, c.o, f.run, &out); code != 1 || !strings.Contains(out.String(), c.want) {
			t.Fatalf("%+v: exit %d\n%s", c.o, code, out.String())
		}
		if len(f.calls) != 0 || pinOf(t, root) != before {
			t.Fatalf("%+v changed something", c.o)
		}
	}

	unpinned := exportTree(t, map[string]string{".env": jobsEnv, ".deploy-state/images.env": "OTHER_IMAGE=x\n"})
	var out bytes.Buffer
	if code := Deploy(unpinned, deployOpts{targets: []string{"paste"}}, deployHost().run, &out); code != 1 || !strings.Contains(out.String(), "paste has no image pinned: run jcloud deploy paste --image") {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}

// JCT49: the triage judges a listing without docker, and drops a one-off
// container a killed `compose run` left (JC83).
func TestTriageJudgesAContainerListing(t *testing.T) {
	got := triage("a svc-a running healthy\nb svc-b running\nc svc-c exited\nd svc-d running starting\nx-svc-run-0af3 svc-x exited\n\n")
	want := []triageLine{{true, "svc-a", "healthy"}, {true, "svc-b", "running, and declares no healthcheck"},
		{false, "svc-c", "exited"}, {false, "svc-d", "running, starting"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
