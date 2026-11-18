// What `jcloud backup`, `jcloud restore-test` and `jcloud healthwatch` share
// (JCT46): the platform as the three jobs read it.
//
// The three were Japode/cloud's bash (scripts/backup.sh, restore-test.sh,
// healthwatch.sh and the backup-lib.sh they source), and that bash walked
// services/*/ and sourced its neighbours. Carried to a platform laid out another
// way, such as a site's own repository with a jcloud/ folder, it read a layout that
// was not there and backed up nothing, reporting success. Everything those scripts
// knew is something jcloud already reads: the manifests, through the Profile, the
// compose files beside them, backup.conf, the pin file and the edge's project
// name. So the jobs are verbs here, and a timer's run: names `jcloud backup`
// instead of a script the platform has to carry.
//
// They are ports, not redesigns. The snapshot keeps the shape the bash gave it
// (every volume at /backup/<key>, the vacuumed SQLite copies at /backup/_sqlite,
// the dumps at /backup/_mysql), restic's --host and --tag are the compose project's
// name (japode-cloud on Japode, which is what the bash wrote), the images are
// pinned by the same digests, and the staging, cache and drill names keep the
// container prefix the bash spelled japode-. A repository the bash filled is one
// these verbs read, back up into and prune, and the reverse.
//
// Each rule the bash carried a measured reason for is kept, and the reason sits
// beside the code that keeps it.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// resticImage is restic 0.18.1, by digest, the one Japode's backup-lib.sh pins: a
// backup taken with a different restic than the restore was rehearsed against is
// a restore nobody has exercised. JCLOUD_RESTIC_IMAGE overrides it.
const resticImage = "restic/restic@sha256:39d9072fb5651c80d75c7a811612eb60b4c06b32ffe87c2e9f3c7222e1797e76"

func resticImageRef() string {
	if v := os.Getenv("JCLOUD_RESTIC_IMAGE"); v != "" {
		return v
	}
	return resticImage
}

// The shape of the snapshot. These are the paths stored in it, so they stay
// stable across runs: restic deduplicates against the previous snapshot by path,
// and renaming one stores every volume again from scratch.
const (
	snapshotRoot = "/backup"
	sqliteKey    = "_sqlite"
	mysqlKey     = "_mysql"
)

// jobCmd is one command a job runs: argv, and where its streams and extra
// environment go. Secrets travel in Env and reach a container as a bare `-e NAME`,
// never `-e NAME=value`, so they are not in the process list every user can read.
type jobCmd struct {
	Name   string
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Env    []string
}

func (c jobCmd) String() string { return c.Name + " " + strings.Join(c.Args, " ") }

// jobRunner runs one command; a variable for the tests.
type jobRunner func(c jobCmd) error

func runJobCmd(c jobCmd) error {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = c.Stdin
	cmd.Stdout = c.Stdout
	if cmd.Stdout == nil {
		cmd.Stdout = io.Discard
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s %s: %w: %s", c.Name, firstArg(c.Args), err, lastLines(msg, 3))
		}
		return fmt.Errorf("%s %s: %w", c.Name, firstArg(c.Args), err)
	}
	return nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}

// out runs c and answers its stdout.
func (p *jobPlatform) out(c jobCmd) (string, error) {
	var b bytes.Buffer
	c.Stdout = &b
	err := p.run(c)
	return b.String(), err
}

// jobPlatform is the tree a job runs in, read once.
type jobPlatform struct {
	root       string
	prof       *Profile
	env        map[string]string
	project    string
	run        jobRunner
	services   []string // deployed: a digest pinned, so something brought them up
	undeployed []string // never deployed here, so they hold nothing to copy
	composeArg []string // `compose --env-file … -f …`, the edge and every deployed service
	volumes    []string
	svcCache   []string

	// backup.conf, read and checked.
	sqliteFiles []confEntry
	excludes    []confEntry
	mysql       []string

	// The job's own report.
	failures int
	w        io.Writer
}

type confEntry struct{ key, path string }

func (p *jobPlatform) say(format string, a ...any) {
	fmt.Fprintf(p.w, "\n\033[1m==> %s\033[0m\n", fmt.Sprintf(format, a...))
}
func (p *jobPlatform) note(format string, a ...any) {
	fmt.Fprintf(p.w, "    %s\n", fmt.Sprintf(format, a...))
}
func (p *jobPlatform) pass(format string, a ...any) {
	fmt.Fprintf(p.w, "    \033[32m✓ %s\033[0m\n", fmt.Sprintf(format, a...))
}

