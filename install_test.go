package main

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// fakeInstall stands in for docker on the target: occupied names the containers
// and volumes already there, tables what the count answers, and each call is
// recorded with the tar entry names a `docker cp -` received.
type fakeInstall struct {
	occupied map[string]bool
	tables   string
	calls    []string
	copied   map[string][]string // destination -> entry names
	bodies   map[string]string   // entry name -> content
	fed      string
}

func (f *fakeInstall) run(stdin io.Reader, stdout io.Writer, name string, args ...string) error {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	switch {
	case args[0] == "ps", args[0] == "volume":
		filter := strings.TrimSuffix(strings.TrimPrefix(strings.SplitN(args[len(args)-1], "=", 2)[1], "^"), "$")
		if f.occupied[strings.TrimPrefix(filter, "/")] {
			io.WriteString(stdout, "abc123\n")
		}
	case args[0] == "cp":
		tr := tar.NewReader(stdin)
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			body, _ := io.ReadAll(tr)
			f.copied[args[len(args)-1]] = append(f.copied[args[len(args)-1]], h.Name)
			f.bodies[h.Name] = string(body)
		}
	case args[0] == "exec" && args[1] == "-i":
		b, _ := io.ReadAll(stdin)
		f.fed = string(b)
	case args[0] == "exec":
		io.WriteString(stdout, f.tables)
	}
	return nil
}

// A volume tar as `docker cp japode-paste:/data -` streams it: a live sqlite file
// with its -wal and -shm beside it, and an upload.
func volumeTar(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range []struct{ name, body string }{
		{"data/app.db", "LIVE"}, {"data/app.db-wal", "WAL"}, {"data/app.db-shm", "SHM"}, {"data/upload.png", "PNG"},
	} {
		tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o640, Uid: 33, Gid: 33, Size: int64(len(e.body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(e.body))
	}
	tw.Close()
	return buf.String()
}

// installArchive exports the fixture service with a docker standing in for the
// source, and answers the archive and the identity its configuration opens with.
func installArchive(t *testing.T) (string, *age.X25519Identity) {
	t.Helper()
	manifest := strings.Replace(needyManifest, "    path: /data\n", "    path: /data\n    sqlite: [app.db]\n", 1)
	root := exportTree(t, map[string]string{
		"services/paste/japode.yml": manifest,
		"docker-compose.yml":        exportEdgeCompose + "x-edge:\n  email: ${ACME_EMAIL:?set ACME_EMAIL}\n",
	})
	tarBody := volumeTar(t)
	was := exportDocker
	exportDocker = func(w io.Writer, name string, args ...string) error {
		switch args[0] {
		case "inspect":
			io.WriteString(w, fakeMounts[args[len(args)-1]])
		case "cp":
			io.WriteString(w, tarBody)
		case "exec":
			io.WriteString(w, "CREATE TABLE `a` (\n);\nCREATE TABLE `b` (\n);\n-- Dump completed\n")
		case "run":
			io.WriteString(w, "CONSISTENT")
		}
		return nil
	}
	defer func() { exportDocker = was }()
	id := testRecipient(t)
	out := filepath.Join(t.TempDir(), "paste.tar.gz")
	if code := export([]string{"paste", "--root", root, "--recipient", id.Recipient().String(), "-o", out}); code != 0 {
		t.Fatalf("export: exit %d", code)
	}
	return out, id
}

func newFakeInstall() *fakeInstall {
	return &fakeInstall{occupied: map[string]bool{}, tables: "2\n", copied: map[string][]string{}, bodies: map[string]string{}}
}

// JCT36: on an empty place the files are laid down, the configuration merged at
// 0600 beside the target's own, the pin added, the volume copied into its created
// container with the sqlite copy in place of the live file and its -wal and -shm
// dropped, the database started and fed before the service, and the service
// started last with the edge.
func TestInstallRestoresBeforeItStarts(t *testing.T) {
	archive, id := installArchive(t)
	target := layTree(t, map[string]string{".env": "ACME_EMAIL=ops@example.org\n"})
	t.Setenv("JCLOUD_IDENTITY", id.String())
	d := newFakeInstall()

	if code := runInstall(archive, target, "", 60, d.run); code != 0 {
		t.Fatalf("exit %d; calls:\n%s", code, strings.Join(d.calls, "\n"))
	}

	if got := string(mustRead(t, filepath.Join(target, "services", "paste", "japode.yml"))); !strings.Contains(got, "sqlite: [app.db]") {
		t.Fatal("the manifest was not laid down")
	}
	env := string(mustRead(t, filepath.Join(target, ".env")))
	if !strings.HasPrefix(env, "ACME_EMAIL=ops@example.org\n") || !strings.Contains(env, "PASTE_DB_PASSWORD=hunter2\n") {
		t.Fatalf(".env:\n%s", env)
	}
	if info, _ := os.Stat(filepath.Join(target, ".env")); info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf(".env is %v", info.Mode())
	}
	if pins := string(mustRead(t, filepath.Join(target, ".deploy-state", "images.env"))); pins != "PASTE_IMAGE=ghcr.io/x/paste@sha256:aaaa\n" {
		t.Fatalf("pins: %q", pins)
	}

	names := d.copied["japode-paste:/"]
	if strings.Join(names, ",") != "data/app.db,data/upload.png" {
		t.Fatalf("copied into japode-paste: %v", names)
	}
	if d.bodies["data/app.db"] != "CONSISTENT" {
		t.Fatalf("the live sqlite file went in, not the copy: %q", d.bodies["data/app.db"])
	}
	if !strings.Contains(d.fed, "-- Dump completed") {
		t.Fatal("the dump was not fed in")
	}

	var order []string
	for _, c := range d.calls {
		f := strings.Fields(c)
		switch {
		case f[1] == "compose":
			for _, w := range f {
				if w == "create" || w == "up" {
					order = append(order, w+" "+f[len(f)-1])
					break
				}
			}
		case f[1] == "cp", f[1] == "exec":
			order = append(order, f[1])
		}
	}
	if got := strings.Join(order, ","); got != "create paste,cp,up paste-db,exec,exec,up caddy" {
		t.Fatalf("order: %s\n%s", got, strings.Join(d.calls, "\n"))
	}
}

