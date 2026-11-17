package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"filippo.io/age"
)

const exportEdgeCompose = `name: japode-cloud
services:
  caddy:
    image: caddy:2-alpine
    volumes:
      - ./caddy/Caddyfile:/etc/caddy/Caddyfile:ro
      - ./caddy/sites:/etc/caddy/sites:ro
      - ../outside/secret.txt:/etc/secret:ro
      - caddy-data:/data
      - type: bind
        source: ./caddy/extra.conf
        target: /etc/caddy/extra.conf
volumes:
  caddy-data:
`

// JCT32: a Japode-shaped tree with two services, so what export leaves out is as
// much the assertion as what it takes.
func exportTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"docker-compose.yml":                exportEdgeCompose,
		"caddy/Caddyfile":                   "{\n  email {$ACME_EMAIL}\n}\nimport sites/*\n",
		"caddy/extra.conf":                  "# extra\n",
		"caddy/sites/paste.caddy":           fixtureRoute,
		"caddy/sites/other.caddy":           "other\n",
		"services/paste/japode.yml":         needyManifest,
		"services/paste/docker-compose.yml": needyCompose,
		"services/paste/backup.conf":        needyBackupConf,
		"services/paste/init.sql":           "-- seed\n",
		"services/paste/.env":               "PASTE_DB_PASSWORD=hunter2\n",
		"services/other/japode.yml":         strings.Replace(fixtureManifest, "name: paste", "name: other", 1),
		"addons/mariadb/addon.yml":          needyAddon,
		"addons/redis/addon.yml":            "version: 1\n",
		".env.example":                      needyEnvExample,
		".deploy-state/images.env":          "OTHER_IMAGE=ghcr.io/x/other@sha256:bbbb\nPASTE_IMAGE=ghcr.io/x/paste@sha256:aaaa\n",
		".env":                              "# deployed\nPASTE_DB_PASSWORD='hunter2'\nPASTE_HOSTS=paste.example.org\nOTHER_SECRET=not-paste\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return layTree(t, files)
}

func exportPaths(files []exportFile) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// JCT32: the set is derived from jcloud.yml and the manifest, never listed: the
// service's directory without its configuration, its route alone, the addon its
// need names, the edge's compose file and the files it mounts from inside the tree,
// and the pin file cut down to this service's line.
func TestExportSetIsDerivedFromTheManifest(t *testing.T) {
	root := exportTree(t, nil)
	files, left, problems := ExportSet(root, "paste")
	if len(problems) > 0 {
		t.Fatalf("problems:\n  %s", strings.Join(problems, "\n  "))
	}
	want := []string{
		".deploy-state/images.env",
		"addons/mariadb/addon.yml",
		"caddy/Caddyfile",
		"caddy/extra.conf",
		"caddy/sites/paste.caddy",
		"docker-compose.yml",
		"services/paste/backup.conf",
		"services/paste/docker-compose.yml",
		"services/paste/init.sql",
		"services/paste/japode.yml",
	}
	if got := exportPaths(files); !reflect.DeepEqual(got, want) {
		t.Fatalf("files:\n got %v\nwant %v", got, want)
	}
	if !reflect.DeepEqual(left, []string{"services/paste/.env"}) {
		t.Fatalf("left: %v", left)
	}
	for _, f := range files {
		switch f.Path {
		case ".deploy-state/images.env":
			if string(f.data) != "PASTE_IMAGE=ghcr.io/x/paste@sha256:aaaa\n" || !f.Partial || !f.Shared {
				t.Fatalf("pin member: %q partial=%t shared=%t", f.data, f.Partial, f.Shared)
			}
		case "docker-compose.yml", "caddy/Caddyfile", "addons/mariadb/addon.yml":
			if !f.Shared {
				t.Fatalf("%s should be marked shared", f.Path)
			}
		case "services/paste/japode.yml", "caddy/sites/paste.caddy":
			if f.Shared {
				t.Fatalf("%s is the service's own", f.Path)
			}
		}
	}
}