// fail is one service's problem: counted, and the rest of the run goes on, so the
// operator gets a backup and a signal where stopping gave them only the signal.
func (p *jobPlatform) fail(format string, a ...any) {
	fmt.Fprintf(p.w, "    \033[31m✗ %s\033[0m\n", fmt.Sprintf(format, a...))
	p.failures++
}

// readDotEnv reads root/.env without sourcing it: an unquoted list such as a
// HOSTS value would run as a command, and the failure would read like a missing
// binary rather than like a parse. The last line for a key wins, as sed | tail did.
func readDotEnv(root string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		return nil, err
	}
	return parseDotEnv(string(data)), nil
}

// loadJobPlatform reads what every job needs: the layout, .env, the project name,
// which services are deployed, and the compose invocation that covers them.
func loadJobPlatform(root string, run jobRunner, w io.Writer) (*jobPlatform, error) {
	prof, err := LoadProfile(root)
	if err != nil {
		return nil, err
	}
	env, err := readDotEnv(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("no .env here: the deploy renders it, and these jobs read their configuration from it")
	}
	if err != nil {
		return nil, err
	}
	project, err := composeProject(root)
	if err != nil {
		return nil, err
	}
	p := &jobPlatform{root: root, prof: prof, env: env, project: project, run: run, w: w}

	names, err := prof.Services(root)
	if err != nil {
		return nil, err
	}
	pins, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(prof.PinFile)))
	pinned := parseDotEnv(string(pins))
	p.composeArg = []string{"compose", "--project-directory", root, "--env-file", filepath.Join(root, ".env")}
	if pins != nil {
		p.composeArg = append(p.composeArg, "--env-file", filepath.Join(root, filepath.FromSlash(prof.PinFile)))
	}
	p.composeArg = append(p.composeArg, "-f", filepath.Join(root, "docker-compose.yml"))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(root, prof.ManifestPath(name)))
		if err != nil {
			return nil, err
		}
		m, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.ToSlash(prof.ManifestPath(name)), err)
		}
		compose := filepath.Join(root, prof.ServiceDir(name), "docker-compose.yml")
		if _, err := os.Stat(compose); err != nil {
			continue
		}
		// Has this service been brought up here? A digest pinned for it says so, or
		// a value in the environment (CI exports placeholders). A compose file whose
		// `${…_IMAGE:?}` has no value makes `compose config` refuse every other file
		// with it, so one service nobody has deployed took the whole backup down,
		// three runs in a row on Japode (JC101). Nothing it never started can hold
		// anything either. A platform that builds on its host has no pin to read.
		v := prof.ImageVarOf(m)
		deployed := os.Getenv(v) != "" || pinned[v] != "" || (m.Build != "" && prof.Builds)
		if !deployed {
			p.undeployed = append(p.undeployed, name)
			continue
		}
		p.services = append(p.services, name)
		p.composeArg = append(p.composeArg, "-f", compose)
	}
	return p, nil
}

// compose is the deploy's compose invocation, with args after it.
func (p *jobPlatform) compose(args ...string) jobCmd {
	return jobCmd{Name: "docker", Args: append(append([]string{}, p.composeArg...), args...)}
}

// refuseUndeployedWithContainers is the one state in which leaving a service out
// would lose data: it has containers, so something brought it up and its volumes
// may hold what the job exists to copy, and its pin has gone missing since.
func (p *jobPlatform) refuseUndeployedWithContainers() error {
	for _, svc := range p.undeployed {
		found, err := p.out(jobCmd{Name: "docker", Args: []string{"ps", "-a", "--filter", "name=^" + p.prof.Container(svc), "--format", "{{.Names}}"}})
		if err == nil && strings.TrimSpace(found) != "" {
			return fmt.Errorf("%s has no image pinned and yet has containers (%s): the pin file has lost a line it had, and leaving it out would be a volume nobody copies",
				svc, strings.Join(strings.Fields(found), " "))
		}
	}
	return nil
}

// loadVolumes asks compose for every named volume the files declare.
func (p *jobPlatform) loadVolumes() error {
	out, err := p.out(p.compose("config", "--volumes"))
	if err != nil {
		return err
	}
	p.volumes = strings.Fields(out)
	if len(p.volumes) == 0 {
		return errors.New("the compose files declare no named volumes")
	}
	return nil
}

// composeServices is every compose service, a need's database among them, read
// once and only when asked.
func (p *jobPlatform) composeServices() ([]string, error) {
	if p.svcCache != nil {
		return p.svcCache, nil
	}
	out, err := p.out(p.compose("config", "--services"))
	if err != nil {
		return nil, err
	}
	p.svcCache = strings.Fields(out)
	sort.Strings(p.svcCache)
	return p.svcCache, nil
}

