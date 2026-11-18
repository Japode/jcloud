// The certificate half of `jcloud export` (JCT42): what lets a moved service answer
// over HTTPS before DNS points at its new server.
//
// A move is checked by pointing a workstation's hosts file at the new server under
// the real name. The new edge cannot get a certificate for that name yet: ACME's
// HTTP-01 challenge reaches the old server. So the certificate the old edge already
// holds travels, and the new edge serves it from its first request, renewing it
// through ACME once DNS has moved.
//
// What is read is Caddy's own storage, in the edge container: the official image
// keeps it under /data/caddy, one directory per issuer and per host
// (certificates/<issuer>/<host>/<host>.crt, .key and .json). One `docker cp` of the
// certificates directory is filtered down to the hosts this service's route answers
// for, as its configuration sets them; the other services' certificates and the ACME
// account key stay where they are. The new server registers its own account on its
// first renewal.
//
// The member holds a private key, so it is sealed whole to the export's recipients
// (the same as the configuration, JCT33) and is never written in clear, staged or
// in the archive. A certificate inside its expiry floor (host.cert_days, or 14 days)
// is said at export: it may run out before the cutover.
//
// It is never a refusal. An edge that cannot be read, or a host it holds no
// certificate for, costs only the hosts-file check, and the export says so. An
// nginx edge's certificates live wherever its ssl_certificate lines say, outside
// any volume this reads, and are not carried by this build.
package main

