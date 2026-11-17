package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	fakeExportDocker(t, nil)
	return layTree(t, files)
}

// fakeExportDocker stands in for the docker export runs, recording each call. fail
// names a verb (cp, exec, run, stop) that fails.
func fakeExportDocker(t *testing.T, fail map[string]string) *[]string {
	t.Helper()
	var calls []string
	was := exportDocker
	exportDocker = func(w io.Writer, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if reason, ok := fail[args[0]]; ok {
			return errors.New(reason)
		}
		switch args[0] {
		case "cp":
			fmt.Fprintf(w, "TAR of %s", args[1])
		case "exec":
			fmt.Fprint(w, "-- MariaDB dump\nCREATE TABLE `a` (\n);\nCREATE TABLE `b` (\n);\n-- Dump completed on 2026-11-17\n")
		case "run":
			fmt.Fprint(w, "SQLite format 3")
		}
		return nil
	}
	t.Cleanup(func() { exportDocker = was })
	return &calls
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
	return exportAndReadWith(t, root, nil, extra...)
}

func exportAndReadWith(t *testing.T, root string, flags []string, extra ...string) (*ExportManifest, map[string][]byte) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "paste.tar.gz")
	args := append([]string{"paste", "--root", root, "-o", out}, flags...)
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

// JCT34: each volume is the tar docker cp streams from the service's container,
// each sqlite file a VACUUM INTO copy from the project's volume, each mysql need a
// dump that ran to the end, with its table count; live unless --stop, which stops
// and starts the service around the volume copy and never the database.
func TestExportCarriesTheState(t *testing.T) {
	manifest := strings.Replace(needyManifest, "    path: /data\n", "    path: /data\n    sqlite: [app.db]\n", 1)
	root := exportTree(t, map[string]string{"services/paste/japode.yml": manifest})
	calls := fakeExportDocker(t, nil)
	id := testRecipient(t)

	idx, got := exportAndRead(t, root, id.Recipient().String())
	if !idx.DataLive {
		t.Fatal("volumes copied without --stop are not said to be live")
	}
	if _, held := got["data/paste-data.tar"]; held {
		t.Fatal("ReadExport held a data member in memory")
	}
	sums := map[string]string{}
	for _, f := range idx.Files {
		sums[f.Path] = f.Sha256
	}
	if sums["data/paste-data.tar"] != newExportFile("x", []byte("TAR of japode-paste:/data"), false, false).Sha256 {
		t.Fatal("the volume member is not the tar docker cp streamed")
	}
	dump := "-- MariaDB dump\nCREATE TABLE `a` (\n);\nCREATE TABLE `b` (\n);\n-- Dump completed on 2026-11-17\n"
	want := []ExportData{
		{Member: "data/paste-db.sql", Kind: "mysql", Source: "paste-db", Bytes: int64(len(dump)), Tables: 2},
		{Member: "data/paste-data.tar", Kind: "volume", Source: "paste-data", Mount: "/data", Bytes: int64(len("TAR of japode-paste:/data"))},
		{Member: "data/paste-data/app.db", Kind: "sqlite", Source: "paste-data", File: "app.db", Bytes: int64(len("SQLite format 3"))},
	}
	if !reflect.DeepEqual(idx.Data, want) {
		t.Fatalf("data:\n got %+v\nwant %+v", idx.Data, want)
	}
	joined := strings.Join(*calls, "\n")
	for _, w := range []string{"docker exec -i japode-paste-db sh -c", "japode-cloud_paste-data:/v"} {
		if !strings.Contains(joined, w) {
			t.Fatalf("no %q in calls:\n%s", w, joined)
		}
	}
	if strings.Contains(joined, "docker stop") {
		t.Fatal("stopped the service without --stop")
	}

	*calls = nil
	idx, _ = exportAndReadWith(t, root, []string{"--stop"}, id.Recipient().String())
	if idx.DataLive {
		t.Fatal("--stop still says live")
	}
	var order []string
	for _, c := range *calls {
		order = append(order, strings.Join(strings.Fields(c)[:2], " "))
	}
	if got := strings.Join(order, ","); got != "docker exec,docker stop,docker cp,docker run,docker start" {
		t.Fatalf("order: %s", got)
	}
}

// JCT34: a dump that did not run to the end, or a database that is not up, refuses
// the export, and the stopped service is started again whatever happened.
func TestExportRefusesACutDump(t *testing.T) {
	root := exportTree(t, nil)
	calls := fakeExportDocker(t, map[string]string{"cp": "no such container"})
	out := filepath.Join(t.TempDir(), "paste.tar.gz")
	if code := export([]string{"paste", "--root", root, "--stop", "--recipient", testRecipient(t).Recipient().String(), "-o", out}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if last := (*calls)[len(*calls)-1]; !strings.HasPrefix(last, "docker start japode-paste") {
		t.Fatalf("the service was not started again: %v", *calls)
	}
	entries, _ := os.ReadDir(filepath.Dir(out))
	if len(entries) != 0 {
		t.Fatalf("a refused export left %v behind", entries)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cut.sql"), []byte("CREATE TABLE `a` (\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tables, complete, err := scanDump(filepath.Join(dir, "cut.sql")); err != nil || complete || tables != 1 {
		t.Fatalf("cut dump: tables=%d complete=%t err=%v", tables, complete, err)
	}
}

// JCT34: a shared need's database is the platform's, and saying so is all export
// does with it.
func TestExportSaysASharedNeedIsNotCarried(t *testing.T) {
	manifest := strings.Replace(needyManifest, "    type: mariadb\n", "    type: mariadb\n    shared: mariadb\n", 1)
	root := exportTree(t, map[string]string{"services/paste/japode.yml": manifest})
	_, data, notes, problems := ExportState(root, "paste", t.TempDir(), false, exportDocker)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	for _, d := range data {
		if d.Kind == "mysql" {
			t.Fatalf("dumped the platform's database: %+v", d)
		}
	}
	if !strings.Contains(strings.Join(notes, "\n"), "needs[db]: its database is mariadb") {
		t.Fatalf("notes: %v", notes)
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
