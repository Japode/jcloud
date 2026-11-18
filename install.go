// `jcloud install` — an exported service, put back on another server (JCT36).
//
// What docs/DEPLOY.md did by hand, in the order it has to happen:
//
//  1. Verify the archive (schema, checksums: ReadExport) and refuse a place that is
//     not empty for this service: a container or a volume of it already there, or one
//     of its own files. Install is for an empty place and never an overwrite, so it
//     cannot be mistaken for a deploy.
//  2. Open the sealed configuration and merge its keys into `.env` (mode 600), and
//     the pins into the pin file, keeping every line already there. Refused before
//     anything is written while a key the archive says the source lacked, or a key
//     a compose file requires with `${KEY:?}` (the edge's ACME_EMAIL), is unset.
//     Then the files are laid down. A shared file the target already has is kept as
//     it is, and said to differ when it does: it is the target platform's, not this
//     service's.
//  3. Create the containers, copy each volume's tar into its own container (with
//     the sqlite copies in place of the live files, and their -wal and -shm left
//     out), start only the database needs, wait for them, feed each dump in, and
//     count the tables back.
//     When the archive carries the route's certificates (JCT42) and the edge is
//     not here yet, the edge is created too and they are opened into its data
//     volume, so it serves them from its first request.
//  4. Start the service and the edge, under one wait budget.
//  5. Report against what the archive recorded, run `agree` on the laid-down tree,
//     and print what is left by hand: DNS, with the hosts the route answers for.
//
// Step 3 before step 4 is load-bearing. fespsp's entrypoint seeds a demo site
// unless `wp core is-installed` passes, so a service started on an empty database
// installs content the dump then lands on top of.
//
// Compose is run the way scripts/deploy.sh runs it: `.env` and then the pin file
// as two --env-file, the edge's compose file and the service's, from the root.
//
// Before any of it is written, every image is pulled: this service's pins and each
// addon's digest, after a `docker login` to each registry they name when a token is
// given (JCT37). A private package such as fespsp-institucional's needs one, a PAT
// with read:packages as the platform's Deploy workflow uses. The token comes from a
// file or JCLOUD_REGISTRY_TOKEN, never argv, and is written nowhere in the tree;
// the login lands in the invoking user's docker config, as a manual one would, and
// install says so. Shipping images inside the archive (`docker save`) is not done:
// nothing in this move needs a host with no registry access at all.
//
// Before all of it, when the archive's jcloud.yml declares host:, `host apply`
// prepares the machine (JCT38, host_apply.go), so an empty server needs this
// command and nothing else. The timers it installs wait for the end, once the
// scripts they run are laid down and the service is up (JCT43).
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

// dockerRunner runs one command with stdin and stdout; a variable for the tests.
type dockerRunner func(stdin io.Reader, stdout io.Writer, name string, args ...string) error

var installDocker dockerRunner = func(stdin io.Reader, stdout io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s: %w: %s", strings.Join(append([]string{name}, args...), " "), err, msg)
		}
		return fmt.Errorf("%s: %w", strings.Join(append([]string{name}, args...), " "), err)
	}
	return nil
}

// requiredRE is a compose interpolation that refuses to start without its key.
var requiredRE = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*):?\?`)

const mysqlRestore = `set -eu
export MYSQL_PWD="$MARIADB_PASSWORD"
exec mariadb --user="$MARIADB_USER"`

const mysqlCount = `set -eu
export MYSQL_PWD="$MARIADB_PASSWORD"
exec mariadb --user="$MARIADB_USER" -N -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='$MARIADB_DATABASE'"`

// installPlan is everything steps 1 and 2 decided, before anything is written.
type installPlan struct {
	idx     *ExportManifest
	prof    *Profile
	m       *Manifest
	project string
	edge    []string          // the edge compose file's services
	needs   []resolvedNeed    // owned needs, resolved from the archive's addons
	write   map[string][]byte // root-relative path -> content
	env     map[string]string // the merged configuration
	notes   []string
	hasPins bool
	pinned  []string // the image references this service's pin lines name
	// The sealed certificates the archive carries (JCT42), the edge they go into,
	// and whether that edge was already here, in which case its certificates are
	// its own and nothing is put in.
	certs      *ExportData
	edgeSvc    string
	edgeName   string
	edgeExists bool
	identity   string
}