// JCT36: a place that is not empty for the service, or a configuration missing a
// key a compose file requires, is refused before anything is written.
func TestInstallRefusesBeforeWriting(t *testing.T) {
	archive, id := installArchive(t)
	t.Setenv("JCLOUD_IDENTITY", id.String())

	target := t.TempDir()
	d := newFakeInstall()
	d.occupied["japode-paste-db"] = true
	d.occupied["japode-cloud_paste-data"] = true
	if code := runInstall(archive, target, "", 60, d.run); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	_, problems := planInstall(archive, target, "", d.run)
	joined := strings.Join(problems, "\n")
	for _, w := range []string{"japode-paste-db: a container", "japode-cloud_paste-data: the volume", "requires ACME_EMAIL"} {
		if !strings.Contains(joined, w) {
			t.Fatalf("no %q in:\n%s", w, joined)
		}
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Fatalf("a refused install wrote %v", entries)
	}

	// The service's own file already there is refused too; a shared one is kept.
	target = layTree(t, map[string]string{
		".env":                      "ACME_EMAIL=ops@example.org\n",
		"services/paste/japode.yml": "other\n",
		"caddy/Caddyfile":           "the target's own\n",
	})
	_, problems = planInstall(archive, target, "", newFakeInstall().run)
	if len(problems) != 1 || !strings.Contains(problems[0], "services/paste/japode.yml: already here") {
		t.Fatalf("problems: %v", problems)
	}
}

// JCT36: a database that comes back with fewer tables than the dump carried is a
// failed install, and the service is never started on it.
func TestInstallRefusesAShortRestore(t *testing.T) {
	archive, id := installArchive(t)
	t.Setenv("JCLOUD_IDENTITY", id.String())
	target := layTree(t, map[string]string{".env": "ACME_EMAIL=ops@example.org\n"})
	d := newFakeInstall()
	d.tables = "1\n"
	if code := runInstall(archive, target, "", 60, d.run); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if last := d.calls[len(d.calls)-1]; strings.Contains(last, " up ") && strings.HasSuffix(last, "caddy") {
		t.Fatal("the service was started on a short restore")
	}
}
