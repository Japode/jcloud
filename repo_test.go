package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The service's own repository, as small as it can be and still hold every fact
// this comparison asks about. It declares the same service fixtureManifest does,
// so one manifest is the subject of both suites — which is the point: the two
// comparisons are the same claims asked of two different repositories.
const fixtureDockerfile = `FROM node:26-alpine
WORKDIR /app
COPY . .
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
CMD ["node", "server.js"]
`

const fixtureUpstreamCaddyfile = `{$SITE_HOSTS:paste.example.com} {
	encode zstd gzip

	request_body {
		max_size 4MB
	}

	reverse_proxy app:8080 {
		# The two load-bearing lines. Compared against the platform's copy by
		# scripts/check-upstream-routes.sh and not here — see agreeCaddyfile.
		header_up X-Forwarded-For {remote_host}
		header_up -X-View-Id

		health_uri /healthz
		health_interval 15s
	}
}
`

const fixturePublish = `name: Publish
on:
  workflow_run:
    workflows: [CI]
    types: [completed]
env:
  IMAGE: ghcr.io/${{ github.repository }}
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
      - uses: docker/build-push-action@v6
        with:
          push: true
          tags: ${{ env.IMAGE }}:latest
`

// The repository this fixture claims to be, as Actions spells it. It is the one
// fact no file in the tree states, which is why it is a parameter at all.
const fixtureRepository = "x/paste"

// writeRepo lays the fixture repository down and applies one substitution to one
// file, which is how each case below breaks exactly one fact.
func writeRepo(t *testing.T, file, old, new string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"japode.yml":                    fixtureManifest,
		"Dockerfile":                    fixtureDockerfile,
		"Caddyfile":                     fixtureUpstreamCaddyfile,
		".github/workflows/publish.yml": fixturePublish,
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

func agreeWithRepoIn(t *testing.T, root, repository string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "japode.yml"))
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
	return m.AgreeWithRepo(root, repository)
}

