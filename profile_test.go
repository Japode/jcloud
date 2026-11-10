package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// layTree writes files under a fresh root. Relative paths are slash-separated.
func layTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
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

// JCT2: a root with no jcloud.yml is read exactly as every literal used to read it,
// so the Japode platform changes no byte. Each value is the one the code spelled out
// before the profile existed.
func TestNoProfileIsTheJapodeLayout(t *testing.T) {
	prof, err := LoadProfile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Name: "fespsp-iwl"}
	for _, c := range []struct{ what, got, want string }{
		{"manifest", filepath.ToSlash(prof.ManifestPath("paste")), "services/paste/japode.yml"},
		{"service dir", filepath.ToSlash(prof.ServiceDir("paste")), "services/paste"},
		{"route", filepath.ToSlash(prof.RouteFile("paste")), "caddy/sites/paste.caddy"},
		{"container", prof.Container("paste-db"), "japode-paste-db"},
		{"image var", prof.ImageVarOf(m), "FESPSP_IWL_IMAGE"},
		{"pin file", prof.PinFile, ".deploy-state/images.env"},
		{"addon", filepath.ToSlash(prof.AddonFile("mariadb")), "addons/mariadb/addon.yml"},
		{"deploy", prof.DeployCommand("skillgist", "ghcr.io/japode/skillgist@sha256:1"),
			"DEPLOY_SERVICE=skillgist DEPLOY_IMAGE=ghcr.io/japode/skillgist@sha256:1 bash scripts/deploy.sh skillgist"},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.what, c.got, c.want)
		}
	}
	if prof.RouteKind != "caddy" {
		t.Errorf("route kind: got %q", prof.RouteKind)
	}
}

// An empty jcloud.yml says nothing, which is every default, and is not a decode error.
func TestAnEmptyProfileIsTheDefault(t *testing.T) {
	prof, err := ParseProfile(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prof, DefaultProfile()) {
		t.Fatalf("got %+v", prof)
	}
}

// A platform may name its containers by short name alone, so an empty prefix is a
// value and not "left out".
func TestAnEmptyContainerPrefixIsAValue(t *testing.T) {
	prof, err := ParseProfile([]byte("container_prefix: \"\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := prof.Container("turing"); got != "turing" {
		t.Fatalf("got %q", got)
	}
}

// Every mistake is reported at once, and a misspelt key is one: it would leave the
// Japode value standing and report the platform's real files as missing.
func TestABadProfileIsRefusedWhole(t *testing.T) {
	_, err := ParseProfile([]byte(`
manifest: services/*/{name}.yml
route:
  path: /etc/nginx/site.conf
  kind: traefik
image_var: IMAGE
pin_file: ../state/images.env
addon: addons/addon.yml
`))
	if err == nil {
		t.Fatal("expected the profile to be refused")
	}
	for _, want := range []string{
		"{name} is its only wildcard",
		"route.path is \"/etc/nginx/site.conf\", and a path here is relative",
		"route.kind is \"traefik\"",
		"image_var is \"IMAGE\"",
		"pin_file is \"../state/images.env\", which leaves the platform root",
		"addon is \"addons/addon.yml\"",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}

	if _, err := ParseProfile([]byte("manifests: x/{name}.yml\n")); err == nil || !strings.Contains(err.Error(), "manifests") {
		t.Errorf("a misspelt key should be refused by name, got %v", err)
	}
	if _, err := ParseProfile([]byte("manifest: services/japode.yml\n")); err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Errorf("a manifest path without {name} should be refused, got %v", err)
	}
}

