package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// outsideTree is a site's own repository as FI194 lays it out (JCT44): japode.yml
// at the repository root, and the platform in jcloud/ beside it, whose jcloud.yml
// points at that manifest and keeps the compose file in jcloud/service/. It
// answers jcloud/, the platform root.
func outsideTree(t *testing.T) string {
	t.Helper()
	repo := layTree(t, map[string]string{
		"japode.yml":                        needyManifest,
		"jcloud/jcloud.yml":                 "manifest: ../japode.yml\nservice_dir: service\n",
		"jcloud/service/docker-compose.yml": needyCompose,
		"jcloud/service/backup.conf":        needyBackupConf,
		"jcloud/docker-compose.yml":         exportEdgeCompose,
		"jcloud/caddy/Caddyfile":            "import sites/*\n",
		"jcloud/caddy/extra.conf":           "# extra\n",
		"jcloud/caddy/sites/paste.caddy":    fixtureRoute,
		"jcloud/addons/mariadb/addon.yml":   needyAddon,
		"jcloud/.deploy-state/images.env":   "PASTE_IMAGE=ghcr.io/x/paste@sha256:aaaa\n",
		"jcloud/.env":                       "PASTE_DB_PASSWORD=hunter2\nPASTE_HOSTS=paste.example.org\n",
	})
	fakeExportDocker(t, nil)
	return filepath.Join(repo, "jcloud")
}

// JCT44: one literal manifest path may leave the root, the service's name comes
// from its name:, and its files are read where service_dir says. A template still
// may not leave the root, and a manifest outside it needs service_dir.
func TestASingleManifestOutsideTheRoot(t *testing.T) {
	root := outsideTree(t)
	prof, err := LoadProfile(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := prof.Services(root); !reflect.DeepEqual(got, []string{"paste"}) {
		t.Fatalf("services: %v", got)
	}
	if filepath.ToSlash(prof.ManifestPath("paste")) != "../japode.yml" || filepath.ToSlash(prof.ServiceDir("paste")) != "service" {
		t.Fatalf("manifest %s, service dir %s", prof.ManifestPath("paste"), prof.ServiceDir("paste"))
	}
	if !prof.ManifestOutside() || !prof.OwnsServiceDir() {
		t.Fatal("a single manifest outside the root with a service_dir")
	}

	for _, c := range []struct{ yml, want string }{
		{"manifest: ../services/{name}/japode.yml\n", "leaves the platform root"},
		{"manifest: ../japode.yml\n", "service_dir has to say"},
		{"manifest: ../japode.yml\nservice_dir: ../service\n", "service_dir is \"../service\", which leaves the platform root"},
		{"manifest: /srv/japode.yml\nservice_dir: s\n", "relative to the platform root"},
		{"manifest: ../*.yml\nservice_dir: s\n", "one file, not a pattern"},
	} {
		if _, err := ParseProfile([]byte(c.yml)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%q: got %v, want %q", c.yml, err, c.want)
		}
	}
	// A single manifest at the root itself owns no directory: carrying "." whole
	// would carry the tree.
	if p, err := ParseProfile([]byte("manifest: japode.yml\n")); err != nil || p.OwnsServiceDir() {
		t.Fatalf("manifest at the root: %v, owns %v", err, p != nil && p.OwnsServiceDir())
	}
	if got, err := mustProfile(t, "manifest: japode.yml\n").Services(t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("a missing single manifest is no service: %v %v", got, err)
	}
}

func mustProfile(t *testing.T, yml string) *Profile {
	t.Helper()
	p, err := ParseProfile([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// JCT44: export carries a manifest outside the root as jcloud-manifest.yml, never
// at a path that leaves the root, and names it in the index; install reads it from
// there and does not lay it down.
func TestExportAndInstallAManifestOutsideTheRoot(t *testing.T) {
	root := outsideTree(t)
	files, _, problems := ExportSet(root, "paste")
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	paths := exportPaths(files)
	for _, want := range []string{ExportManifestMember, "service/docker-compose.yml", "service/backup.conf", "caddy/sites/paste.caddy"} {
		if !contains(paths, want) {
			t.Fatalf("no %s in %v", want, paths)
		}
	}
	for _, p := range paths {
		if strings.HasPrefix(p, "../") {
			t.Fatalf("a member leaves the root: %s", p)
		}
	}

	id := testRecipient(t)
	archive := filepath.Join(t.TempDir(), "paste.tar.gz")
	if code := export([]string{"paste", "--root", root, "--recipient", id.Recipient().String(), "-o", archive}); code != 0 {
		t.Fatalf("export: exit %d", code)
	}
	t.Setenv("JCLOUD_IDENTITY", id.String())
	p, problems := planInstall(archive, t.TempDir(), "", newFakeInstall().run)
	if len(problems) > 0 {
		t.Fatalf("install: %v", problems)
	}
	if p.idx.Manifest != ExportManifestMember || p.m.Name != "paste" {
		t.Fatalf("index manifest %q, service %q", p.idx.Manifest, p.m.Name)
	}
	if _, laid := p.write[ExportManifestMember]; laid {
		t.Fatal("the manifest member was laid down")
	}
	if _, laid := p.write["service/docker-compose.yml"]; !laid {
		t.Fatal("the service's compose file was not laid down")
	}
	if !strings.Contains(strings.Join(p.notes, "\n"), "outside the source's root") {
		t.Fatalf("notes: %v", p.notes)
	}
}