// JCT32: an nginx edge's one route file is carried and marked shared, and
// jcloud.yml travels with the tree it describes.
func TestExportNginxRouteIsShared(t *testing.T) {
	root := exportTree(t, map[string]string{
		"jcloud.yml":       "route:\n  path: nginx/sites.conf\n  kind: nginx\n",
		"nginx/sites.conf": "server {}\n",
	})
	files, _, problems := ExportSet(root, "paste")
	if len(problems) > 0 {
		t.Fatalf("problems:\n  %s", strings.Join(problems, "\n  "))
	}
	shared := map[string]bool{}
	for _, f := range files {
		shared[f.Path] = f.Shared
	}
	if s, ok := shared["nginx/sites.conf"]; !ok || !s {
		t.Fatalf("nginx route: present=%t shared=%t", ok, s)
	}
	if s, ok := shared["jcloud.yml"]; !ok || !s {
		t.Fatalf("jcloud.yml: present=%t shared=%t", ok, s)
	}
}

// JCT32: every problem at once, and no archive from a set that has one.
func TestExportRefusesAnIncompleteService(t *testing.T) {
	root := exportTree(t, map[string]string{".deploy-state/images.env": "OTHER_IMAGE=x\n"})
	if err := os.Remove(filepath.Join(root, "addons", "mariadb", "addon.yml")); err != nil {
		t.Fatal(err)
	}
	_, _, problems := ExportSet(root, "paste")
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"addons/mariadb/addon.yml: is not there", "no PASTE_IMAGE="} {
		if !strings.Contains(joined, want) {
			t.Fatalf("no %q in:\n%s", want, joined)
		}
	}

	if _, _, problems := ExportSet(root, "nope"); len(problems) != 1 || !strings.Contains(problems[0], `no service "nope"`) {
		t.Fatalf("unknown service: %v", problems)
	}

	out := filepath.Join(t.TempDir(), "paste.tar.gz")
	if code := export([]string{"paste", "--root", root, "--recipient", testRecipient(t).Recipient().String(), "-o", out}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused export left %s behind", out)
	}
}

// JCT32: what export writes, ReadExport reads back member for member, with the
// index first and a checksum per file.
func TestExportRoundTrips(t *testing.T) {
	root := exportTree(t, nil)
	id := testRecipient(t)
	idx, got := exportAndRead(t, root, id.Recipient().String())
	if idx.Schema != ExportSchema || idx.Service != "paste" || idx.Created == "" || idx.Jcloud == "" {
		t.Fatalf("index: %+v", idx)
	}
	if !reflect.DeepEqual(idx.Left, []string{"services/paste/.env"}) {
		t.Fatalf("left: %v", idx.Left)
	}
	if string(got["services/paste/japode.yml"]) != needyManifest {
		t.Fatal("the manifest did not come back as it was")
	}
	for name, data := range got {
		if bytes.Contains(data, []byte("hunter2")) {
			t.Fatalf("%s carries a configuration value in clear", name)
		}
	}
}

func testRecipient(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func exportAndRead(t *testing.T, root string, extra ...string) (*ExportManifest, map[string][]byte) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "paste.tar.gz")
	args := []string{"paste", "--root", root, "-o", out}
	for _, r := range extra {
		args = append(args, "--recipient", r)
	}
	if code := export(args); code != 0 {
		t.Fatalf("exit %d", code)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	idx, got, err := ReadExport(f)
	if err != nil {
		t.Fatal(err)
	}
	return idx, got
}

// openedEnv opens every value of an exported jcloud-env.age with id.
func openedEnv(t *testing.T, data []byte, id age.Identity) map[string]string {
	t.Helper()
	f := ParseEnvFile(data)
	out := map[string]string{}
	for _, k := range f.Keys() {
		b64, _ := f.Get(k)
		v, err := open(b64, []age.Identity{id})
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		out[k] = string(v)
	}
	return out
}

// JCT33: exactly the keys the service reads travel, from the deployed .env, sealed
// to every recipient: the manifest's non-fixed variables and the route's {$…}
// placeholders, and not another service's key in the same .env.
func TestExportCarriesTheServicesEnvSealed(t *testing.T) {
	root := exportTree(t, nil)
	a, b := testRecipient(t), testRecipient(t)
	idx, got := exportAndRead(t, root, a.Recipient().String(), b.Recipient().String())
	if idx.Env != ExportEnvMember {
		t.Fatalf("index names env member %q", idx.Env)
	}
	want := map[string]string{"PASTE_DB_PASSWORD": "hunter2", "PASTE_HOSTS": "paste.example.org"}
	for _, id := range []age.Identity{a, b} {
		if env := openedEnv(t, got[ExportEnvMember], id); !reflect.DeepEqual(env, want) {
			t.Fatalf("env: got %v, want %v", env, want)
		}
	}
	if len(idx.EnvMissing) != 0 {
		t.Fatalf("missing: %v", idx.EnvMissing)
	}
}