// planInstall reads the archive and the target and decides what to write, or
// refuses with every reason at once.
func planInstall(archive, root, identity string, run dockerRunner) (*installPlan, []string) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, []string{err.Error()}
	}
	idx, files, err := ReadExport(f)
	f.Close()
	if err != nil {
		return nil, []string{archive + ": " + err.Error()}
	}
	p := &installPlan{idx: idx, write: map[string][]byte{}, env: map[string]string{}}

	// The source's layout, which is where its files go.
	p.prof = DefaultProfile()
	if data, ok := files[ProfileFile]; ok {
		if p.prof, err = ParseProfile(data); err != nil {
			return nil, []string{err.Error()}
		}
	}
	manifest, ok := files[filepath.ToSlash(p.prof.ManifestPath(idx.Service))]
	if !ok {
		return nil, []string{"the archive carries no manifest for " + idx.Service}
	}
	if p.m, err = Parse(manifest); err != nil {
		return nil, []string{err.Error()}
	}
	if p.project, p.edge, err = composeTop(files["docker-compose.yml"]); err != nil {
		return nil, []string{err.Error()}
	}

	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	// The needs, from the addons the archive carries: the target has none yet.
	volumes := []string{}
	for _, v := range p.m.Volumes {
		volumes = append(volumes, p.m.Name+"-"+v.Name)
	}
	for _, n := range p.m.Needs {
		if n.Shared != "" {
			p.notes = append(p.notes, fmt.Sprintf("needs[%s] is %s, the platform's, which the target has to run already", n.Name, n.Shared))
			continue
		}
		at := filepath.ToSlash(p.prof.AddonFile(n.Type))
		a, err := ParseAddon(files[at])
		if err != nil {
			bad("%s: %v", at, err)
			continue
		}
		r := resolvedNeed{need: n, addon: a, svc: p.m.Name + "-" + n.Name}
		p.needs = append(p.needs, r)
		volumes = append(volumes, r.svc)
	}

	// Empty for this service: no container and no volume of it.
	containers := []string{p.prof.Container(p.m.Name)}
	for _, r := range p.needs {
		containers = append(containers, p.prof.Container(r.svc))
	}
	for _, c := range containers {
		var out bytes.Buffer
		if err := run(nil, &out, "docker", "ps", "-a", "-q", "--filter", "name=^/"+c+"$"); err != nil {
			bad("%v", err)
		} else if strings.TrimSpace(out.String()) != "" {
			bad("%s: a container of that name is already here, and install never overwrites", c)
		}
	}
	for _, v := range volumes {
		name := p.project + "_" + v
		var out bytes.Buffer
		if err := run(nil, &out, "docker", "volume", "ls", "-q", "--filter", "name=^"+name+"$"); err != nil {
			bad("%v", err)
		} else if strings.TrimSpace(out.String()) != "" {
			bad("%s: the volume is already here, and install never overwrites", name)
		}
	}

	p.identity = identity
	p.planCerts(files, run, bad)
	pins := p.planFiles(root, files, bad)
	p.planEnv(root, identity, files, bad)

	// What compose would refuse to start without, refused here instead, while
	// nothing has been written.
	set := func(k string) bool { _, a := p.env[k]; _, b := pins[k]; return a || b }
	for _, k := range idx.EnvMissing {
		if !set(k) {
			bad("%s: the source had no value for it, and the target's .env does not set it", k)
		}
	}
	svcCompose := filepath.ToSlash(filepath.Join(p.prof.ServiceDir(p.m.Name), "docker-compose.yml"))
	seen := map[string]bool{}
	for _, at := range []string{"docker-compose.yml", svcCompose} {
		for _, match := range requiredRE.FindAllStringSubmatch(string(files[at]), -1) {
			if k := match[1]; !set(k) && !seen[k] {
				seen[k] = true
				bad("%s: requires %s, and neither the target's .env nor the archive sets it", at, k)
			}
		}
	}
	return p, problems
}

