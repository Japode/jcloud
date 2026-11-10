// Does the manifest agree with the repository it sits in?
//
// agree.go asks the other question: whether the manifest matches the four files
// this repository deploys beside it. That check runs here, which is the wrong
// place for it to fail — the port was changed by somebody in the service's
// repository, whose own CI went green (JC18). This one runs there, against the
// Dockerfile, the standalone Caddyfile and the workflow that publishes the image,
// so the person who made the change is the person who sees it fail.
//
// It reads those files and no others. What a service's repository holds beyond
// them is its own business: this asserts the facts the platform is being asked to
// honour and says nothing about the rest.
//
// Textual and read-only, for the same reason agree.go is: a half-understood parse
// of a file another tool owns is a second grammar to keep in step. Where it cannot
// see — a workflow that names its image through a GitHub expression nobody expanded
// — it says so rather than passing, which is the rule JC33 cost this repository once
// already.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Caddy's own default when a reverse_proxy block declares no health_interval.
// Compared against the manifest's rather than skipped: a block that says nothing
// is a block that probes every 30s, and "unstated" and "agrees" are not the same
// answer (JC41).
const caddyDefaultHealthInterval = "30s"

// RepoLayout is where in the repository the two files this reads by name are.
//
// Both were literals until one repository put them somewhere else. skillgist keeps
// its Dockerfile at its root, so `filepath.Join(root, "Dockerfile")` was correct
// every time anyone ran this; fespsp-institucional builds one image from two
// checkouts and keeps it at `docker/production/Dockerfile`, and the check refused on
// the path rather than asserting anything about the port it exists to assert (JC87).
//
// Paths passed in, and not a field in the manifest. Where a repository keeps its
// build context is not a fact it shares with the platform: `agree` compares the four
// files deployed on this side, and a field that means nothing here is a field that
// goes stale here.
//
// The empty string is each file's own name at the root, so a caller that knows of no
// such repository passes a zero value and gets what it always got.
type RepoLayout struct {
	Dockerfile string
	Caddyfile  string
}

func (l RepoLayout) dockerfile() string { return orDefault(l.Dockerfile, "Dockerfile") }
func (l RepoLayout) caddyfile() string  { return orDefault(l.Caddyfile, "Caddyfile") }

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// AgreeWithRepo reports every disagreement between the manifest and the repository
// it sits in.
//
// repository is what GitHub calls that repository — `owner/name`. It is a
// parameter and not read from a file because no file in the repository states it:
// a workflow writes `ghcr.io/${{ github.repository }}` and the value arrives from
// Actions. Unsupplied, an image named that way is reported as unchecked.
func (m *Manifest) AgreeWithRepo(root, repository string, layout RepoLayout) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	// Reported at the path the caller named, never at the default: "no such file:
	// Dockerfile" in a repository that has one under docker/ sends its reader
	// looking for the wrong defect.
	at := layout.dockerfile()
	dockerfile, err := os.ReadFile(filepath.Join(root, at))
	if err != nil {
		// The one file that is not optional. The manifest promises the platform an
		// image built from this repository, and nothing else here states its port.
		bad("%s: %v", at, err)
	} else {
		p = append(p, m.agreeDockerfile(at, string(dockerfile))...)
	}

	// Optional: a service that runs no stack of its own has no Caddyfile, and
	// absent with nothing to say is agreement — the same rule backup.conf gets in
	// agree.go.
	//
	// Which is why this one takes the caller's path too, and is the reason it is in
	// this struct rather than only the Dockerfile JC87 names. A caller pointing the
	// route comparison at `docker/Caddyfile` while this looked for one at the root
	// would find none, say nothing, and pass — a silent skip, which is the shape
	// JC66 refuses.
	ca := layout.caddyfile()
	if caddyfile, err := os.ReadFile(filepath.Join(root, ca)); err == nil {
		p = append(p, m.agreeCaddyfile(ca, string(caddyfile))...)
	}

	// A service built on the platform's host publishes nothing (JCT7), so there is
	// no workflow to hold to an image it does not have.
	if m.Build == "" {
		p = append(p, m.agreeWorkflows(root, repository)...)
	}
	return p
}