// JCT33: with no .env deployed, the values come from .env.age opened with the
// identity, and a key read with no value and no default is listed, not invented.
// A route placeholder with a default is not missing.
func TestExportEnvFromTheSealedStore(t *testing.T) {
	store := testRecipient(t)
	sealed, err := seal([]byte("from-age"), []age.Recipient{store.Recipient()})
	if err != nil {
		t.Fatal(err)
	}
	root := exportTree(t, map[string]string{
		EnvFileName: "recipient " + store.Recipient().String() + "\nOTHER_SECRET=" + sealed + "\n",
	})
	if err := os.Remove(filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	// The secret lives under the service's key in the store this time.
	f := ParseEnvFile(mustRead(t, filepath.Join(root, EnvFileName)))
	f.Set("PASTE_DB_PASSWORD", sealed)
	if err := os.WriteFile(filepath.Join(root, EnvFileName), f.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JCLOUD_IDENTITY", store.String())

	target := testRecipient(t)
	_, got := exportAndRead(t, root, target.Recipient().String())
	if env := openedEnv(t, got[ExportEnvMember], target); !reflect.DeepEqual(env, map[string]string{"PASTE_DB_PASSWORD": "from-age"}) {
		t.Fatalf("env: %v", env)
	}

	// Unset now, and the manifest gives it no default: reported.
	f.Unset("PASTE_DB_PASSWORD")
	if err := os.WriteFile(filepath.Join(root, EnvFileName), f.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	idx, _ := exportAndRead(t, root, target.Recipient().String())
	if !reflect.DeepEqual(idx.EnvMissing, []string{"PASTE_DB_PASSWORD"}) {
		t.Fatalf("missing: %v", idx.EnvMissing)
	}
}

// JCT33: no recipient is the arguments being wrong, and nothing is written.
func TestExportWithoutARecipientIsRefused(t *testing.T) {
	root := exportTree(t, nil)
	out := filepath.Join(t.TempDir(), "paste.tar.gz")
	if code := export([]string{"paste", "--root", root, "-o", out}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if code := export([]string{"paste", "--root", root, "--recipient", "not-a-key", "-o", out}); code != 1 {
		t.Fatalf("a malformed recipient: exit %d, want 1", code)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused export left %s behind", out)
	}
}

// JCT32: an archive is refused when its schema is unknown, a member was changed
// after it was written, or a member is not listed.
func TestReadExportRefusesWhatItDoesNotKnow(t *testing.T) {
	files := []exportFile{newExportFile("a.yml", []byte("a\n"), false, false)}
	base := ExportManifest{Schema: ExportSchema, Service: "a", Created: "2026-11-17T00:00:00Z"}

	write := func(idx ExportManifest, members map[string]string) []byte {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		idx.Files = []ExportMember{files[0].ExportMember}
		index, _ := json.Marshal(idx)
		put := func(name, body string) {
			tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
			tw.Write([]byte(body))
		}
		put(ExportIndex, string(index))
		for _, name := range []string{"a.yml", "b.yml"} {
			if body, ok := members[name]; ok {
				put(name, body)
			}
		}
		tw.Close()
		zw.Close()
		return buf.Bytes()
	}

	var good bytes.Buffer
	if err := WriteExport(&good, base, files); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadExport(bytes.NewReader(good.Bytes())); err != nil {
		t.Fatalf("a good archive was refused: %v", err)
	}

	future := base
	future.Schema = ExportSchema + 1
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"unknown schema", write(future, map[string]string{"a.yml": "a\n"}), "schema"},
		{"changed member", write(base, map[string]string{"a.yml": "b\n"}), "sha256"},
		{"unlisted member", write(base, map[string]string{"a.yml": "a\n", "b.yml": "b\n"}), "not listed"},
		{"missing member", write(base, nil), "not in the archive"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := ReadExport(bytes.NewReader(c.data))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error naming %q", err, c.want)
			}
		})
	}
}
