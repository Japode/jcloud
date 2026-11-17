// `jcloud export` — one service, as the files that make it up, in one archive (JCT32).
//
// Moving a service to another server used to mean gathering its compose file, its
// route, its manifest, the addon its need names and its image pin by hand, from a
// tree where nothing listed them together. Every one of them is already named
// somewhere jcloud reads: jcloud.yml says where a manifest, a route, an addon and
// the pin file sit, and the manifest says which needs it has. So the set is derived
// here, from the same resolution `agree` makes, and never listed.
//
// This is the skeleton the rest of the move fills: the platform's files and the
// format, with no configuration values and no data. A file named like `.env` in the
// service's directory is left out and said to be, because what the configuration
// holds is secret and travels encrypted or not at all.
//
// The archive is a gzipped tar. At its root sits jcloud-export.json, which says
// which schema it is, which build wrote it, for which service, from which host and
// when, and carries a sha256 per member, so the reading side (ReadExport, and the
// install that will use it) refuses an archive it does not know or one that was
// changed after it was written.
//
// A member is marked shared when it is not the service's alone: the edge's compose
// file and what it mounts, jcloud.yml, an nginx edge's one route file. Installing
// one of those on a server already running other services is a merge, not a copy,
// and the flag is what lets that be decided rather than overwritten. The pin file
// is carried with this service's lines only and marked partial for the same reason.
//
// Nothing outside the root is read, as everywhere else: a bind mount the edge's
// compose file points outside the tree, or a symlink, is not followed.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ExportSchema is the version of jcloud-export.json this build writes and reads.
const ExportSchema = 1

// ExportIndex is the name of the description at the archive's root.
const ExportIndex = "jcloud-export.json"

// ExportManifest is jcloud-export.json.
type ExportManifest struct {
	Schema     int            `json:"schema"`
	Jcloud     string         `json:"jcloud"`
	Service    string         `json:"service"`
	SourceHost string         `json:"source_host"`
	Created    string         `json:"created"`
	Files      []ExportMember `json:"files"`
	// What the service's directory holds and the archive does not, by name, so
	// that the reading side is told rather than left to notice.
	Left []string `json:"left,omitempty"`
}

// ExportMember is one file in the archive, at the path it has under the root.
type ExportMember struct {
	Path    string `json:"path"`
	Sha256  string `json:"sha256"`
	Size    int    `json:"size"`
	Shared  bool   `json:"shared,omitempty"`
	Partial bool   `json:"partial,omitempty"`
}

// exportFile is a member with its bytes, before it is written.
type exportFile struct {
	ExportMember
	data []byte
}

// ExportSet resolves the files one service is made of under root. It answers every
// problem at once, for Manifest.Validate's reason, and an archive is only written
// from a set that has none.
func ExportSet(root, name string) (files []exportFile, left, problems []string) {
	prof, err := LoadProfile(root)
	if err != nil {
		return nil, nil, []string{err.Error()}
	}
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	seen := map[string]bool{}

	// add reads one file under root. required decides whether its absence is a
	// problem or simply nothing to carry.
	add := func(rel string, required, shared bool) {
		rel = filepath.ToSlash(filepath.Clean(rel))
		if seen[rel] {
			return
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if required {
				bad("%s: is not there", rel)
			}
			return
		case err != nil:
			bad("%s: %v", rel, unwrapPath(err))
			return
		case !info.Mode().IsRegular():
			bad("%s: is not a regular file, and export follows nothing out of the tree", rel)
			return
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			bad("%s: %v", rel, unwrapPath(err))
			return
		}
		seen[rel] = true
		files = append(files, newExportFile(rel, data, shared, false))
	}

	manifestRel := filepath.ToSlash(prof.ManifestPath(name))
	data, err := os.ReadFile(filepath.Join(root, prof.ManifestPath(name)))
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("%s: no service %q under this root (%v)", manifestRel, name, unwrapPath(err))}
	}
	m, err := Parse(data)
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("%s: %v", manifestRel, err)}
	}
	if m.Name != name {
		bad("%s: names itself %q", manifestRel, m.Name)
	}

	// The service's own directory, whole, when the layout gives each service one:
	// whatever its compose file mounts beside it is part of it, and a list of
	// expected names would miss the file nobody thought of. Where the template puts
	// several manifests in one directory, only the files agree reads are taken.
	svcDir := prof.ServiceDir(name)
	if strings.Contains(path.Dir(prof.Manifest), "{name}") {
		walkErr := filepath.WalkDir(filepath.Join(root, svcDir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			if isEnvFile(d.Name()) {
				left = append(left, filepath.ToSlash(rel))
				return nil
			}
			add(rel, false, false)
			return nil
		})
		if walkErr != nil {
			bad("%s: %v", filepath.ToSlash(svcDir), unwrapPath(walkErr))
		}
	} else {
		add(prof.ManifestPath(name), true, false)
		add(filepath.Join(svcDir, "backup.conf"), false, false)
	}
	add(filepath.Join(svcDir, "docker-compose.yml"), true, false)

	// The route. An nginx edge keeps every host in one file, which is carried and
	// said to be shared; a Caddy route is the service's own.
	add(prof.RouteFile(name), len(m.Routes) > 0, prof.RouteKind == "nginx")

	// The provider of each need. A shared need's addon is still what says what the
	// database is, so it travels too, and the platform that runs it is the target's
	// to have.
	for _, n := range m.Needs {
		add(prof.AddonFile(n.Type), true, true)
	}

	// The edge: the top-level compose file, every file it bind-mounts from inside
	// the tree (the Caddyfile, on Japode), and the platform's own profile.
	add("docker-compose.yml", true, true)
	for _, rel := range edgeMounts(root) {
		add(rel, false, true)
	}
	add(ProfileFile, false, true)

	// The pin, so the target pulls the digest the source runs. Only this service's
	// lines: the rest of the file is other services' state.
	if m.Build == "" {
		pins, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(prof.PinFile)))
		if lines := pinLines(prof, m, string(pins)); lines == "" {
			bad("%s: no %s=, so the target could not pull the image this host runs", filepath.ToSlash(prof.PinFile), prof.ImageVarOf(m))
		} else {
			files = append(files, newExportFile(filepath.ToSlash(filepath.Clean(prof.PinFile)), []byte(lines), true, true))
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Strings(left)
	return files, left, problems
}