// The service is read back out of the template, so a layout that keeps every
// manifest in one directory names services by file, not by directory.
func TestServicesAreReadBackThroughTheTemplate(t *testing.T) {
	root := layTree(t, map[string]string{
		"jcloud.yml":                    "manifest: manifests/{name}.manifest.yml\n",
		"manifests/turing.manifest.yml": "",
		"manifests/shio.manifest.yml":   "",
		"manifests/README.md":           "",
	})
	prof, err := LoadProfile(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := prof.Services(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"shio", "turing"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The layout a second platform might keep, with every fact the Japode fixture
// carries moved and renamed to match it.
const otherProfile = `manifest: apps/{name}/service.yml
route:
  path: edge/{name}.caddy
  kind: caddy
container_prefix: viglet-
image_var: VIGLET_{NAME}_IMAGE
pin_file: state/pins.env
deploy: ./deploy {name} {image}
addon: providers/{type}.yml
`

func otherLayout(compose string) string {
	return strings.NewReplacer(
		"container_name: japode-", "container_name: viglet-",
		"${PASTE_IMAGE", "${VIGLET_PASTE_IMAGE",
	).Replace(compose)
}

// JCT2's point: the same facts under other names agree once jcloud.yml says where
// they are, and the very same tree without it is refused on the paths alone.
func TestAProfiledTreeAgrees(t *testing.T) {
	files := map[string]string{
		"jcloud.yml":                    otherProfile,
		"apps/paste/service.yml":        fixtureManifest,
		"apps/paste/docker-compose.yml": otherLayout(fixtureCompose),
		"apps/paste/backup.conf":        fixtureBackupConf,
		"edge/paste.caddy":              fixtureRoute,
		".env.example":                  fixtureEnvExample,
	}
	m, err := Parse([]byte(fixtureManifest))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.AgreeWith(layTree(t, files), []byte(fixtureAdapted)); len(p) != 0 {
		t.Fatalf("the profiled tree should agree, got:\n  %s", strings.Join(p, "\n  "))
	}

	delete(files, "jcloud.yml")
	p := strings.Join(m.AgreeWith(layTree(t, files), []byte(fixtureAdapted)), "\n")
	for _, want := range []string{"services/paste/docker-compose.yml", "caddy/sites/paste.caddy"} {
		if !strings.Contains(p, want) {
			t.Errorf("without jcloud.yml the Japode path %s should be the one reported, got:\n%s", want, p)
		}
	}
}

// The container prefix and the provider's path reach the needs check too: a need's
// container is the prefix plus the derived name, and its provider is where the
// profile says.
func TestAProfiledNeedAgrees(t *testing.T) {
	m, err := Parse([]byte(needyManifest))
	if err != nil {
		t.Fatal(err)
	}
	root := layTree(t, map[string]string{
		"jcloud.yml":                    otherProfile,
		"apps/paste/service.yml":        needyManifest,
		"apps/paste/docker-compose.yml": otherLayout(needyCompose),
		"apps/paste/backup.conf":        needyBackupConf,
		"edge/paste.caddy":              fixtureRoute,
		".env.example":                  needyEnvExample,
		"providers/mariadb.yml":         needyAddon,
	})
	if p := m.AgreeWith(root, []byte(fixtureAdapted)); len(p) != 0 {
		t.Fatalf("the profiled needy tree should agree, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// A profile that cannot be read is the one answer agree gives, rather than every
// file reported missing from a layout nobody declared.
func TestAnUnreadableProfileIsTheOneAnswer(t *testing.T) {
	root := layTree(t, map[string]string{"jcloud.yml": "route:\n  kind: traefik\n"})
	m, err := Parse([]byte(fixtureManifest))
	if err != nil {
		t.Fatal(err)
	}
	p := m.AgreeWith(root, []byte(fixtureAdapted))
	if len(p) != 1 || !strings.Contains(p[0], "jcloud.yml") {
		t.Fatalf("expected one answer naming jcloud.yml, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// up reads the pin under the profile's variable, and survey finds the services
// through the profile's template.
func TestUpAndSurveyReadTheProfile(t *testing.T) {
	prof, err := ParseProfile([]byte(otherProfile))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse([]byte(fixtureManifest))
	if err != nil {
		t.Fatal(err)
	}
	if got := pinnedDigest(prof, m, "PASTE_IMAGE=wrong\nVIGLET_PASTE_IMAGE=ghcr.io/x/paste@sha256:1\n"); got != "ghcr.io/x/paste@sha256:1" {
		t.Errorf("pinned: got %q", got)
	}
	if got := prof.DeployCommand("paste", "img@sha256:1"); got != "./deploy paste img@sha256:1" {
		t.Errorf("deploy: got %q", got)
	}

	root := layTree(t, map[string]string{
		"jcloud.yml":             otherProfile,
		"docker-compose.yml":     "services:\n  edge: {}\n",
		"apps/paste/service.yml": fixtureManifest,
	})
	d, err := DeclaredIn(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.Services, []string{"paste"}) || !reflect.DeepEqual(d.Others, []string{"edge"}) {
		t.Fatalf("got %+v", d)
	}
}