// planCerts finds the certificates the archive carries and the edge they go into.
// An edge already on the target keeps its own: install never overwrites.
func (p *installPlan) planCerts(files map[string][]byte, run dockerRunner, bad func(string, ...any)) {
	for i, d := range p.idx.Data {
		if d.Kind == "certs" {
			p.certs = &p.idx.Data[i]
		}
	}
	if p.certs == nil {
		return
	}
	svc, name, err := edgeContainer(files["docker-compose.yml"], p.prof)
	if err != nil {
		bad("%s: %v", p.certs.Member, err)
		return
	}
	p.edgeSvc, p.edgeName = svc, name
	var out bytes.Buffer
	if err := run(nil, &out, "docker", "ps", "-a", "-q", "--filter", "name=^/"+name+"$"); err != nil {
		bad("%v", err)
		return
	}
	if strings.TrimSpace(out.String()) != "" {
		p.edgeExists = true
		p.notes = append(p.notes, fmt.Sprintf("%s was already here, so the certificates for %s were not put into it; it asks ACME for its own once DNS points here", name, strings.Join(p.certs.Hosts, ", ")))
	}
}

// restoreCerts creates the edge, not started, and opens the sealed certificates
// straight into its data volume, so the first start already serves them (JCT42).
func (p *installPlan) restoreCerts(root string, staged map[string]string, run dockerRunner) (string, error) {
	if p.certs == nil || p.edgeExists {
		return "", nil
	}
	ids, err := LoadIdentities(p.identity)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.certs.Member, err)
	}
	f, err := os.Open(staged[p.certs.Member])
	if err != nil {
		return "", err
	}
	defer f.Close()
	plain, err := age.Decrypt(f, ids...)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.certs.Member, err)
	}
	if err := run(nil, io.Discard, "docker", p.compose(root, "create", p.edgeSvc)...); err != nil {
		return "", fmt.Errorf("creating the edge: %w", err)
	}
	if err := run(plain, io.Discard, "docker", "cp", "-a", "-", p.edgeName+":"+p.certs.Mount); err != nil {
		return "", fmt.Errorf("%s: %w", p.certs.Member, err)
	}
	return fmt.Sprintf("certificates for %s put into %s before it started", strings.Join(p.certs.Hosts, ", "), p.edgeName), nil
}

// planFiles decides each platform file against what the target already holds, and
// answers the pins the merged pin file will carry.
func (p *installPlan) planFiles(root string, files map[string][]byte, bad func(string, ...any)) map[string]string {
	pins := map[string]string{}
	for _, member := range p.idx.Files {
		if strings.HasPrefix(member.Path, "data/") || member.Path == p.idx.Env {
			continue
		}
		data := files[member.Path]
		existing, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(member.Path)))
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			bad("%s: %v", member.Path, unwrapPath(err))
			continue
		}
		switch {
		case member.Partial:
			merged, err := mergeLines(string(existing), string(data))
			if err != nil {
				bad("%s: %v", member.Path, err)
				continue
			}
			p.write[member.Path] = []byte(merged)
			for k, v := range parseDotEnv(merged) {
				pins[k] = v
			}
			for _, v := range parseDotEnv(string(data)) {
				p.pinned = append(p.pinned, v)
			}
			sort.Strings(p.pinned)
			p.hasPins = true
		case exists && bytes.Equal(existing, data):
		case exists && member.Shared:
			p.notes = append(p.notes, member.Path+": the target's own differs from the source's and is kept; compare them by hand")
		case exists:
			bad("%s: already here, and install never overwrites", member.Path)
		default:
			p.write[member.Path] = data
		}
	}
	return pins
}

// planEnv merges the sealed configuration into the target's .env: the target's
// lines kept, this service's added.
func (p *installPlan) planEnv(root, identity string, files map[string][]byte, bad func(string, ...any)) {
	existing, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		bad(".env: %v", unwrapPath(err))
	}
	for k, v := range parseDotEnv(string(existing)) {
		p.env[k] = v
	}
	text := string(existing)
	defer func() { p.write[".env"] = []byte(text) }()
	if p.idx.Env == "" {
		return
	}
	sealed := ParseEnvFile(files[p.idx.Env])
	ids, err := LoadIdentities(identity)
	if err != nil {
		bad("%s: %v", p.idx.Env, err)
		return
	}
	for _, k := range sealed.Keys() {
		b64, _ := sealed.Get(k)
		value, err := open(b64, ids)
		if err != nil {
			bad("%s: %s: %v", p.idx.Env, k, err)
			continue
		}
		if was, ok := p.env[k]; ok {
			if was != string(value) {
				p.notes = append(p.notes, k+": the target's .env already sets it, to another value, and that is kept")
			}
			continue
		}
		p.env[k] = string(value)
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += k + "=" + string(value) + "\n"
	}
}

