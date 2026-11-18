// `jcloud deploy` — bring a service up at the image its CI published, and go back
// on its own when it does not come up (JCT49, the port of Japode's
// scripts/deploy.sh and health-triage.sh).
//
// The bash walked services/*/ and spelled japode-, so a site whose server is its
// own jcloud/ folder (fespsp-institucional, FI194) had nothing to run on push. The
// rules are the bash's, read through jcloud.yml:
//
//   - It builds nothing: images arrive built, and building on the server is where
//     Viglet Cloud's worst incidents came from.
//   - It pins by digest, never by tag, in the pin file the layout names: a tag
//     resolved here may not be the one CI published, and a rollback aimed at a tag
//     aims at a target that moves.
//   - A service with no pin has never been deployed here and stays out of the
//     stack, so one directory nobody finished stops nobody else's deploy (JC97). One
//     with no pin and containers is refused: `up --remove-orphans` over a narrower
//     set would tear a live service down.
//   - The configuration is checked before anything comes up: `compose config`, and
//     `caddy validate` against the Caddyfile that goes live, since a syntax error in
//     one route takes the whole edge down.
//   - The running digest of each target is recorded first, and a target with none
//     is booting for the first time, which may take --first-boot instead of the
//     ordinary --wait-timeout (JC80).
//   - An expired wait is a question, not a verdict (JC81): docker is asked once
//     what each container is, and only an unwell one rolls back to the recorded
//     digests. All healthy is a deploy that passed late, and it says so.
//   - The edge is reloaded, since `up` does not recreate Caddy when only a mounted
//     file changed, and only dangling images are pruned: the old ones are the next
//     rollback.
//
// A platform's jcloud.yml can make it what `up` prints:
// `deploy: jcloud deploy {name} --image {image}`.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// deployNow is the clock the wait is measured with, swapped by the tests.
var deployNow = time.Now

// triageLine is one container as `compose ps --all` reports it, judged.
type triageLine struct {
	ok           bool
	service, why string
}

var oneOffRE = regexp.MustCompile(`-run-[0-9a-f]+$`)

// triage judges `compose ps --all --format '{{.Name}} {{.Service}} {{.State}}
// {{.Health}}'` output, the decision between a rollback and a green deploy, kept
// apart from docker so a test can reach it (JC84). ok is what `up --wait` accepts:
// healthy, or running with no healthcheck declared. A one-off `compose run`
// container is dropped by name: a deploy killed mid-validate leaves an exited one,
// and under --all it would roll back a stack with nothing wrong (JC83).
func triage(listing string) []triageLine {
	var out []triageLine
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || oneOffRE.MatchString(f[0]) {
			continue
		}
		svc, state, health := f[1], f[2], ""
		if len(f) >= 4 {
			health = f[3]
		}
		switch {
		case health == "healthy":
			out = append(out, triageLine{true, svc, "healthy"})
		case health == "" && state == "running":
			out = append(out, triageLine{true, svc, "running, and declares no healthcheck"})
		case health == "":
			out = append(out, triageLine{false, svc, state})
		default:
			out = append(out, triageLine{false, svc, state + ", " + health})
		}
	}
	return out
}

// pinImage rewrites one service's line in the pin file, keeping every other: one
// line per service, always. A new file is 0600, since it sits beside .env.
func pinImage(file, variable, image string) error {
	data, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if l != "" && !strings.HasPrefix(l, variable+"=") {
			lines = append(lines, l)
		}
	}
	lines = append(lines, variable+"="+image)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(file); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), mode); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// deployOpts is what the command line asks for.
type deployOpts struct {
	targets   []string
	image     string
	wait      int
	firstBoot int
}