import (
	"archive/tar"
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

// CaddyDataDir is where the official Caddy image keeps its storage; certificates
// sit under it in certificates/.
const CaddyDataDir = "/data/caddy"

// ExportCertsMember is the sealed tar of the route's certificates.
const ExportCertsMember = "data/certificates.tar.age"

// defaultCertDays is the expiry floor when jcloud.yml's host: names none.
const defaultCertDays = 14

// edgeContainer is the top-level compose service that runs the Caddy edge and the
// container it runs as: its container_name, or the platform's prefix and its name.
// With one service it is that one; with several, the one whose image is caddy.
func edgeContainer(compose []byte, prof *Profile) (svc, container string, err error) {
	var top struct {
		Services map[string]struct {
			Image         string `yaml:"image"`
			ContainerName string `yaml:"container_name"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(compose, &top); err != nil {
		return "", "", fmt.Errorf("docker-compose.yml: %w", err)
	}
	var names []string
	for name, s := range top.Services {
		if len(top.Services) == 1 || strings.HasPrefix(path.Base(strings.SplitN(s.Image, ":", 2)[0]), "caddy") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) != 1 {
		return "", "", errors.New("docker-compose.yml: no single Caddy service runs the edge")
	}
	s := top.Services[names[0]]
	if s.ContainerName != "" {
		return names[0], s.ContainerName, nil
	}
	return names[0], prof.Container(names[0]), nil
}

// caddyHostDir is the directory name Caddy stores a host's certificate under:
// lower case, and a wildcard's `*` spelt `wildcard_`.
func caddyHostDir(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if strings.HasPrefix(h, "*.") {
		return "wildcard_" + h[1:]
	}
	return h
}

// routeHosts is every host this service's routes answer for, as the source's
// configuration sets them (a Caddy route reads <NAME>_HOSTS), or as the manifest
// declares them.
func routeHosts(root, identity string, prof *Profile, m *Manifest) []string {
	var keys []exportEnvKey
	for _, r := range m.Routes {
		keys = append(keys, exportEnvKey{key: m.hostsVar(r)})
	}
	values, _, _ := sourceEnv(root, identity, keys)
	var out []string
	for _, r := range m.Routes {
		hosts := r.Hosts
		if v := values[m.hostsVar(r)]; v != "" {
			hosts = strings.FieldsFunc(v, func(c rune) bool { return c == ',' || c == ' ' })
		}
		for _, h := range hosts {
			if !contains(out, h) {
				out = append(out, h)
			}
		}
	}
	return out
}

// ExportCerts stages the sealed certificates of the service's hosts under dir. It
// answers the member (nil when nothing was carried), its data entry, and notes;
// never a problem.
func ExportCerts(root, name, identity, dir string, recipients []string, run streamRunner) (*exportFile, *ExportData, []string) {
	prof, err := LoadProfile(root)
	if err != nil {
		return nil, nil, []string{"certificates: " + err.Error()}
	}
	raw, err := os.ReadFile(filepath.Join(root, prof.ManifestPath(name)))
	if err != nil {
		return nil, nil, []string{"certificates: " + unwrapPath(err).Error()}
	}
	m, err := Parse(raw)
	if err != nil || len(m.Routes) == 0 {
		return nil, nil, nil
	}
	cost := "; a hosts-file check before DNS moves will find no certificate"
	if prof.RouteKind != "caddy" {
		return nil, nil, []string{"certificates: an " + prof.RouteKind + " edge's are not carried by this build" + cost}
	}
	compose, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		return nil, nil, []string{"certificates: " + unwrapPath(err).Error() + cost}
	}
	svc, container, err := edgeContainer(compose, prof)
	if err != nil {
		return nil, nil, []string{"certificates: " + err.Error() + cost}
	}
	var sealTo []age.Recipient
	for _, r := range recipients {
		parsed, err := age.ParseX25519Recipient(r)
		if err != nil {
			return nil, nil, []string{fmt.Sprintf("certificates: --recipient %s: %v", r, err)}
		}
		sealTo = append(sealTo, parsed)
	}

	var all bytes.Buffer
	if err := run(&all, "docker", "cp", container+":"+CaddyDataDir+"/certificates", "-"); err != nil {
		return nil, nil, []string{fmt.Sprintf("certificates: %s could not be read: %v%s", container, err, cost)}
	}
	hosts := routeHosts(root, identity, prof, m)
	wanted := map[string]string{}
	for _, h := range hosts {
		wanted[caddyHostDir(h)] = h
	}
	floor := prof.Host.CertDays
	if floor <= 0 {
		floor = defaultCertDays
	}

	// Kept: certificates/<issuer>/<host>/..., for a wanted host, regular files only.
	var kept bytes.Buffer
	tw := tar.NewWriter(&kept)
	found := map[string]bool{}
	var notes []string
	tr := tar.NewReader(&all)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, []string{fmt.Sprintf("certificates: what %s streamed is not a tar: %v%s", container, err, cost)}
		}
		parts := strings.Split(strings.TrimPrefix(path.Clean(h.Name), "/"), "/")
		if len(parts) != 4 || parts[0] != "certificates" || h.Typeflag != tar.TypeReg {
			continue
		}
		host, ok := wanted[parts[2]]
		if !ok {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, nil, []string{"certificates: " + err.Error()}
		}
		if err := tw.WriteHeader(h); err != nil {
			return nil, nil, []string{"certificates: " + err.Error()}
		}
		if _, err := tw.Write(body); err != nil {
			return nil, nil, []string{"certificates: " + err.Error()}
		}
		found[host] = true
		if strings.HasSuffix(parts[3], ".crt") {
			if left, ok := certDaysLeft(body, time.Now()); ok && left < floor {
				notes = append(notes, fmt.Sprintf("certificates: %s's expires in %d day(s), under the %d-day floor; it may run out before the cutover", host, left, floor))
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, nil, []string{"certificates: " + err.Error()}
	}
	var carried []string
	for _, h := range hosts {
		if found[h] {
			carried = append(carried, h)
		} else {
			notes = append(notes, fmt.Sprintf("certificates: %s holds none for %s%s", container, h, cost))
		}
	}
	if len(carried) == 0 {
		return nil, nil, notes
	}
	f, err := stage(dir, ExportCertsMember, func(w io.Writer) error {
		enc, err := age.Encrypt(w, sealTo...)
		if err != nil {
			return err
		}
		if _, err := enc.Write(kept.Bytes()); err != nil {
			return err
		}
		return enc.Close()
	})
	if err != nil {
		return nil, nil, append(notes, "certificates: "+err.Error())
	}
	d := ExportData{Member: ExportCertsMember, Kind: "certs", Source: svc, Mount: CaddyDataDir, Hosts: carried, Bytes: int64(f.Size)}
	return &f, &d, notes
}

// certDaysLeft is how many whole days the first certificate in a PEM file has.
func certDaysLeft(pemData []byte, now time.Time) (int, bool) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return 0, false
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return 0, false
	}
	return int(c.NotAfter.Sub(now).Hours() / 24), true
}