func newExportFile(rel string, data []byte, shared, partial bool) exportFile {
	sum := sha256.Sum256(data)
	return exportFile{ExportMember{Path: rel, Sha256: hex.EncodeToString(sum[:]), Size: len(data), Shared: shared, Partial: partial}, data}
}

// isEnvFile is configuration by its name: `.env`, `.env.age`, `.env.production`.
// The template is not configuration and is carried.
func isEnvFile(base string) bool {
	return (base == ".env" || strings.HasPrefix(base, ".env.")) && base != ".env.example"
}

// pinLines is this service's line in the pin file, whole, as the deploy wrote it.
func pinLines(prof *Profile, m *Manifest, body string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(prof.ImageVarOf(m)) + `=.+$`)
	found := re.FindAllString(body, -1)
	if len(found) == 0 {
		return ""
	}
	return strings.Join(found, "\n") + "\n"
}

// edgeMounts is every regular file under root that the top-level compose file
// bind-mounts, in either volume syntax. A directory is passed over: on Japode that
// is caddy/sites, every service's routes, and this one's is already taken. A source
// that leaves the root, or that is a named volume, is not a file of the tree.
func edgeMounts(root string) []string {
	data, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		return nil
	}
	var compose struct {
		Services map[string]struct {
			Volumes []yaml.Node `yaml:"volumes"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(data, &compose) != nil {
		return nil
	}
	var out []string
	for _, svc := range compose.Services {
		for _, v := range svc.Volumes {
			var source string
			switch v.Kind {
			case yaml.ScalarNode:
				source, _, _ = strings.Cut(v.Value, ":")
			case yaml.MappingNode:
				var long struct {
					Type   string `yaml:"type"`
					Source string `yaml:"source"`
				}
				if v.Decode(&long) != nil || long.Type != "bind" {
					continue
				}
				source = long.Source
			}
			if !strings.HasPrefix(source, ".") && !strings.Contains(source, "/") {
				continue // a named volume
			}
			if path.IsAbs(source) || filepath.IsAbs(source) {
				continue
			}
			rel := path.Clean(filepath.ToSlash(source))
			if rel == ".." || strings.HasPrefix(rel, "../") {
				continue
			}
			if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && info.Mode().IsRegular() && !contains(out, rel) {
				out = append(out, rel)
			}
		}
	}
	sort.Strings(out)
	return out
}

func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// WriteExport writes the archive: the index first, then every member in path
// order, each stamped with the export's own time so two exports of one tree at one
// moment are the same bytes.
func WriteExport(w io.Writer, idx ExportManifest, files []exportFile) error {
	created, err := time.Parse(time.RFC3339, idx.Created)
	if err != nil {
		return err
	}
	idx.Files = nil
	for _, f := range files {
		idx.Files = append(idx.Files, f.ExportMember)
	}
	index, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(w)
	tw := tar.NewWriter(zw)
	put := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: created, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := put(ExportIndex, append(index, '\n')); err != nil {
		return err
	}
	for _, f := range files {
		if err := put(f.Path, f.data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

// ReadExport reads an archive back and refuses it unless it is one this build
// knows: the schema it writes, an index first, every member it lists present with
// its checksum, nothing it does not list, and no path that leaves the root it will
// be installed under.
func ReadExport(r io.Reader) (*ExportManifest, map[string][]byte, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, nil, fmt.Errorf("not a gzipped archive: %w", err)
	}
	tr := tar.NewReader(zr)
	var idx *ExportManifest
	got := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, nil, err
		}
		if idx == nil {
			if h.Name != ExportIndex {
				return nil, nil, fmt.Errorf("%s is not the first member, so this is not an archive jcloud export wrote", ExportIndex)
			}
			idx = &ExportManifest{}
			if err := json.Unmarshal(data, idx); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", ExportIndex, err)
			}
			if idx.Schema != ExportSchema {
				return nil, nil, fmt.Errorf("%s: schema %d, and this build reads %d", ExportIndex, idx.Schema, ExportSchema)
			}
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return nil, nil, fmt.Errorf("%s: is not a regular file", h.Name)
		}
		if _, dup := got[h.Name]; dup {
			return nil, nil, fmt.Errorf("%s: is in the archive twice", h.Name)
		}
		got[h.Name] = data
	}
	if idx == nil {
		return nil, nil, fmt.Errorf("empty archive")
	}
	var problems []string
	listed := map[string]bool{}
	for _, f := range idx.Files {
		listed[f.Path] = true
		clean := path.Clean(f.Path)
		if clean != f.Path || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			problems = append(problems, fmt.Sprintf("%s: leaves the root it would be installed under", f.Path))
			continue
		}
		data, ok := got[f.Path]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: listed and not in the archive", f.Path))
			continue
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != f.Sha256 {
			problems = append(problems, fmt.Sprintf("%s: its sha256 is not the one %s lists", f.Path, ExportIndex))
		}
	}
	for name := range got {
		if !listed[name] {
			problems = append(problems, fmt.Sprintf("%s: in the archive and not listed in %s", name, ExportIndex))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, nil, errors.New(strings.Join(problems, "; "))
	}
	return idx, got, nil
}

// export is the command. It writes beside the target and renames, so a refused or
// interrupted export never leaves a half archive under the name asked for.
func export(args []string) int {
	usage := "jcloud export <service> [--root <dir>] -o <file>.tar.gz"
	root, out, name := ".", "", ""
	for len(args) > 0 {
		switch {
		case args[0] == "--root" && len(args) >= 2:
			root, args = args[1], args[2:]
		case args[0] == "-o" && len(args) >= 2:
			out, args = args[1], args[2:]
		case !strings.HasPrefix(args[0], "-") && name == "":
			name, args = args[0], args[1:]
		default:
			fmt.Fprintln(os.Stderr, usage)
			return 2
		}
	}
	if name == "" || out == "" {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}

	files, left, problems := ExportSet(root, name)
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s cannot be exported\n", name)
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "      %s\n", p)
		}
		return 1
	}
	host, _ := os.Hostname()
	idx := ExportManifest{
		Schema: ExportSchema, Jcloud: Version(), Service: name, SourceHost: host,
		Created: time.Now().UTC().Format(time.RFC3339), Left: left,
	}
	var buf bytes.Buffer
	if err := WriteExport(&buf, idx, files); err != nil {
		fmt.Fprintf(os.Stderr, "jcloud export: %v\n", err)
		return 2
	}
	tmp := out + ".partial"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "jcloud export: %v\n", err)
		return 2
	}
	if err := os.Rename(tmp, out); err != nil {
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "jcloud export: %v\n", err)
		return 2
	}

	fmt.Printf("  \033[32m✓\033[0m %s: %s, %d file(s)\n", out, name, len(files))
	for _, f := range files {
		var tags []string
		if f.Shared {
			tags = append(tags, "shared")
		}
		if f.Partial {
			tags = append(tags, "this service's lines only")
		}
		suffix := ""
		if len(tags) > 0 {
			suffix = " (" + strings.Join(tags, ", ") + ")"
		}
		fmt.Printf("      %s%s\n", f.Path, suffix)
	}
	for _, l := range left {
		fmt.Printf("      \033[33m!\033[0m %s left out: configuration is not carried by this archive\n", l)
	}
	return 0
}
