// The data half of `jcloud export` (JCT34): what a service holds, in a form another
// host can open without restic.
//
// What the state is, and how to take it consistently, is already declared. Each
// volume the manifest names is copied whole, as the tar `docker cp` streams from the
// service's own container, so no image is pulled to read it and a stopped container
// is read the same way. A need whose addon asks for `mysql` (the backup.conf
// directive) is dumped, never copied: `mariadb-dump --single-transaction` as the
// container's own user over its one database, the command scripts/backup.sh runs,
// which is consistent while the site keeps serving. A sqlite file the manifest
// names is taken with VACUUM INTO, through the sqlite image the platform's backup
// pins, never as bytes mid-write; it sits beside the volume's tar and wins over the
// copy inside it.
//
// A volume is not consistent the way a dump is: a file uploaded during the copy may
// be missing. `--stop` stops the service (not its database) for the copy and starts
// it again, which the last export before a cutover wants. Without it the index says
// the volumes were copied live.
//
// The index records each member's size and each dump's table count, so the install
// can say the restore came back whole and not merely that it ran.
//
// A shared need's database is the platform's, and its data is not carried; that is
// said, not passed over.
//
// The data can be large, so each member is staged in a file and hashed as it is
// written, and the archive is assembled from the staged files.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExportData is one member of the archive that carries state, as the index lists it.
type ExportData struct {
	Member string `json:"member"`
	// volume, mysql or sqlite
	Kind string `json:"kind"`
	// The compose volume key, or the need's derived name for a dump.
	Source string `json:"source"`
	// Where the volume is mounted in the service's container; the tar's root
	// directory is that path's last element.
	Mount  string `json:"mount,omitempty"`
	File   string `json:"file,omitempty"`
	Bytes  int64  `json:"bytes"`
	Tables int    `json:"tables,omitempty"`
}

// streamRunner runs one command with its stdout written to w. A variable so the
// tests can stand in for docker.
type streamRunner func(w io.Writer, name string, args ...string) error

func streamCommand(w io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = w, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s %s: %w: %s", name, args[0], err, msg)
		}
		return fmt.Errorf("%s %s: %w", name, args[0], err)
	}
	return nil
}

var exportDocker streamRunner = streamCommand

// mysqlDump is scripts/backup.sh's MYSQL_DUMP: the credentials are already in the
// database container's environment, because the addon says the image reads them,
// and MYSQL_PWD keeps the password off the argument list.
const mysqlDump = `set -eu
: "${MARIADB_USER:?the container does not carry it}"
: "${MARIADB_PASSWORD:?the container does not carry it}"
: "${MARIADB_DATABASE:?the container does not carry it}"
export MYSQL_PWD="$MARIADB_PASSWORD"
exec mariadb-dump --user="$MARIADB_USER" --single-transaction --quick --databases "$MARIADB_DATABASE"`

// sqliteImage is the one the platform's scripts/backup-lib.sh pins, by digest;
// JCLOUD_SQLITE_IMAGE overrides it where a platform pins another.
const sqliteImage = "keinos/sqlite3@sha256:a5610a155a8c9007f2050120406a0abcffab246570d6ac1ffe370f5f23e14dc1"

func sqliteImageRef() string {
	if v := os.Getenv("JCLOUD_SQLITE_IMAGE"); v != "" {
		return v
	}
	return sqliteImage
}

// composeProject is the top-level `name:`, which prefixes every volume docker
// created. Read, and refused when absent, as scripts/backup-lib.sh does.
func composeProject(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		return "", unwrapPath(err)
	}
	var top struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(data, &top); err != nil {
		return "", fmt.Errorf("docker-compose.yml: %w", err)
	}
	if top.Name == "" {
		return "", fmt.Errorf("docker-compose.yml declares no top-level name:, so the volumes docker created cannot be named")
	}
	return top.Name, nil
}