func TestFixtureRepositoryAgreesWithItself(t *testing.T) {
	if p := agreeWithRepoIn(t, writeRepo(t, "", "", ""), fixtureRepository); len(p) != 0 {
		t.Fatalf("the fixture repository should agree with itself, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// Without the repository name the workflow's image is an unexpanded expression,
// and that is said rather than passed over. Same rule as the adapted configuration
// in agree.go: the answer to "I could not look" must never be a tick.
func TestUnexpandedImageIsReportedAndNotAssumed(t *testing.T) {
	p := strings.Join(agreeWithRepoIn(t, writeRepo(t, "", "", ""), ""), "\n")
	if !strings.Contains(p, "which image this pushes is unchecked") {
		t.Fatalf("expected the unexpanded expression to be reported, got:\n%s", p)
	}
}

// A Caddyfile is optional — a service that runs no stack of its own has none, and
// absent with nothing to say is agreement.
func TestRepositoryWithoutCaddyfileIsNotAFault(t *testing.T) {
	root := writeRepo(t, "", "", "")
	if err := os.Remove(filepath.Join(root, "Caddyfile")); err != nil {
		t.Fatal(err)
	}
	if p := agreeWithRepoIn(t, root, fixtureRepository); len(p) != 0 {
		t.Fatalf("no Caddyfile is not a disagreement, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// One case per fact the platform is being asked to honour. Each is a real mistake
// somebody makes in the service's repository — the port moved in the Dockerfile,
// the health route renamed, a volume added to the image and to nothing else.
func TestRepositoryDisagreements(t *testing.T) {
	cases := []struct {
		name, file, old, new, want string
	}{
		{"the image exposes another port", "Dockerfile",
			"EXPOSE 8080", "EXPOSE 8081",
			"EXPOSE names 8081 and the manifest declares port 8080"},
		{"the image exposes nothing", "Dockerfile",
			"EXPOSE 8080\n", "",
			"nothing is EXPOSEd"},
		{"the healthcheck probes another path", "Dockerfile",
			"/healthz", "/livez",
			"the HEALTHCHECK does not probe /healthz"},
		{"the healthcheck probes another port", "Dockerfile",
			"127.0.0.1:8080", "127.0.0.1:9090",
			"the HEALTHCHECK does not probe port 8080"},
		{"the image has no healthcheck at all", "Dockerfile",
			"HEALTHCHECK", "# HEALTHCHECK",
			"no HEALTHCHECK"},
		{"the image declares a volume nobody mounts", "Dockerfile",
			`VOLUME ["/data"]`, `VOLUME ["/data", "/cache"]`,
			"VOLUME /cache is declared by the image and by no volume in the manifest"},
		{"the route aims at another port", "Caddyfile",
			"reverse_proxy app:8080", "reverse_proxy app:8081",
			"does not aim at port 8080"},
		{"the route probes another path", "Caddyfile",
			"health_uri /healthz", "health_uri /livez",
			"health_uri is /livez and the manifest declares /healthz"},
		{"the route probes nothing", "Caddyfile",
			"\t\thealth_uri /healthz\n", "",
			"the block probes nothing"},
		{"the probe interval drifted", "Caddyfile",
			"health_interval 15s", "health_interval 1m",
			"probes every 1m and the manifest declares 15s"},
		// Caddy's default is 30s and the manifest declares 15s, so saying nothing
		// is a disagreement and not an absence (JC41).
		{"the probe interval is left to Caddy", "Caddyfile",
			"\t\thealth_interval 15s\n", "",
			"probes every 30s and the manifest declares 15s"},
		{"nothing proxies at all", "Caddyfile",
			"reverse_proxy app:8080 {", "respond 200 {",
			"no reverse_proxy, and the manifest declares 1 route(s)"},
		{"the workflow pushes another image", ".github/workflows/publish.yml",
			"ghcr.io/${{ github.repository }}", "ghcr.io/x/paste-next",
			"pushes ghcr.io/x/paste-next and the manifest declares ghcr.io/x/paste"},
		{"no workflow publishes the image", ".github/workflows/publish.yml",
			"  IMAGE: ghcr.io/${{ github.repository }}\n", "",
			"nothing here names ghcr.io/x/paste"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := agreeWithRepoIn(t, writeRepo(t, c.file, c.old, c.new), fixtureRepository)
			if !strings.Contains(strings.Join(p, "\n"), c.want) {
				t.Fatalf("expected %q, got:\n  %s", c.want, strings.Join(p, "\n  "))
			}
		})
	}
}

// The Dockerfile is the one file that is not optional: the manifest promises the
// platform an image built from this repository, and nothing else here states its
// port.
func TestMissingDockerfileIsReported(t *testing.T) {
	root := writeRepo(t, "", "", "")
	if err := os.Remove(filepath.Join(root, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	p := strings.Join(agreeWithRepoIn(t, root, fixtureRepository), "\n")
	if !strings.Contains(p, "Dockerfile:") {
		t.Fatalf("expected the missing Dockerfile to be reported, got:\n%s", p)
	}
}

// A HEALTHCHECK written over two lines is one directive, and the continuation
// carries the path and the port. Read a line at a time it would be neither.
func TestHealthcheckContinuationIsOneDirective(t *testing.T) {
	got := dockerfileDirective(fixtureDockerfile, "HEALTHCHECK")
	if len(got) != 1 {
		t.Fatalf("expected one HEALTHCHECK, got %d: %q", len(got), got)
	}
	if !strings.Contains(got[0], "/healthz") || !strings.Contains(got[0], "--start-period=10s") {
		t.Fatalf("the continuation was not joined: %q", got[0])
	}
}

// `HEALTHCHECK NONE` is an image saying it has no check, not one probing a path
// called NONE.
func TestHealthcheckNoneIsNotComparedForAPath(t *testing.T) {
	root := writeRepo(t, "Dockerfile",
		"HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \\\n  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1",
		"HEALTHCHECK NONE")
	p := strings.Join(agreeWithRepoIn(t, root, fixtureRepository), "\n")
	if strings.Contains(p, "does not probe") {
		t.Fatalf("HEALTHCHECK NONE should not be read as a probe, got:\n%s", p)
	}
	if !strings.Contains(p, "no HEALTHCHECK the image runs") {
		t.Fatalf("an image that disabled its check is one that probes nothing, got:\n%s", p)
	}
}
