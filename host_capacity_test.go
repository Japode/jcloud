package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"
)

// JCT25: a certificate as an edge writes one, expiring when the case says.
func pemCert(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "paste.example.com"},
		NotBefore:    notAfter.Add(-90 * 24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

const capacityProfile = `
host:
  disks: [/, /var/lib/docker]
  disk_free: 15%
  certs: [caddy/certificates/*/*.crt]
  cert_days: 14
`

func TestCapacityIsReadFromTheProfile(t *testing.T) {
	prof, err := ParseProfile([]byte(capacityProfile))
	if err != nil {
		t.Fatal(err)
	}
	want := Host{Disks: []string{"/", "/var/lib/docker"}, DiskFree: "15%", Certs: []string{"caddy/certificates/*/*.crt"}, CertDays: 14}
	if !reflect.DeepEqual(prof.Host, want) {
		t.Fatalf("got %+v", prof.Host)
	}

	_, err = ParseProfile([]byte("host:\n  disks: [/]\n  certs: [\"[\"]\n"))
	for _, w := range []string{"host.disk_free is not", `"[" is not a path or a glob`, "host.cert_days is not a positive"} {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("want %q in %v", w, err)
		}
	}
	if _, err := ParseProfile([]byte("host:\n  disks: [/]\n  disk_free: 15\n")); err == nil || !strings.Contains(err.Error(), "a floor is a percentage") {
		t.Errorf("got %v", err)
	}
}

func TestObserveReadsDisksAndCertificates(t *testing.T) {
	clock := time.Unix(1_800_000_000, 0)
	atClock(t, clock.Unix())
	soon := clock.Add(5 * 24 * time.Hour)
	root := layTree(t, map[string]string{
		"caddy/certificates/acme/paste.example.com.crt": pemCert(t, soon),
		"caddy/certificates/acme/broken.crt":            "not a certificate",
	})
	df := func(name string, args ...string) (string, error) {
		switch {
		case name == "df" && args[len(args)-1] == "/":
			return "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 100000 91000 9000 91% /\n", nil
		case name == "df":
			return "", errors.New("df: /var/lib/docker: No such file or directory")
		}
		return "", errors.New("observe ran " + name)
	}
	h := Host{Disks: []string{"/", "/var/lib/docker"}, DiskFree: "15%",
		Certs: []string{"caddy/certificates/*/*.crt", "/etc/none/*.pem"}, CertDays: 14}
	lines := observeHost(root, h, df)
	want := []string{
		"clock host 1800000000",
		"disk / 9",
		"disk /var/lib/docker unknown",
		"cert caddy/certificates/acme/broken.crt unreadable",
		"cert caddy/certificates/acme/paste.example.com.crt 1800432000", // the clock plus 5 days
		"cert /etc/none/*.pem none",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("got\n  %s\nwant\n  %s", strings.Join(lines, "\n  "), strings.Join(want, "\n  "))
	}

	got := surveyHost(h, ParseObserved(strings.Join(lines, "\n")))
	says := map[string]string{}
	for _, d := range got {
		says[d.Service] += d.Says
	}
	for subject, w := range map[string]string{
		"/":                                  "has 9% free, 6 points under the 15% floor",
		"/var/lib/docker":                    "df did not answer",
		"caddy/certificates/acme/broken.crt": "could not be read as a PEM certificate",
		"caddy/certificates/acme/paste.example.com.crt": "expires in 5 day(s), on 2027-01-20, under the 14-day floor",
		"/etc/none/*.pem": "matched no certificate",
	} {
		if !strings.Contains(says[subject], w) {
			t.Errorf("%s: want %q, got %q", subject, w, says[subject])
		}
	}
	if len(got) != 5 {
		t.Errorf("one divergence per fact, got %+v", got)
	}
}

func TestSurveyCapacityAgreesAndReportsExpiry(t *testing.T) {
	h := Host{Disks: []string{"/"}, DiskFree: "15%", Certs: []string{"/c/*.crt"}, CertDays: 14}
	ok := "clock host 1800000000\ndisk / 15\ncert /c/a.crt 1802000000\n"
	if got := surveyHost(h, ParseObserved(ok)); len(got) != 0 {
		t.Fatalf("a host on its floors reported %+v", got)
	}
	expired := strings.Replace(ok, "1802000000", "1799740000", 1) // 3 days before the clock
	if got := surveyHost(h, ParseObserved(expired)); len(got) != 1 || !strings.Contains(got[0].Says, "expired 3 day(s) ago") {
		t.Fatalf("got %+v", got)
	}
	if got := surveyHost(h, ParseObserved("disk / 40\n")); len(got) != 1 || !strings.Contains(got[0].Says, "do not report it") {
		t.Fatalf("a declared certificate nobody observed is named, got %+v", got)
	}
}