// archiveHost is the host: the archive's jcloud.yml declares, verified with the
// rest of the archive; the zero Host when it carries no jcloud.yml.
func archiveHost(archive string) (Host, error) {
	f, err := os.Open(archive)
	if err != nil {
		return Host{}, err
	}
	defer f.Close()
	_, files, err := ReadExport(f)
	if err != nil {
		return Host{}, fmt.Errorf("%s: %w", archive, err)
	}
	data, ok := files[ProfileFile]
	if !ok {
		return Host{}, nil
	}
	prof, err := ParseProfile(data)
	if err != nil {
		return Host{}, err
	}
	return prof.Host, nil
}

// mergeLines adds the archive's KEY=value lines to the target's, refusing a key
// the target already pins to something else.
func mergeLines(existing, add string) (string, error) {
	have := parseDotEnv(existing)
	out := existing
	for _, line := range strings.Split(strings.TrimSpace(add), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if was, ok := have[k]; ok {
			if was != v {
				return "", fmt.Errorf("%s is already pinned here to %s", k, was)
			}
			continue
		}
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += line + "\n"
	}
	return out, nil
}

// composeTop is the edge compose file's project name and services.
func composeTop(data []byte) (string, []string, error) {
	if data == nil {
		return "", nil, errors.New("the archive carries no docker-compose.yml for the edge")
	}
	var top struct {
		Name     string               `yaml:"name"`
		Services map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &top); err != nil {
		return "", nil, fmt.Errorf("docker-compose.yml: %w", err)
	}
	if top.Name == "" {
		return "", nil, errors.New("docker-compose.yml declares no top-level name:, so the volumes cannot be named")
	}
	var services []string
	for s := range top.Services {
		services = append(services, s)
	}
	sort.Strings(services)
	return top.Name, services, nil
}

// lay writes every planned file under root: .env at 0600, the rest at 0644.
func (p *installPlan) lay(root string) error {
	paths := make([]string, 0, len(p.write))
	for rel := range p.write {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		dest := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if rel == ".env" {
			mode = 0o600
		}
		if err := os.WriteFile(dest, p.write[rel], mode); err != nil {
			return err
		}
		if err := os.Chmod(dest, mode); err != nil {
			return err
		}
	}
	return nil
}

// compose is the argv deploy.sh would run, with args after it.
func (p *installPlan) compose(root string, args ...string) []string {
	argv := []string{"compose", "--project-directory", root, "-p", p.project, "--env-file", filepath.Join(root, ".env")}
	if p.hasPins {
		argv = append(argv, "--env-file", filepath.Join(root, filepath.FromSlash(p.prof.PinFile)))
	}
	argv = append(argv, "-f", filepath.Join(root, "docker-compose.yml"),
		"-f", filepath.Join(root, p.prof.ServiceDir(p.m.Name), "docker-compose.yml"))
	return append(argv, args...)
}

func (p *installPlan) containerOf(source string) string {
	for _, r := range p.needs {
		if r.svc == source {
			return p.prof.Container(r.svc)
		}
	}
	return p.prof.Container(p.m.Name)
}

// restore is steps 3 to 5, on a tree already laid down.
func (p *installPlan) restore(archive, root string, wait int, run dockerRunner) (report, problems []string) {
	staging, err := os.MkdirTemp("", "jcloud-install-")
	if err != nil {
		return nil, []string{err.Error()}
	}
	defer os.RemoveAll(staging)
	staged, err := stageData(archive, staging)
	if err != nil {
		return nil, []string{err.Error()}
	}

	var needSvcs []string
	for _, r := range p.needs {
		needSvcs = append(needSvcs, r.svc)
	}
	// Created and not started: the volumes exist, and nothing has booted on them.
	if err := run(nil, io.Discard, "docker", p.compose(root, append(append([]string{"create"}, needSvcs...), p.m.Name)...)...); err != nil {
		return nil, []string{"creating the containers: " + err.Error()}
	}
	if report, err = p.restoreVolumes(staged, run); err != nil {
		return report, []string{err.Error()}
	}
	if done, err := p.restoreCerts(root, staged, run); err != nil {
		return report, []string{err.Error()}
	} else if done != "" {
		report = append(report, done)
	}
	if len(needSvcs) > 0 {
		if err := run(nil, io.Discard, "docker", p.compose(root, append([]string{"up", "-d", "--wait", "--wait-timeout", strconv.Itoa(wait)}, needSvcs...)...)...); err != nil {
			return report, []string{"starting the databases: " + err.Error()}
		}
	}
	dumps, problems := p.restoreDumps(staged, run)
	report = append(report, dumps...)
	if len(problems) > 0 {
		return report, problems
	}
	if err := run(nil, io.Discard, "docker", p.compose(root, append([]string{"up", "-d", "--wait", "--wait-timeout", strconv.Itoa(wait), p.m.Name}, p.edge...)...)...); err != nil {
		return report, []string{"starting the service and the edge: " + err.Error()}
	}
	return append(report, fmt.Sprintf("%s and %s are up and healthy", p.m.Name, strings.Join(p.edge, ", "))), nil
}