func (m *Manifest) agreeDockerfile(at, body string) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	var exposed []string
	for _, arg := range dockerfileDirective(body, "EXPOSE") {
		for _, word := range directiveWords(arg) {
			// `3000/tcp` and `3000` are the same port declared two ways.
			exposed = append(exposed, strings.SplitN(word, "/", 2)[0])
		}
	}
	switch {
	case len(exposed) == 0:
		bad("%s: nothing is EXPOSEd, so the port the manifest declares (%d) is stated in one repository only", at, m.Port)
	case !contains(exposed, fmt.Sprint(m.Port)):
		bad("%s: EXPOSE names %s and the manifest declares port %d", at, strings.Join(exposed, ", "), m.Port)
	}

	// The image's own HEALTHCHECK is the only machine-readable claim in this
	// repository that the application answers the declared path. Its start_period
	// and interval are deliberately not compared: those belong to the container the
	// platform runs, and the manifest's are what services/<name>/docker-compose.yml
	// carries — a different healthcheck with a different job.
	probe := ""
	for _, check := range dockerfileDirective(body, "HEALTHCHECK") {
		// A later HEALTHCHECK replaces an earlier one, which is the rule the
		// builder itself follows — and `HEALTHCHECK NONE` is an image saying it has
		// no check, not one probing a path called NONE.
		if fields := strings.Fields(check); len(fields) == 0 || strings.EqualFold(fields[0], "NONE") {
			probe = ""
			continue
		}
		probe = check
	}
	if probe == "" {
		bad("%s: no HEALTHCHECK the image runs, so whether it answers %s is unchecked here — the deploy waits on that path and the edge probes it", at, m.Health.Path)
	} else {
		if !strings.Contains(probe, m.Health.Path) {
			bad("%s: the HEALTHCHECK does not probe %s, which the manifest declares", at, m.Health.Path)
		}
		if !strings.Contains(probe, fmt.Sprint(m.Port)) {
			bad("%s: the HEALTHCHECK does not probe port %d", at, m.Port)
		}
	}

	// One direction only. A VOLUME the image declares and the manifest does not is
	// a directory the platform mounts nothing at, so it is written into the
	// container's writable layer and thrown away on the next deploy — and it is
	// backed up by nothing. The reverse is not a fault: a volume can be mounted at
	// a path no VOLUME names, which is what every bind mount is.
	for _, arg := range dockerfileDirective(body, "VOLUME") {
		for _, path := range directiveWords(arg) {
			if m.volumeAt(path) == nil {
				bad("%s: VOLUME %s is declared by the image and by no volume in the manifest — the platform would mount nothing there and the next deploy would take it", at, path)
			}
		}
	}
	return p
}

func (m *Manifest) volumeAt(path string) *Volume {
	for i, v := range m.Volumes {
		if v.Path == path {
			return &m.Volumes[i]
		}
	}
	return nil
}

// The service's own Caddyfile, which serves its standalone stack. Only the facts
// the manifest names are compared: the upstream port, the probed path and how
// often it is probed.
//
// The `reverse_proxy` bodies are not, and that is not an oversight. Which block
// answers for which of the manifest's routes is not derivable from a file whose
// address blocks read its own variables — `{$SITE_HOSTS:…}` here against the
// `{$SKILLGIST_HOSTS:…}` the platform derives — so a body comparison would have to
// guess the pairing. scripts/check-upstream-routes.sh already compares those
// bodies, in the repository that holds both copies of them and can see which is
// which (JC29).
func (m *Manifest) agreeCaddyfile(at, body string) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	if len(m.Routes) == 0 {
		return nil
	}

	blocks := reverseProxyBlocks(body)
	if len(blocks) == 0 {
		bad("%s: no reverse_proxy, and the manifest declares %d route(s)", at, len(m.Routes))
		return p
	}

	want := m.Health.interval()
	for _, b := range blocks {
		where := fmt.Sprintf("%s:%d", at, b.line)

		// The upstream's name is this stack's own — `app` here, the short name in
		// the platform's route — so only the port is comparable.
		port := ""
		if i := strings.LastIndex(b.upstream, ":"); i >= 0 {
			port = b.upstream[i+1:]
		}
		if port != fmt.Sprint(m.Port) {
			bad("%s: reverse_proxy %s does not aim at port %d, which the manifest declares", where, b.upstream, m.Port)
		}

		switch uri := b.directive("health_uri"); uri {
		case "":
			bad("%s: the block probes nothing, and the manifest declares health.path %s", where, m.Health.Path)
		case m.Health.Path:
		default:
			bad("%s: health_uri is %s and the manifest declares %s", where, uri, m.Health.Path)
		}

		// Absent means Caddy's default and not "unstated", so it is compared like
		// any other value.
		got := b.directive("health_interval")
		if got == "" {
			got = caddyDefaultHealthInterval
		}
		if got != want {
			bad("%s: the block probes every %s and the manifest declares %s", where, got, want)
		}
	}
	return p
}

