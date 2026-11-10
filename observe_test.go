package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// JCT22: a docker that answers the way the real one does for the three calls
// observe makes, from a table of containers, with no daemon.
type fakeContainer struct{ name, status, image string }

func fakeDocker(containers []fakeContainer, digests map[string]string) commandRunner {
	return func(name string, args ...string) (string, error) {
		if name != "docker" {
			return "", errors.New("observe ran " + name)
		}
		var out strings.Builder
		switch {
		case args[0] == "ps":
			for _, c := range containers {
				out.WriteString(c.name + "\n")
			}
		case args[0] == "inspect":
			for _, asked := range args[3:] {
				for _, c := range containers {
					if c.name == asked {
						out.WriteString("/" + c.name + " " + c.status + " " + c.image + "\n")
					}
				}
			}
		case args[0] == "image" && args[1] == "inspect":
			for _, id := range args[4:] {
				out.WriteString(id + " " + digests[id] + " \n")
			}
		default:
			return "", errors.New("observe asked docker " + strings.Join(args, " "))
		}
		return out.String(), nil
	}
}

var demoManifest = strings.Replace(fixtureManifest, "name: paste", "name: demo", 1) + "profile: demo\n"

func observeTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"docker-compose.yml":        "services:\n  caddy: {}\n",
		"services/paste/japode.yml": fixtureManifest,
		"services/demo/japode.yml":  demoManifest,
		".deploy-state/images.env":  "PASTE_IMAGE=ghcr.io/x/paste@sha256:aaaa\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return layTree(t, files)
}

func noEnv(t *testing.T) {
	t.Helper()
	was := lookupEnv
	lookupEnv = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { lookupEnv = was })
}

var upPasteAndEdge = []fakeContainer{
	{"japode-paste", "healthy", "sha256:img1"},
	{"japode-caddy", "running", "sha256:img2"},
}

// What observe prints is what survey reads: a host running what the tree declares
// round-trips to no divergence. The edge is a local image with no repo digest, so
// it gets no `running` line, and the demo profile nothing switched on reads off.
func TestObserveRoundTripsThroughSurvey(t *testing.T) {
	noEnv(t)
	root := observeTree(t, nil)
	lines, err := Observe(root, fakeDocker(upPasteAndEdge, map[string]string{"sha256:img1": "ghcr.io/x/paste@sha256:aaaa"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"container caddy running",
		"container paste healthy",
		"pinned paste ghcr.io/x/paste@sha256:aaaa",
		"profile demo off",
		"running paste ghcr.io/x/paste@sha256:aaaa",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("got\n  %s\nwant\n  %s", strings.Join(lines, "\n  "), strings.Join(want, "\n  "))
	}

	d, err := DeclaredIn(root)
	if err != nil {
		t.Fatal(err)
	}
	// caddy is "running", not healthy, and that is survey's to report; held healthy
	// here so the round trip is about the format and nothing else.
	o := ParseObserved(strings.Replace(strings.Join(lines, "\n"), "caddy running", "caddy healthy", 1))
	if got := SurveyAgainst(d, o); len(got) != 0 {
		t.Fatalf("observe's own output should survey clean, got %+v", got)
	}

	// A pinned digest the container is not on is the drift survey exists to name.
	lines, err = Observe(root, fakeDocker(upPasteAndEdge, map[string]string{"sha256:img1": "ghcr.io/x/paste@sha256:bbbb"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := SurveyAgainst(d, ParseObserved(strings.Join(lines, "\n"))); len(got) == 0 || !strings.Contains(got[len(got)-1].Says, "pinned") {
		t.Fatalf("a digest drift should come through, got %+v", got)
	}
}

// A profile is on when COMPOSE_PROFILES names it (the environment first, then the
// tree's .env, compose's order), or when one of its services is up.
func TestObserveProfiles(t *testing.T) {
	docker := fakeDocker(upPasteAndEdge, nil)
	profile := func(t *testing.T, root string) string {
		t.Helper()
		lines, err := Observe(root, docker)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			if strings.HasPrefix(l, "profile ") {
				return l
			}
		}
		return ""
	}

	noEnv(t)
	if got := profile(t, observeTree(t, map[string]string{".env": "X=1\nCOMPOSE_PROFILES=\"other,demo\"\n"})); got != "profile demo on" {
		t.Errorf(".env naming it: got %q", got)
	}
	if got := profile(t, observeTree(t, map[string]string{".env": "COMPOSE_PROFILES=*\n"})); got != "profile demo on" {
		t.Errorf("every profile: got %q", got)
	}

	lookupEnv = func(string) (string, bool) { return "", true }
	if got := profile(t, observeTree(t, map[string]string{".env": "COMPOSE_PROFILES=demo\n"})); got != "profile demo off" {
		t.Errorf("the environment wins over .env, as in compose: got %q", got)
	}

	noEnv(t)
	lines, err := Observe(observeTree(t, nil), fakeDocker(append(upPasteAndEdge, fakeContainer{"japode-demo", "healthy", "sha256:img3"}), nil))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(lines, "profile demo on") {
		t.Errorf("a service of the profile is up: got %v", lines)
	}
}

// The prefix stripped is jcloud.yml's, not Japode's.
func TestObserveReadsThePlatformsPrefix(t *testing.T) {
	noEnv(t)
	root := observeTree(t, map[string]string{"jcloud.yml": "container_prefix: viglet-\n"})
	lines, err := Observe(root, fakeDocker([]fakeContainer{{"viglet-paste", "healthy", "sha256:img1"}, {"japode-caddy", "healthy", "sha256:img2"}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(lines, "container paste healthy") || !contains(lines, "container japode-caddy healthy") {
		t.Fatalf("got %v", lines)
	}
}

// A docker that cannot be asked is an error, never an empty answer that survey
// would read as a server running nothing. An empty host is an answer.
func TestObserveRefusesADockerThatCannotBeAsked(t *testing.T) {
	noEnv(t)
	root := observeTree(t, nil)
	down := func(string, ...string) (string, error) { return "", errors.New("Cannot connect to the Docker daemon") }
	if lines, err := Observe(root, down); err == nil || len(lines) != 0 {
		t.Fatalf("want an error and no lines, got %v, %v", lines, err)
	}
	lines, err := Observe(root, fakeDocker(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pinned paste ghcr.io/x/paste@sha256:aaaa", "profile demo off"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("an empty host: got %v", lines)
	}
}