// restoreVolumes copies each volume's tar into the container that mounts it.
func (p *installPlan) restoreVolumes(staged map[string]string, run dockerRunner) ([]string, error) {
	var report []string
	for _, d := range p.idx.Data {
		if d.Kind != "volume" {
			continue
		}
		subst := map[string]string{}
		for _, s := range p.idx.Data {
			if s.Kind == "sqlite" && s.Source == d.Source {
				subst[path.Base(d.Mount)+"/"+path.Clean(s.File)] = staged[s.Member]
			}
		}
		pr, pw := io.Pipe()
		go func(src string) { pw.CloseWithError(rewriteVolumeTar(src, subst, pw)) }(staged[d.Member])
		err := run(pr, io.Discard, "docker", "cp", "-a", "-", p.containerOf(d.Source)+":"+path.Dir(d.Mount))
		pr.CloseWithError(io.ErrClosedPipe)
		if err != nil {
			return report, fmt.Errorf("%s: %w", d.Member, err)
		}
		report = append(report, fmt.Sprintf("%s restored into %s (%d byte(s) as exported)", d.Source, d.Mount, d.Bytes))
	}
	return report, nil
}

// restoreDumps feeds each dump into its running database and counts the tables
// back against what the archive recorded.
func (p *installPlan) restoreDumps(staged map[string]string, run dockerRunner) (report, problems []string) {
	for _, d := range p.idx.Data {
		if d.Kind != "mysql" {
			continue
		}
		c := p.containerOf(d.Source)
		f, err := os.Open(staged[d.Member])
		if err != nil {
			return report, []string{err.Error()}
		}
		err = run(f, io.Discard, "docker", "exec", "-i", c, "sh", "-c", mysqlRestore)
		f.Close()
		if err != nil {
			return report, []string{fmt.Sprintf("%s: feeding the dump in: %v", d.Member, err)}
		}
		var out bytes.Buffer
		if err := run(nil, &out, "docker", "exec", c, "sh", "-c", mysqlCount); err != nil {
			return report, []string{fmt.Sprintf("%s: counting the tables: %v", d.Source, err)}
		}
		got, _ := strconv.Atoi(strings.TrimSpace(out.String()))
		if got != d.Tables {
			problems = append(problems, fmt.Sprintf("%s: %d table(s) came back, and the dump carried %d", d.Source, got, d.Tables))
			continue
		}
		report = append(report, fmt.Sprintf("%s restored, %d table(s), as exported", d.Source, got))
	}
	return report, problems
}

// stageData extracts every data/ member of a verified archive into dir.
func stageData(archive, dir string) (map[string]string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(h.Name, "data/") {
			continue
		}
		local := filepath.Join(dir, strconv.Itoa(len(out)))
		w, err := os.OpenFile(local, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(w, tr)
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		out[h.Name] = local
	}
}

// rewriteVolumeTar copies a volume's tar to w with each sqlite file replaced by its
// consistent copy, keeping the entry's owner and mode, and the file's -wal and
// -shm dropped: they belong to the live file the copy replaces.
func rewriteVolumeTar(src string, subst map[string]string, w io.Writer) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	tr, tw := tar.NewReader(f), tar.NewWriter(w)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return tw.Close()
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(path.Clean(h.Name), "/")
		if sqliteSidecar(name, subst) {
			continue
		}
		body := io.Reader(tr)
		if copyPath, ok := subst[name]; ok {
			data, err := os.ReadFile(copyPath)
			if err != nil {
				return err
			}
			h.Size, body = int64(len(data)), bytes.NewReader(data)
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := io.Copy(tw, body); err != nil {
			return err
		}
	}
}