// Every workflow in the repository, and the images they name.
//
// Not one file called publish.yml: what matters is that something here pushes the
// image the manifest declares and that nothing here pushes a different one, and
// which file does it is the repository's business.
func (m *Manifest) agreeWorkflows(root, repository string) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		bad(".github/workflows: %v — nothing here publishes %s", err, m.Image)
		return p
	}

	found := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		at := ".github/workflows/" + name
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			bad("%s: %v", at, err)
			continue
		}

		body := string(raw)
		if repository != "" {
			body = githubRepositoryRE.ReplaceAllLiteralString(body, repository)
		}
		if strings.Contains(body, "ghcr.io/${{") {
			bad("%s: an image is named through a GitHub expression and nothing expanded it, so which image this pushes is unchecked — `--repository ${{ github.repository }}` is what supplies it", at)
			continue
		}

		for _, ref := range ghcrRefRE.FindAllString(body, -1) {
			// EqualFold and not ==: `${{ github.repository }}` expands to the name
			// GitHub spells, capital and all, while the manifest carries the name
			// the registry accepts — GHCR refuses an uppercase tag outright, so
			// `ghcr.io/Owner/x` and `ghcr.io/owner/x` are one package written two
			// ways and never drift. Whether the manifest itself may carry a capital
			// is a different question, and Validate's.
			if strings.EqualFold(ref, m.Image) {
				found = true
				continue
			}
			bad("%s: pushes %s and the manifest declares %s", at, ref, m.Image)
		}
	}

	if !found && len(p) == 0 {
		bad(".github/workflows: nothing here names %s, so the image the platform pulls is published by something this repository does not describe", m.Image)
	}
	return p
}

// `${{ github.repository }}`, however it was spaced.
var githubRepositoryRE = regexp.MustCompile(`\$\{\{\s*github\.repository\s*\}\}`)

// A GHCR repository path, without the tag or digest — the manifest names neither,
// and `image:` refuses both (see Validate). The character class stops at `:` and
// `@` for exactly that reason.
//
// Case-insensitive, because `${{ github.repository }}` expands to whatever
// capitalisation GitHub holds and a workflow is right to write it that way — what
// reaches the registry is lowered on the way. Matched here and compared with
// EqualFold above, so two spellings of one package never read as drift.
var ghcrRefRE = regexp.MustCompile(`(?i)ghcr\.io/[a-z0-9][a-z0-9._/-]*`)

// dockerfileDirective returns the argument of every instance of one instruction,
// with `\` continuations joined, so a HEALTHCHECK written over three lines is read
// as one string.
func dockerfileDirective(body, instruction string) []string {
	var out []string
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if !strings.EqualFold(fields[0], instruction) {
			continue
		}
		arg := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		for strings.HasSuffix(arg, `\`) && i+1 < len(lines) {
			i++
			next := strings.TrimSpace(lines[i])
			if strings.HasPrefix(next, "#") {
				// A comment between two continued lines is not part of either.
				continue
			}
			arg = strings.TrimSpace(strings.TrimSuffix(arg, `\`)) + " " + next
		}
		out = append(out, strings.TrimSpace(arg))
	}
	return out
}

// The JSON-array form and the shell form of EXPOSE and VOLUME are the same list of
// words once the punctuation is out of the way.
func directiveWords(arg string) []string {
	return strings.Fields(strings.NewReplacer("[", " ", "]", " ", `"`, " ", "'", " ", ",", " ").Replace(arg))
}

// One `reverse_proxy` in a Caddyfile: what it dials, where it starts, and the
// directives inside its block.
type proxyBlock struct {
	upstream string
	line     int
	body     []string
}

// directive returns the argument of the first line in the block whose first field
// is name, or "" if there is none.
func (b proxyBlock) directive(name string) string {
	for _, line := range b.body {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == name {
			return fields[1]
		}
	}
	return ""
}

func reverseProxyBlocks(body string) []proxyBlock {
	var out []proxyBlock
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines); i++ {
		fields := strings.Fields(stripCaddyComment(lines[i]))
		if len(fields) == 0 || fields[0] != "reverse_proxy" {
			continue
		}
		b := proxyBlock{line: i + 1}
		if len(fields) > 1 && fields[1] != "{" {
			b.upstream = fields[1]
		}
		if fields[len(fields)-1] == "{" {
			// Placeholders such as {remote_host} are balanced, so counting braces
			// finds the end of the block without a grammar.
			depth := 1
			for i++; i < len(lines); i++ {
				line := stripCaddyComment(lines[i])
				if depth += strings.Count(line, "{") - strings.Count(line, "}"); depth <= 0 {
					break
				}
				b.body = append(b.body, strings.TrimSpace(line))
			}
		}
		out = append(out, b)
	}
	return out
}

// A Caddyfile comment runs from an unquoted `#` to the end of the line. Only the
// two forms that occur — a whole-line comment and one after whitespace — are
// recognised, because a `#` inside a value is a directive this does not read.
func stripCaddyComment(line string) string {
	if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "#") {
		return ""
	}
	if i := strings.Index(line, " #"); i >= 0 {
		return line[:i]
	}
	return line
}
