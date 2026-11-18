package main

import (
	"archive/tar"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

// certPEM is a self-signed certificate for host valid until notAfter.
func certPEM(t *testing.T, host string, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// caddyCertsTar is what `docker cp japode-caddy:/data/caddy/certificates -` streams:
// this service's host, another service's, and the ACME account key beside them.
func caddyCertsTar(t *testing.T, crt []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	const le = "certificates/acme-v02.api.letsencrypt.org-directory/"
	for _, e := range []struct {
		name string
		body []byte
	}{
		{le + "paste.example.org/paste.example.org.crt", crt},
		{le + "paste.example.org/paste.example.org.key", []byte("PASTE PRIVATE KEY")},
		{le + "paste.example.org/paste.example.org.json", []byte("{}")},
		{le + "other.example.org/other.example.org.key", []byte("OTHER PRIVATE KEY")},
		{"certificates/acme-v02.api.letsencrypt.org-directory/users/ops/ops.key", []byte("ACCOUNT KEY")},
	} {
		tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o600, Size: int64(len(e.body)), Typeflag: tar.TypeReg})
		tw.Write(e.body)
	}
	tw.Close()
	return buf.Bytes()
}

// certsDocker stands in for the source's docker: the edge's certificates on a cp of
// them, and otherwise what the volume and dump fixtures answer.
func certsDocker(t *testing.T, certs []byte) {
	t.Helper()
	was := exportDocker
	exportDocker = func(w io.Writer, name string, args ...string) error {
		switch {
		case args[0] == "inspect":
			io.WriteString(w, fakeMounts[args[len(args)-1]])
		case args[0] == "cp" && args[1] == "japode-caddy:/data/caddy/certificates":
			w.Write(certs)
		case args[0] == "cp":
			io.WriteString(w, volumeTar(t))
		case args[0] == "exec":
			io.WriteString(w, "CREATE TABLE `a` (\n);\nCREATE TABLE `b` (\n);\n-- Dump completed\n")
		case args[0] == "run":
			io.WriteString(w, "CONSISTENT")
		}
		return nil
	}
	t.Cleanup(func() { exportDocker = was })
}

// certsTree is the export fixture with a Caddy edge that names its container.
func certsTree(t *testing.T) string {
	t.Helper()
	compose := strings.Replace(exportEdgeCompose, "    image: caddy:2-alpine\n", "    image: caddy:2-alpine\n    container_name: japode-caddy\n", 1)
	return exportTree(t, map[string]string{
		"docker-compose.yml": compose + "x-edge:\n  email: ${ACME_EMAIL:?set ACME_EMAIL}\n",
	})
}

// JCT42: export carries the certificate of exactly the hosts the route answers for,
// as the source's configuration sets them, sealed: the key is in no clear byte of
// the archive, and the other service's certificate and the ACME account key stay.
func TestExportSealsOnlyTheRouteCertificates(t *testing.T) {
	root := certsTree(t)
	certsDocker(t, caddyCertsTar(t, certPEM(t, "paste.example.org", time.Now().Add(60*24*time.Hour))))
	id := testRecipient(t)

	f, d, notes := ExportCerts(root, "paste", "", t.TempDir(), []string{id.Recipient().String()}, exportDocker)
	if f == nil {
		t.Fatalf("nothing carried; notes: %v", notes)
	}
	if len(notes) != 0 {
		t.Fatalf("notes on a certificate well inside its life: %v", notes)
	}
	if d.Kind != "certs" || d.Source != "caddy" || d.Mount != "/data/caddy" || strings.Join(d.Hosts, ",") != "paste.example.org" {
		t.Fatalf("entry: %+v", d)
	}
	sealed := mustRead(t, f.src)
	if bytes.Contains(sealed, []byte("PRIVATE KEY")) || bytes.Contains(sealed, []byte("BEGIN CERTIFICATE")) {
		t.Fatal("the certificates were staged in clear")
	}
	plain, err := age.Decrypt(bytes.NewReader(sealed), id)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(plain)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, filepath.Base(h.Name))
	}
	if got := strings.Join(names, ","); got != "paste.example.org.crt,paste.example.org.key,paste.example.org.json" {
		t.Fatalf("carried: %s", got)
	}
}