func sqliteSidecar(name string, subst map[string]string) bool {
	for live := range subst {
		if name == live+"-wal" || name == live+"-shm" {
			return true
		}
	}
	return false
}

// images is every reference the install pulls: this service's pins and each owned
// need's addon image, which carries its own digest.
func (p *installPlan) images() []string {
	out := append([]string{}, p.pinned...)
	for _, r := range p.needs {
		if !contains(out, r.addon.Image) {
			out = append(out, r.addon.Image)
		}
	}
	return out
}

// registryOf is the host an image reference is pulled from, by docker's own rule:
// the first component is a host when it has a dot or a colon or is localhost.
func registryOf(ref string) string {
	first, _, found := strings.Cut(ref, "/")
	if found && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return first
	}
	return "docker.io"
}

// registryLogin is who install logs in as (JCT37): a token, never written to the
// tree or the archive, and the name it belongs to.
type registryLogin struct{ user, token string }

// pull logs in to each registry the images name, when a token was given, and pulls
// every image, before anything is written: a token that is wrong or expired fails
// the move on an untouched place, not halfway through a restore (JCT37).
func (p *installPlan) pull(login registryLogin, run dockerRunner) (report, problems []string) {
	images := p.images()
	if login.token != "" {
		var registries []string
		for _, ref := range images {
			if r := registryOf(ref); r != "docker.io" && !contains(registries, r) {
				registries = append(registries, r)
			}
		}
		sort.Strings(registries)
		for _, r := range registries {
			if err := run(strings.NewReader(login.token), io.Discard, "docker", "login", r, "-u", login.user, "--password-stdin"); err != nil {
				return report, []string{fmt.Sprintf("logging in to %s as %s: %v", r, login.user, redact(err, login.token))}
			}
			report = append(report, fmt.Sprintf("logged in to %s as %s; the login is in this user's docker config, and `docker logout %s` removes it", r, login.user, r))
		}
	}
	for _, ref := range images {
		if err := run(nil, io.Discard, "docker", "pull", "--quiet", ref); err != nil {
			hint := ""
			if login.token == "" && registryOf(ref) != "docker.io" {
				hint = " — a private image needs --registry-token-file (or JCLOUD_REGISTRY_TOKEN) and --registry-user"
			}
			problems = append(problems, fmt.Sprintf("pulling %s: %v%s", ref, redact(err, login.token), hint))
			continue
		}
		report = append(report, "pulled "+ref)
	}
	return report, problems
}

// redact keeps a token out of an error that may have echoed its input.
func redact(err error, token string) string {
	if token == "" {
		return err.Error()
	}
	return strings.ReplaceAll(err.Error(), token, "***")
}

// installHosts is what DNS has to point here: each route's hosts as the target's
// configuration sets them, or as the manifest declares them.
func (p *installPlan) installHosts() []string {
	var hosts []string
	for _, r := range p.m.Routes {
		if v, ok := p.env[p.m.hostsVar(r)]; ok && v != "" && p.prof.RouteKind == "caddy" {
			for _, h := range strings.Split(v, ",") {
				hosts = append(hosts, strings.TrimSpace(h))
			}
			continue
		}
		hosts = append(hosts, r.Hosts...)
	}
	return hosts
}