// Deploy runs one deploy at root and answers its exit code.
func Deploy(root string, o deployOpts, run jobRunner, w io.Writer) int {
	die := func(format string, a ...any) int {
		fmt.Fprintf(w, "\n\033[31mfailed: %s\033[0m\n", fmt.Sprintf(format, a...))
		return 1
	}
	prof, err := LoadProfile(root)
	if err != nil {
		return die("%v", err)
	}
	names, err := prof.Services(root)
	if err != nil {
		return die("%v", err)
	}
	if len(names) == 0 {
		return die("no service is declared under %s", root)
	}
	for _, t := range o.targets {
		if !contains(names, t) {
			return die("unknown service: %s (there are: %s)", t, strings.Join(names, " "))
		}
	}
	pinFile := filepath.Join(root, filepath.FromSlash(prof.PinFile))

	// A new image is pinned before the stack is assembled, which is how a first
	// deploy brings a service in.
	if o.image != "" {
		if len(o.targets) != 1 {
			return die("--image pins one service, and %d were named", len(o.targets))
		}
		if !strings.Contains(o.image, "@sha256:") {
			return die("--image must be a digest (…@sha256:…), got: %s", o.image)
		}
		m, err := readManifest(root, prof, o.targets[0])
		if err != nil {
			return die("%v", err)
		}
		fmt.Fprintf(w, "\n\033[1m==> pinning %s at %s\033[0m\n", o.targets[0], o.image)
		if err := pinImage(pinFile, prof.ImageVarOf(m), o.image); err != nil {
			return die("%v", err)
		}
	}

	p, err := loadJobPlatform(root, run, w)
	if err != nil {
		return die("%v", err)
	}
	for _, t := range o.targets {
		if contains(p.undeployed, t) {
			return die("%s has no image pinned: run jcloud deploy %s --image …@sha256:…", t, t)
		}
	}
	if err := p.refuseUndeployedWithContainers(); err != nil {
		return die("%v", err)
	}
	if len(p.undeployed) > 0 {
		p.say("not in this stack: %s", strings.Join(p.undeployed, " "))
		for _, svc := range p.undeployed {
			p.note("%s is declared and has never been deployed: its CI has to publish an image, and its variables have to reach .env", svc)
		}
	}
	targets := o.targets
	if len(targets) == 0 {
		targets = p.services
	}
	if len(targets) == 0 {
		return die("no service has an image pinned: the first deploy needs jcloud deploy <service> --image …@sha256:…")
	}

	p.say("checking the configuration")
	if err := p.run(p.compose("config", "--quiet")); err != nil {
		return die("docker compose config: %v", err)
	}
	compose, _ := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	edge, _, edgeErr := edgeContainer(compose, prof)
	if prof.RouteKind == "caddy" && edgeErr == nil {
		if err := p.run(p.compose("run", "--rm", "--no-deps", "--entrypoint", "caddy", edge,
			"validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")); err != nil {
			return die("caddy validate: %v", err)
		}
	}

	// The before picture, taken before the pull, because after it the local tag
	// has already moved. RepoDigests and not the local id: an id does not survive a
	// prune, and a digest can be pulled again anywhere.
	previous := map[string]string{}
	var first []string
	for _, svc := range targets {
		if d := p.runningDigest(svc); d != "" {
			previous[svc] = d
		} else {
			first = append(first, svc)
		}
	}
	budget := o.wait
	if len(first) > 0 && o.firstBoot > budget {
		budget = o.firstBoot
		p.say("first boot of %s: waiting up to %ds and not %ds", strings.Join(first, " "), budget, o.wait)
	}
	wait := strconv.Itoa(budget)

	// Every service, a need's database among them: an image nobody pulled is
	// pulled by `up` instead, out of the budget meant for the healthchecks (JC80).
	p.say("pulling the images")
	if err := p.run(p.compose("pull", "--quiet")); err != nil {
		return die("docker compose pull: %v", err)
	}

	p.say("starting (waiting on healthchecks, up to %ss)", wait)
	started := deployNow()
	late := 0
	if err := p.run(p.compose("up", "-d", "--remove-orphans", "--wait", "--wait-timeout", wait)); err != nil {
		elapsed := int(deployNow().Sub(started).Seconds())
		p.say("the wait expired %ds in: asking docker what each container is now", elapsed)
		listing, _ := p.out(p.compose("ps", "--all", "--format", "{{.Name}} {{.Service}} {{.State}} {{.Health}}"))
		verdicts := triage(listing)
		// No answer is not a good answer: an empty listing reads like a stack with
		// nothing wrong.
		if len(verdicts) == 0 {
			return die("the wait expired and docker could not say what is running: the stack needs a person")
		}
		var unwell []string
		for _, v := range verdicts {
			if v.ok {
				fmt.Fprintf(w, "    %-34s %s\n", v.service, v.why)
			} else {
				unwell = append(unwell, v.service+" ("+v.why+")")
			}
		}
		if len(unwell) > 0 {
			for _, u := range unwell {
				p.note("%s", u)
			}
			logs := p.compose(append([]string{"logs", "--tail", "80"}, targets...)...)
			logs.Stdout = w
			p.run(logs)
			return p.rollback(pinFile, targets, previous, wait, die)
		}
		late = elapsed
		p.say("every container is healthy: the deploy passed late, not badly")
	}

	waited := int(deployNow().Sub(started).Seconds())
	if len(first) > 0 {
		p.say("the first boot of %s took %ds of the %ss allowed", strings.Join(first, " "), waited, wait)
	} else {
		p.say("healthy after %ds of the %ss allowed", waited, wait)
	}
	if prof.RouteKind == "caddy" && edgeErr == nil {
		p.say("reloading the edge")
		if err := p.run(p.compose("exec", "-T", edge, "caddy", "reload", "--config", "/etc/caddy/Caddyfile")); err != nil {
			return die("caddy reload: the edge is up with the old configuration: %v", err)
		}
	}
	p.say("state")
	ps := p.compose("ps")
	ps.Stdout = w
	p.run(ps)
	p.run(jobCmd{Name: "docker", Args: []string{"image", "prune", "-f"}})

	pins, _ := os.ReadFile(pinFile)
	pinned := parseDotEnv(string(pins))
	fmt.Fprintf(w, "\n\033[32mlive\033[0m\n")
	for _, svc := range targets {
		if m, err := readManifest(root, prof, svc); err == nil {
			fmt.Fprintf(w, "  %s  %s\n", svc, pinned[prof.ImageVarOf(m)])
		}
	}
	if late > 0 {
		fmt.Fprintf(w, "\n\033[33mthe %ss wait expired and every container was healthy anyway, %ds in: raise --wait-timeout\033[0m\n", wait, late)
	}
	return 0
}