// JCT42: a certificate inside its expiry floor, or a host the edge holds none for,
// is said at export and never refuses it; an nginx edge's are not carried, by name.
func TestExportCertsSaysWhatItCannotCarry(t *testing.T) {
	root := certsTree(t)
	certsDocker(t, caddyCertsTar(t, certPEM(t, "paste.example.org", time.Now().Add(5*24*time.Hour))))
	id := testRecipient(t)
	_, _, notes := ExportCerts(root, "paste", "", t.TempDir(), []string{id.Recipient().String()}, exportDocker)
	if !strings.Contains(strings.Join(notes, "\n"), "under the 14-day floor") {
		t.Fatalf("notes: %v", notes)
	}

	certsDocker(t, caddyCertsTar(t, nil)[:0])
	f, _, notes := ExportCerts(root, "paste", "", t.TempDir(), []string{id.Recipient().String()}, exportDocker)
	if f != nil || !strings.Contains(strings.Join(notes, "\n"), "holds none for paste.example.org") {
		t.Fatalf("an empty edge carried %v; notes: %v", f, notes)
	}

	nginx := exportTree(t, map[string]string{ProfileFile: "route:\n  path: nginx/sites.conf\n  kind: nginx\n"})
	if f, _, notes := ExportCerts(nginx, "paste", "", t.TempDir(), []string{id.Recipient().String()}, exportDocker); f != nil || !strings.Contains(strings.Join(notes, "\n"), "nginx edge's are not carried") {
		t.Fatalf("nginx: %v %v", f, notes)
	}
	if caddyHostDir("*.Example.org") != "wildcard_.example.org" {
		t.Fatal("caddyHostDir")
	}
}

// JCT42: install creates the edge, unstarted, and opens the certificates into its
// data volume before anything starts; an edge already on the target is left alone.
func TestInstallPutsTheCertificatesInBeforeTheEdgeStarts(t *testing.T) {
	root := certsTree(t)
	certsDocker(t, caddyCertsTar(t, certPEM(t, "paste.example.org", time.Now().Add(60*24*time.Hour))))
	id := testRecipient(t)
	archive := filepath.Join(t.TempDir(), "paste.tar.gz")
	if code := export([]string{"paste", "--root", root, "--recipient", id.Recipient().String(), "-o", archive}); code != 0 {
		t.Fatalf("export: exit %d", code)
	}
	t.Setenv("JCLOUD_IDENTITY", id.String())

	target := layTree(t, map[string]string{".env": "ACME_EMAIL=ops@example.org\n"})
	d := newFakeInstall()
	if code := runInstall(archive, target, "", 60, registryLogin{}, d.run); code != 0 {
		t.Fatalf("exit %d; calls:\n%s", code, strings.Join(d.calls, "\n"))
	}
	got := d.copied["japode-caddy:/data/caddy"]
	if len(got) != 3 || !strings.HasSuffix(got[1], "paste.example.org/paste.example.org.key") {
		t.Fatalf("copied into the edge: %v", got)
	}
	if d.bodies[got[1]] != "PASTE PRIVATE KEY" {
		t.Fatal("the key did not arrive opened")
	}
	var order []string
	for _, c := range d.calls {
		f := strings.Fields(c)
		switch {
		case f[1] == "compose" && contains(f, "create"):
			order = append(order, "create "+f[len(f)-1])
		case f[1] == "compose" && contains(f, "up"):
			order = append(order, "up "+f[len(f)-1])
		case f[1] == "cp":
			order = append(order, "cp "+f[len(f)-1])
		}
	}
	want := "create paste,cp japode-paste:/,create caddy,cp japode-caddy:/data/caddy,up paste-db,up caddy"
	if strings.Join(order, ",") != want {
		t.Fatalf("order:\n got %s\nwant %s", strings.Join(order, ","), want)
	}

	target = layTree(t, map[string]string{".env": "ACME_EMAIL=ops@example.org\n"})
	d = newFakeInstall()
	d.occupied["japode-caddy"] = true
	if code := runInstall(archive, target, "", 60, registryLogin{}, d.run); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, ok := d.copied["japode-caddy:/data/caddy"]; ok {
		t.Fatal("certificates were put into an edge that was already here")
	}
	if _, err := os.Stat(filepath.Join(target, ".env")); err != nil {
		t.Fatal(err)
	}
}