// backupConfs is every backup.conf the jobs read: the root's, which is nobody's
// service and always read, and each deployed service's, beside its compose file.
// An undeployed service's directives go with it, or every line of its file would
// read as naming a volume nothing declares (JC101).
func (p *jobPlatform) backupConfs() []string {
	out := []string{"backup.conf"}
	for _, svc := range p.services {
		out = append(out, filepath.ToSlash(filepath.Join(p.prof.ServiceDir(svc), "backup.conf")))
	}
	return out
}

// readBackupConf reads every directive and refuses the run on any it cannot
// parse, all of them at once: a directive nobody could parse may be the one naming
// the database that matters, and a run that skips it and reports success is a
// backup whose contents nobody knows.
//
//	sqlite  <volume-key> <path inside the volume>
//	exclude <volume-key> <path or pattern inside the volume>
//	mysql   <volume-key>          (also the compose service holding it open)
func (p *jobPlatform) readBackupConf() error {
	var problems []string
	for _, rel := range p.backupConfs() {
		f, err := os.Open(filepath.Join(p.root, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			fields := strings.Fields(sc.Text())
			if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
				continue
			}
			where := fmt.Sprintf("%s:%d", rel, n)
			bad := func(msg string) { problems = append(problems, where+": "+msg) }
			switch kind := fields[0]; kind {
			case "sqlite", "exclude":
				switch {
				case len(fields) < 3:
					bad(fmt.Sprintf("'%s' takes a volume key and a path inside it", kind))
				case len(fields) > 3:
					bad(fmt.Sprintf("unexpected extra field after '%s'", fields[2]))
				case !contains(p.volumes, fields[1]):
					bad(fmt.Sprintf("no compose file declares a volume called '%s'", fields[1]))
				case kind == "sqlite":
					p.sqliteFiles = append(p.sqliteFiles, confEntry{fields[1], fields[2]})
				default:
					p.excludes = append(p.excludes, confEntry{fields[1], fields[2]})
				}
			case "mysql":
				switch {
				case len(fields) < 2:
					bad("'mysql' takes a volume key, which is also the compose service holding it open")
				case len(fields) > 2:
					bad(fmt.Sprintf("unexpected extra field after '%s': the dump comes from the container, so there is no path inside the volume to name", fields[1]))
				case !contains(p.volumes, fields[1]):
					bad(fmt.Sprintf("no compose file declares a volume called '%s'", fields[1]))
				default:
					services, err := p.composeServices()
					if err != nil {
						f.Close()
						return err
					}
					if !contains(services, fields[1]) {
						bad(fmt.Sprintf("no compose file declares a service called '%s': 'mysql' dumps from the running container of that name", fields[1]))
					} else {
						p.mysql = append(p.mysql, fields[1])
					}
				}
			default:
				bad(fmt.Sprintf("unknown directive '%s'", kind))
			}
		}
		f.Close()
	}
	if len(problems) > 0 {
		return fmt.Errorf("backup.conf does not parse:\n    %s", strings.Join(problems, "\n    "))
	}
	return nil
}

// dumped is whether a volume is dumped rather than copied: excluded from the
// snapshot whole, with _mysql/<key>.sql standing in for it. Asked here once,
// because the drill asking it in one step and not the other failed every run
// against a backup that restored completely (JC98).
func (p *jobPlatform) dumped(key string) bool { return contains(p.mysql, key) }

// The names the jobs give what they create, with the platform's container prefix,
// which Japode spells japode-.
func (p *jobPlatform) cacheVolume() string      { return p.prof.ContainerPrefix + "backup-cache" }
func (p *jobPlatform) stagingVolume() string    { return p.prof.ContainerPrefix + "backup-staging" }
func (p *jobPlatform) mysqlStaging() string     { return p.prof.ContainerPrefix + "backup-mysql-staging" }
func (p *jobPlatform) drillProject() string     { return p.prof.ContainerPrefix + "restore-test" }
func (p *jobPlatform) volume(key string) string { return p.project + "_" + key }

// resticConfig is the repository and its credentials, from .env.
func (p *jobPlatform) resticConfig() ([]string, error) {
	repo, pass := p.env["BACKUP_REPOSITORY"], p.env["BACKUP_PASSWORD"]
	if repo == "" {
		return nil, errors.New("BACKUP_REPOSITORY is empty in .env")
	}
	if pass == "" {
		return nil, errors.New("BACKUP_PASSWORD is empty in .env: a restic repository cannot be opened without it, and losing it loses every snapshot in it")
	}
	return []string{
		"RESTIC_REPOSITORY=" + repo, "RESTIC_PASSWORD=" + pass,
		"AWS_ACCESS_KEY_ID=" + p.env["BACKUP_S3_ACCESS_KEY_ID"],
		"AWS_SECRET_ACCESS_KEY=" + p.env["BACKUP_S3_SECRET_ACCESS_KEY"],
	}, nil
}