// rollback pins each target back to its recorded digest and brings the stack up
// again. A target with none was booting for the first time, with nowhere to go.
func (p *jobPlatform) rollback(pinFile string, targets []string, previous map[string]string, wait string, die func(string, ...any) int) int {
	p.say("the deploy did not pass: returning to the previous digests")
	restored := false
	for _, svc := range targets {
		d, ok := previous[svc]
		if !ok {
			p.note("%s was not up before: nowhere to go back to", svc)
			continue
		}
		m, err := readManifest(p.root, p.prof, svc)
		if err != nil {
			return die("%v", err)
		}
		p.note("%s -> %s", svc, d)
		if err := pinImage(pinFile, p.prof.ImageVarOf(m), d); err != nil {
			return die("%v", err)
		}
		restored = true
	}
	if !restored {
		return die("first deploy of these services; nothing was restored")
	}
	if err := p.run(p.compose("up", "-d", "--wait", "--wait-timeout", wait)); err != nil {
		return die("the rollback did not come up either: the stack needs a person")
	}
	return die("reverted to the previous state")
}

// runningDigest is the digest the service's running container was pulled by, ""
// when it has never come up here.
func (p *jobPlatform) runningDigest(svc string) string {
	cid, err := p.out(p.compose("ps", "-q", svc))
	ids := strings.Fields(cid)
	if err != nil || len(ids) == 0 {
		return ""
	}
	img, err := p.out(jobCmd{Name: "docker", Args: []string{"inspect", "--format", "{{.Image}}", ids[0]}})
	if err != nil {
		return ""
	}
	d, err := p.out(jobCmd{Name: "docker", Args: []string{"inspect", "--format", "{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}", strings.TrimSpace(img)}})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(d)
}

// readManifest reads and parses one service's manifest under prof.
func readManifest(root string, prof *Profile, name string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, prof.ManifestPath(name)))
	if err != nil {
		return nil, err
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.ToSlash(prof.ManifestPath(name)), err)
	}
	return m, nil
}

// deployCmd is the command.
func deployCmd(args []string) int {
	usage := "jcloud deploy [<service> …] [--image <ref@sha256:…>] [--root <dir>] [--wait-timeout <seconds>] [--first-boot <seconds>]"
	root := "."
	o := deployOpts{wait: restoreWaitTimeout}
	if v, err := strconv.Atoi(os.Getenv("WAIT_TIMEOUT")); err == nil && v > 0 {
		o.wait = v
	}
	seconds := func(s string) (int, bool) { n, err := strconv.Atoi(s); return n, err == nil && n > 0 }
	for len(args) > 0 {
		switch {
		case args[0] == "--root" && len(args) >= 2:
			root, args = args[1], args[2:]
		case args[0] == "--image" && len(args) >= 2:
			o.image, args = args[1], args[2:]
		case (args[0] == "--wait-timeout" || args[0] == "--first-boot") && len(args) >= 2:
			n, ok := seconds(args[1])
			if !ok {
				fmt.Fprintln(os.Stderr, usage)
				return 2
			}
			if args[0] == "--wait-timeout" {
				o.wait = n
			} else {
				o.firstBoot = n
			}
			args = args[2:]
		case !strings.HasPrefix(args[0], "-"):
			o.targets, args = append(o.targets, args[0]), args[1:]
		default:
			fmt.Fprintln(os.Stderr, usage)
			return 2
		}
	}
	return Deploy(root, o, runJobCmd, os.Stdout)
}
