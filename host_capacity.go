// Disk and certificates, read on the host (JCT25).
//
// Two failures were seen only through what they break: a full disk stops a database
// and every build, and an expired certificate takes every host on it down at once.
// Both are answered by the host itself, so observe reads them without dialling
// anything, and jcloud.yml's host: declares the floor survey holds them to:
//
//	host:
//	  disks: [/, /var/lib/docker]     # each filesystem holding one of these paths
//	  disk_free: 15%                  # must keep this much free
//	  certs:                          # each certificate file, a glob allowed, since
//	    - /var/lib/docker/volumes/caddy_data/_data/caddy/certificates/*/*/*.crt
//	  cert_days: 14                   # must stay valid this many days more
//
// A certificate is read from its file with crypto/x509, never by connecting to the
// host name it serves, so this reaches no network and needs no openssl. It reads the
// leaf, the first certificate in the file, which is the one that expires first in
// any chain an edge writes.
//
// Out of scope: CPU, memory and request latency. They change by the second and want a
// time-series store, which is a monitoring system and not a consistency check.
package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	percentWord = regexp.MustCompile(`^([0-9]{1,2})%$`)
	pathWord    = regexp.MustCompile(`^\S+$`)
)

func (h Host) validateCapacity(bad func(string, ...any)) {
	for _, d := range h.Disks {
		if !pathWord.MatchString(d) {
			bad("host.disks: %q is empty or has a space in it", d)
		}
	}
	for _, c := range h.Certs {
		if _, err := path.Match(c, ""); !pathWord.MatchString(c) || err != nil {
			bad("host.certs: %q is not a path or a glob", c)
		}
	}
	switch {
	case len(h.Disks) > 0 && h.DiskFree == "":
		bad("host.disks is given and host.disk_free is not, so no floor holds them")
	case h.DiskFree != "" && !percentWord.MatchString(h.DiskFree):
		bad("host.disk_free is %q, and a floor is a percentage such as 15%%", h.DiskFree)
	}
	if len(h.Certs) > 0 && h.CertDays <= 0 {
		bad("host.certs is given and host.cert_days is not a positive number of days, so no floor holds them")
	}
}

// observeDisks prints the free share of each filesystem, as df counts it: available
// over used plus available, which leaves out the blocks reserved for root, so 0%
// is the point where an ordinary writer starts failing.
//
//	disk <path> <percent free>     or unknown when df did not answer
func observeDisks(root string, disks []string, run commandRunner) []string {
	var lines []string
	for _, d := range disks {
		free := "unknown"
		if out, err := run("df", "-P", "-k", hostPath(root, d)); err == nil {
			if rows := strings.Split(strings.TrimSpace(out), "\n"); len(rows) == 2 {
				if f := strings.Fields(rows[1]); len(f) >= 4 {
					used, e1 := strconv.ParseInt(f[2], 10, 64)
					avail, e2 := strconv.ParseInt(f[3], 10, 64)
					if e1 == nil && e2 == nil && used+avail > 0 {
						free = strconv.FormatInt(avail*100/(used+avail), 10)
					}
				}
			}
		}
		lines = append(lines, fmt.Sprintf("disk %s %s", d, free))
	}
	return lines
}

// observeCerts prints the expiry of each certificate a declared path or glob matches.
//
//	cert <file>    <unix seconds of NotAfter>
//	cert <file>    unreadable                  a file that is not a PEM certificate
//	cert <pattern> none                        a glob that matched nothing
func observeCerts(root string, certs []string) []string {
	var lines []string
	for _, c := range certs {
		files, _ := filepath.Glob(filepath.FromSlash(hostPath(root, c)))
		if len(files) == 0 {
			lines = append(lines, fmt.Sprintf("cert %s none", c))
			continue
		}
		for _, file := range files {
			name := filepath.ToSlash(file)
			if !path.IsAbs(c) {
				// Named as the declaration names it, relative to the root, so survey
				// can match it against the pattern.
				if rel, err := filepath.Rel(root, file); err == nil {
					name = filepath.ToSlash(rel)
				}
			}
			lines = append(lines, fmt.Sprintf("cert %s %s", name, certExpiry(file)))
		}
	}
	return lines
}

func certExpiry(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return "unreadable"
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "unreadable"
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "unreadable"
	}
	return strconv.FormatInt(cert.NotAfter.Unix(), 10)
}

// hostPath is a declared path on the host: as written when absolute, under the root
// otherwise, with slashes.
func hostPath(root, p string) string {
	if path.IsAbs(p) {
		return p
	}
	return path.Join(filepath.ToSlash(root), p)
}

func surveyCapacity(h Host, o *Observed) []Divergence {
	var out []Divergence
	if len(h.Disks) > 0 {
		m := percentWord.FindStringSubmatch(h.DiskFree)
		floor, _ := strconv.Atoi(m[1]) // validate refused anything else
		for _, d := range h.Disks {
			free, seen := o.Disks[d]
			switch n, err := strconv.Atoi(free); {
			case !seen:
				out = append(out, Divergence{d, "is declared in jcloud.yml host.disks and the observations do not report it"})
			case err != nil:
				out = append(out, Divergence{d, "could not be read — df did not answer on the host"})
			case n < floor:
				out = append(out, Divergence{d, fmt.Sprintf("has %d%% free, %d points under the %s floor jcloud.yml declares", n, floor-n, h.DiskFree)})
			}
		}
	}

	clock := now()
	if c, ok := o.Clock["host"]; ok {
		clock = time.Unix(c, 0)
	}
	floor := time.Duration(h.CertDays) * 24 * time.Hour
	for _, pattern := range h.Certs {
		matched := false
		for file, expiry := range o.Certs {
			if file != pattern {
				if ok, _ := path.Match(pattern, file); !ok {
					continue
				}
			}
			matched = true
			secs, err := strconv.ParseInt(expiry, 10, 64)
			switch {
			case expiry == "none":
				out = append(out, Divergence{pattern, "matched no certificate on the host, so nothing it names can be held to a floor"})
			case err != nil:
				out = append(out, Divergence{file, "could not be read as a PEM certificate"})
			default:
				left := time.Unix(secs, 0).Sub(clock)
				switch {
				case left <= 0:
					out = append(out, Divergence{file, fmt.Sprintf("expired %d day(s) ago, on %s", int(-left.Hours()/24), time.Unix(secs, 0).UTC().Format("2006-01-02"))})
				case left < floor:
					out = append(out, Divergence{file, fmt.Sprintf("expires in %d day(s), on %s, under the %d-day floor jcloud.yml declares", int(left.Hours()/24), time.Unix(secs, 0).UTC().Format("2006-01-02"), h.CertDays)})
				}
			}
		}
		if !matched {
			out = append(out, Divergence{pattern, "is declared in jcloud.yml host.certs and the observations do not report it"})
		}
	}
	// By subject, since the certificates were read off a map.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}