// restic is the `docker run` that reaches the repository, with mounts after it and
// then the restic arguments. A repository named by a path is a mounted disk the
// container sees only when it is passed through; s3:, b2:, sftp: and rest: are
// addresses restic dials itself.
func (p *jobPlatform) restic(env, mounts []string, args ...string) jobCmd {
	argv := []string{"run", "--rm",
		"-e", "RESTIC_REPOSITORY", "-e", "RESTIC_PASSWORD", "-e", "AWS_ACCESS_KEY_ID", "-e", "AWS_SECRET_ACCESS_KEY",
		"-v", p.cacheVolume() + ":/root/.cache/restic"}
	if repo := p.env["BACKUP_REPOSITORY"]; strings.HasPrefix(repo, "/") {
		argv = append(argv, "-v", repo+":"+repo)
	}
	for _, m := range mounts {
		argv = append(argv, "-v", m)
	}
	argv = append(argv, resticImageRef())
	return jobCmd{Name: "docker", Args: append(argv, args...), Env: env}
}

// sqlite is a `docker run` of the pinned sqlite image as root: its own default is
// uid 100, and a volume's contents belong to whatever uid wrote them, so the
// default reads some volumes and not others, which works until a service changes
// its user.
func (p *jobPlatform) sqlite(mounts []string, args ...string) jobCmd {
	argv := []string{"run", "--rm", "--user", "0:0"}
	for _, m := range mounts {
		argv = append(argv, "-v", m)
	}
	return jobCmd{Name: "docker", Args: append(append(argv, sqliteImageRef()), args...)}
}

// sh runs a shell script in the sqlite image with the mounts given. The arguments
// are the inner shell's positional parameters, which keeps a path with a space in
// it one word.
func (p *jobPlatform) sh(mounts []string, script string, args ...string) jobCmd {
	return p.shell(false, mounts, script, args...)
}

// shIn is sh with its standard input open, for a stream piped into a volume.
func (p *jobPlatform) shIn(mounts []string, script string, args ...string) jobCmd {
	return p.shell(true, mounts, script, args...)
}

func (p *jobPlatform) shell(stdin bool, mounts []string, script string, args ...string) jobCmd {
	argv := []string{"run", "--rm", "--user", "0:0"}
	if stdin {
		argv = append(argv, "-i")
	}
	argv = append(argv, "--entrypoint", "sh")
	for _, m := range mounts {
		argv = append(argv, "-v", m)
	}
	argv = append(argv, sqliteImageRef(), "-c", script, "sh")
	return jobCmd{Name: "docker", Args: append(argv, args...)}
}

// fileCount is how many files a volume holds, 0 for one that does not exist,
// which is the same answer as one that holds nothing. The backup and the drill
// both decide something on "empty", and answering it two ways is how one of the
// two checks ends up switched off.
func (p *jobPlatform) fileCount(volume string) int {
	out, err := p.out(p.sh([]string{volume + ":/vol:ro"}, `find /vol -type f | wc -l | tr -d "[:space:]"`))
	if err != nil {
		return 0
	}
	n := 0
	fmt.Sscan(strings.TrimSpace(out), &n)
	return n
}

// sqliteIntact asks a SQLite file whether it is whole; "" when it is.
func (p *jobPlatform) sqliteIntact(mount, file string) string {
	out, _ := p.out(p.sqlite([]string{mount}, "sqlite3", file, "PRAGMA integrity_check;"))
	if answer := strings.TrimSpace(out); answer != "ok" {
		if answer == "" {
			return "no answer"
		}
		return answer
	}
	return ""
}

// requiredVars is every `${NAME:?…}` the compose files require, which `config`
// refuses to resolve without.
var requiredVarRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*):?\?`)

// placeholders is a value for every variable the compose files require and the
// environment does not set, for a job that starts nothing and only has to read
// the files' shape: the health watch before a first deploy.
func (p *jobPlatform) placeholders() []string {
	files := []string{filepath.Join(p.root, "docker-compose.yml")}
	for _, svc := range append(append([]string{}, p.services...), p.undeployed...) {
		files = append(files, filepath.Join(p.root, p.prof.ServiceDir(svc), "docker-compose.yml"))
	}
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		data, _ := os.ReadFile(f)
		for _, m := range requiredVarRE.FindAllStringSubmatch(string(data), -1) {
			if k := m[1]; !seen[k] && os.Getenv(k) == "" && p.env[k] == "" {
				seen[k] = true
				out = append(out, k+"=placeholder")
			}
		}
	}
	sort.Strings(out)
	return out
}