// install is the command.
func install(args []string) int {
	usage := "jcloud install <archive> [--root <dir>] [--identity <file>] [--wait-timeout <seconds>] [--registry-token-file <path> --registry-user <name>]"
	root, identity, archive, tokenFile := ".", "", "", ""
	login := registryLogin{user: os.Getenv("JCLOUD_REGISTRY_USER"), token: os.Getenv("JCLOUD_REGISTRY_TOKEN")}
	wait := 600
	for len(args) > 0 {
		switch {
		case args[0] == "--registry-token-file" && len(args) >= 2:
			tokenFile, args = args[1], args[2:]
		case args[0] == "--registry-user" && len(args) >= 2:
			login.user, args = args[1], args[2:]
		case args[0] == "--root" && len(args) >= 2:
			root, args = args[1], args[2:]
		case args[0] == "--identity" && len(args) >= 2:
			identity, args = args[1], args[2:]
		case args[0] == "--wait-timeout" && len(args) >= 2:
			n, err := strconv.Atoi(args[1])
			if err != nil || n <= 0 {
				fmt.Fprintln(os.Stderr, usage)
				return 2
			}
			wait, args = n, args[2:]
		case !strings.HasPrefix(args[0], "-") && archive == "":
			archive, args = args[0], args[1:]
		default:
			fmt.Fprintln(os.Stderr, usage)
			return 2
		}
	}
	if archive == "" {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	if tokenFile != "" {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "jcloud install: %v\n", err)
			return 2
		}
		login.token = strings.TrimSpace(string(data))
	}
	// The name is the token's owner's, as the platform's workflow passes it; a
	// token without one is a login that cannot be spelled.
	if login.token != "" && login.user == "" {
		fmt.Fprintf(os.Stderr, "jcloud install: a registry token needs --registry-user (or JCLOUD_REGISTRY_USER), the account it belongs to\n%s\n", usage)
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud install: %v\n", err)
		return 2
	}
	return runInstall(archive, abs, identity, wait, login, installDocker)
}

func runInstall(archive, root, identity string, wait int, login registryLogin, run dockerRunner) int {
	refuse := func(what string, problems []string) int {
		fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s\n", what)
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "      %s\n", p)
		}
		return 1
	}
	// The host first, when the archive's platform declares one: docker has to be
	// there before anything below can ask it (JCT38).
	// Its timers wait for the end: the scripts they run are not laid down yet
	// (JCT43).
	h, err := archiveHost(archive)
	if err != nil {
		return refuse("nothing was written", []string{err.Error()})
	}
	if first := h.withoutInstalls(); first.declaresSetup() {
		fmt.Printf("  preparing the host from jcloud.yml's host:\n")
		if code := applyHostReport(root, first, hostRun); code != 0 {
			return refuse("the host is not what jcloud.yml declares, so nothing else was done", nil)
		}
	}
	p, problems := planInstall(archive, root, identity, run)
	if len(problems) > 0 {
		name := archive
		if p != nil {
			name = p.idx.Service
		}
		return refuse(name+" cannot be installed here; nothing was written", problems)
	}
	pulled, problems := p.pull(login, run)
	for _, r := range pulled {
		fmt.Printf("      %s\n", r)
	}
	if len(problems) > 0 {
		return refuse(p.idx.Service+": the images could not be pulled; nothing was written", problems)
	}
	if err := p.lay(root); err != nil {
		return refuse(p.idx.Service+": laying the files down", []string{err.Error()})
	}
	fmt.Printf("  \033[32m✓\033[0m %s: %d file(s) laid down under %s\n", p.idx.Service, len(p.write), root)
	report, problems := p.restore(archive, root, wait, run)
	for _, r := range report {
		fmt.Printf("      %s\n", r)
	}
	if len(problems) > 0 {
		return refuse(p.idx.Service+" did not come back whole", problems)
	}
	// The timers last, once the tree they run in is laid down and the service is
	// up, so the first run finds both (JCT43).
	if h.installsTimers() {
		fmt.Printf("  installing the timers jcloud.yml's host: declares\n")
		if code := applyHostReport(root, h, hostRun); code != 0 {
			return refuse(p.idx.Service+" is up, and its timers are not what jcloud.yml declares", nil)
		}
	}
	for _, n := range p.notes {
		fmt.Printf("      \033[33m!\033[0m %s\n", n)
	}
	for _, l := range p.idx.LeftMounts {
		fmt.Printf("      \033[33m!\033[0m %s was left behind on the source, as its export said\n", l)
	}
	if agreeProblems, _ := p.m.AgreeReport(root, AgreeInputs{}); len(agreeProblems) > 0 {
		fmt.Printf("      agree on the laid-down tree:\n")
		for _, a := range agreeProblems {
			fmt.Printf("        %s\n", a)
		}
	}
	if hosts := p.installHosts(); len(hosts) > 0 {
		fmt.Printf("\n  left by hand: point %s here; the edge asks for a certificate on the first request\n", strings.Join(hosts, ", "))
	}
	return 0
}