// stage runs one command into a file under dir, hashing as it writes.
func stage(dir, member string, run func(io.Writer) error) (exportFile, error) {
	local := filepath.Join(dir, filepath.FromSlash(member))
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return exportFile{}, err
	}
	f, err := os.OpenFile(local, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return exportFile{}, err
	}
	h := sha256.New()
	counter := &countWriter{}
	runErr := run(io.MultiWriter(f, h, counter))
	closeErr := f.Close()
	if runErr != nil {
		return exportFile{}, runErr
	}
	if closeErr != nil {
		return exportFile{}, closeErr
	}
	return exportFile{ExportMember: ExportMember{Path: member, Sha256: hex.EncodeToString(h.Sum(nil)), Size: int(counter.n)}, src: local}, nil
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// ExportState stages every piece of the service's state under dir. stop asks for
// the service to be stopped while its volumes are copied.
func ExportState(root, name, dir string, stop bool, run streamRunner) (files []exportFile, data []ExportData, notes, problems []string) {
	prof, err := LoadProfile(root)
	if err != nil {
		return nil, nil, nil, []string{err.Error()}
	}
	raw, err := os.ReadFile(filepath.Join(root, prof.ManifestPath(name)))
	if err != nil {
		return nil, nil, nil, []string{unwrapPath(err).Error()}
	}
	m, err := Parse(raw)
	if err != nil {
		return nil, nil, nil, []string{err.Error()}
	}
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	// The dumps first: they need the database up, and the service's stop is no
	// business of theirs.
	needs, needProblems := m.resolveNeeds(root, prof)
	problems = append(problems, needProblems...)
	for _, n := range needs {
		switch {
		case n.addon == nil:
			continue
		case n.need.Shared != "":
			notes = append(notes, fmt.Sprintf("needs[%s]: its database is %s, the platform's, and its data is not carried", n.need.Name, n.svc))
			continue
		case n.addon.Volume.Backup == "":
			// A volume a byte-for-byte copy is right for, taken from the need's own
			// container.
			member := "data/" + n.svc + ".tar"
			f, err := stage(dir, member, func(w io.Writer) error {
				return run(w, "docker", "cp", prof.Container(n.svc)+":"+n.addon.Volume.Path, "-")
			})
			if err != nil {
				bad("needs[%s]: %v", n.need.Name, err)
				continue
			}
			files = append(files, f)
			data = append(data, ExportData{Member: member, Kind: "volume", Source: n.svc, Mount: n.addon.Volume.Path, Bytes: int64(f.Size)})
		case n.addon.Volume.Backup == "mysql":
			member := "data/" + n.svc + ".sql"
			f, err := stage(dir, member, func(w io.Writer) error {
				return run(w, "docker", "exec", "-i", prof.Container(n.svc), "sh", "-c", mysqlDump)
			})
			if err != nil {
				bad("needs[%s]: no dump came out of %s: %v", n.need.Name, prof.Container(n.svc), err)
				continue
			}
			tables, complete, err := scanDump(f.src)
			switch {
			case err != nil:
				bad("needs[%s]: %v", n.need.Name, err)
				continue
			case !complete:
				bad("needs[%s]: the dump of %s does not end where mariadb-dump ends one; it was cut off", n.need.Name, n.svc)
				continue
			}
			files = append(files, f)
			data = append(data, ExportData{Member: member, Kind: "mysql", Source: n.svc, Bytes: int64(f.Size), Tables: tables})
		default:
			bad("needs[%s]: addons/%s/addon.yml asks for `%s`, and this build dumps mysql only", n.need.Name, n.need.Type, n.addon.Volume.Backup)
		}
	}

	if len(m.Volumes) == 0 {
		return files, data, notes, problems
	}
	project, err := composeProject(root)
	if err != nil {
		bad("%v", err)
		return files, data, notes, problems
	}
	container := prof.Container(m.Name)
	if stop {
		if err := run(io.Discard, "docker", "stop", container); err != nil {
			bad("--stop: %v", err)
			return files, data, notes, problems
		}
		defer func() {
			if err := run(io.Discard, "docker", "start", container); err != nil {
				problems = append(problems, fmt.Sprintf("%s was stopped for the copy and did not start again: %v", container, err))
			}
		}()
	} else {
		notes = append(notes, fmt.Sprintf("the volumes were copied while %s ran; --stop takes them at rest", container))
	}
	for _, v := range m.Volumes {
		key := m.Name + "-" + v.Name
		member := "data/" + key + ".tar"
		f, err := stage(dir, member, func(w io.Writer) error {
			return run(w, "docker", "cp", container+":"+v.Path, "-")
		})
		if err != nil {
			bad("volumes[%s]: %v", v.Name, err)
			continue
		}
		files = append(files, f)
		data = append(data, ExportData{Member: member, Kind: "volume", Source: key, Mount: v.Path, Bytes: int64(f.Size)})

		for _, file := range v.SQLite {
			member := "data/" + key + "/" + path.Clean(file)
			f, err := stage(dir, member, func(w io.Writer) error {
				return run(w, "docker", "run", "--rm", "--user", "0:0", "-v", project+"_"+key+":/v",
					"--entrypoint", "sh", sqliteImageRef(), "-c",
					`rm -f /tmp/copy.db && sqlite3 "/v/$1" "VACUUM INTO '/tmp/copy.db'" && cat /tmp/copy.db`, "sh", file)
			})
			if err != nil {
				bad("volumes[%s]: sqlite %s: %v", v.Name, file, err)
				continue
			}
			files = append(files, f)
			data = append(data, ExportData{Member: member, Kind: "sqlite", Source: key, File: file, Bytes: int64(f.Size)})
		}
	}
	return files, data, notes, problems
}

// scanDump counts the tables a dump creates and says whether it ran to the end:
// mariadb-dump writes `-- Dump completed` last.
func scanDump(local string) (tables int, complete bool, err error) {
	f, err := os.Open(local)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "CREATE TABLE "):
			tables++
		case strings.HasPrefix(line, "-- Dump completed"):
			complete = true
		}
	}
	return tables, complete, sc.Err()
}
