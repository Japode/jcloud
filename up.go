// `jcloud up` — what stands between a prepared service and a deploy of it.
//
// A service still arrives by hand: somebody clones this repository, writes that
// service's variables into the `SERVICES_ENV` secret, finds the first digest and runs
// Deploy — four steps in docs/DEPLOY.md, in order, by a person who has read it (JC30).
// This build answers the first two of those questions and brings nothing up.
//
// Answering them separately is the whole point, and JC97 is why. A Deploy was dispatched
// for skillgist and refused six `FESPSP_IWL_*` keys: fespsp-iwl had been declared in JC90,
// its variables had never reached the secret, and `compose config` resolves every file
// whatever the deploy targets — so one unconfigured service had been stopping every other
// service's deploy since that commit, and the only thing that said so was a failed run.
// The keys are derivable from the manifest and the answer was always available; nothing
// asked it. This asks it.
//
// What it does not do, and both are refusals the deploy already makes. It does not build
// (JC7). And it does not resolve a moving tag into a digest (JC8) — a tag resolved on this
// side may not be the one that went green where the image was published, so what `--published`
// carries is what the publisher itself says it pushed, gathered by
// scripts/published-digests.sh out of GitHub's own record of the package. This file reaches
// no network: the record arrives as a file, which is the division `compare` made when it
// took two paths rather than a repository (JC29), and it keeps every sentence below
// testable without a token.
//
// With that record in hand the digest is resolved rather than pasted, and the deploy is
// handed its arguments verbatim — `DEPLOY_SERVICE=… DEPLOY_IMAGE=…@sha256:… bash
// scripts/deploy.sh …`, printed and never run. scripts/deploy.sh stays the only thing that
// brings anything up, which is also why this shells out to nothing.
//
// It writes nothing, least of all `.env`: the configuration is rendered from the secret on
// every deploy (JC9), so this answers with a list of missing keys and never a file.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Readiness is what one service's answer looks like, so the report and its exit
// code are computed from the same thing rather than from two readings of it.
type Readiness struct {
	Service string
	Missing []string // declared, and not in the env file
	Fixed   []string // declared with a fixed value and set anyway, which is ignored
	Pinned  string   // the digest already pinned, or ""

	// What the publisher says it pushed, when a record was consulted at all. Asked
	// apart from the rest because "nobody looked" and "nothing was published" are
	// two different answers, and reporting the first as the second would send
	// somebody to the service's CI over a token that was never passed.
	Asked       bool
	Published   string   // <image>@sha256:… the publisher's newest tagged version carries
	Tags        []string // the tags that version carries, so a reader can see which build it is
	Unpublished string   // the one word saying why there is nothing, when there is nothing
}

// Ready is the question the exit code answers: a deploy of this service would get
// past `check-env.sh` and have an image to bring up.
//
// A published digest is deliberately not enough. `deploy.sh` refuses every service
// with nothing in `.deploy-state/images.env` before it brings any of them up, so a
// service whose image exists and was never deployed still stops the deploy of every
// other service — which is the whole sentence this command exists to say (JC97).
// What the record changes is the answer's usefulness, not its verdict: the line
// under it is the command that fixes it.
func (r Readiness) Ready() bool { return len(r.Missing) == 0 && r.Pinned != "" }

// Behind is a deploy that is available and has not been run: the publisher has moved
// past what this machine is pinned at.
//
// Digests and not the whole reference, because the reference carries a name and the
// name has two lawful spellings — `${{ github.repository }}` expands with whatever
// capitals GitHub holds while the registry lowercases, which is the drift JC47 and
// JC48 were about. The digest is the identity; comparing the text around it would
// report a deploy waiting whenever two correct spellings met.
func (r Readiness) Behind() bool {
	return r.Published != "" && r.Pinned != "" && digestOf(r.Published) != digestOf(r.Pinned)
}

// digestOf is the `sha256:…` half of a pinned reference. A value with no `@` is
// returned whole: it is not a digest, and the comparison it loses is one that should
// report a difference rather than agreement.
func digestOf(ref string) string {
	if at := strings.Index(ref, "@"); at >= 0 {
		return ref[at+1:]
	}
	return ref
}

// RequiredEnv is every key this service's variables and routes put in `SERVICES_ENV`.
//
// Derived here from the manifest rather than read from `.env.example`, because the
// template is prose a person maintains and the manifest is the declaration `jcloud
// agree` already holds it to. The two rules are agree.go's own and are not restated:
// a variable with a fixed `value:` is not configuration and must not be set, and a
// route's hosts variable is named by hostsVar.
func RequiredEnv(m *Manifest) (required, fixed []string) {
	for _, e := range m.Env {
		key := m.SCREAM() + "_" + e.Name
		if e.Value != nil {
			fixed = append(fixed, key)
			continue
		}
		required = append(required, key)
	}
	for _, r := range m.Routes {
		required = append(required, m.hostsVar(r))
	}
	return required, fixed
}

// envKeys reads the keys an env file carries, and only the keys.
//
// The same `^[A-Z][A-Z0-9_]*=` shape scripts/check-env.sh reads, so that a key this
// sees and a key the deploy's own check sees are one key. Values are never parsed:
// this runs on the server against the real `.env`, and a report about configuration
// has no reason to hold any of it.
func envKeys(body string) map[string]bool {
	keys := map[string]bool{}
	re := regexp.MustCompile(`^([A-Z][A-Z0-9_]*)=`)
	for _, line := range strings.Split(body, "\n") {
		if match := re.FindStringSubmatch(line); match != nil {
			keys[match[1]] = true
		}
	}
	return keys
}

// pinnedDigest reads one service's image out of the deploy's own state file.
//
// The variable is the profile's image_var, which on Japode is `<SCREAM>_IMAGE=`,
// scripts/deploy.sh's image_var() spelling. Absent is not an error: a service that
// has never been deployed has no digest, and saying so is this command's job.
func pinnedDigest(prof *Profile, m *Manifest, body string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(prof.ImageVarOf(m)) + `=(.+)$`)
	if match := re.FindStringSubmatch(body); match != nil {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// Published is what each service's own CI pushed, as scripts/published-digests.sh
// read it out of GitHub's record of the package.
//
// The same `<kind> <name> <value>` lines survey.go's observations use, and for the
// reason stated there: a format a shell script emits with `printf` and this reads with
// `strings.Fields` is one neither side can get subtly wrong. An unknown kind is
// ignored rather than refused, so a collector that learns to report something more is
// not a collector this build stops reading.
//
//	gathered   at         2026-09-23T12:00:00Z
//	published  <service>  ghcr.io/owner/name@sha256:…
//	tag        <service>  latest
//	unpublished <service> never
//
// The digest is the value, never a tag this resolves: `tag` lines are reported so a
// reader can see which build was picked and are not what picks it (JC8).
//
// The stamp is not decoration. What this file says was true when it was gathered and
// at no other moment, so a reader deciding whether to deploy has to be told how old
// the answer is — a record the last deploy left behind is the usual case (JC99), and
// it is exactly as convincing as its date.
type Published struct {
	Ref  map[string]string   // service -> <image>@sha256:…
	Tags map[string][]string // service -> the tags that version carries
	None map[string]string   // service -> the word saying why nothing was published
	When string              // when it was gathered, as the collector stamped it
}

func ParsePublished(body string) *Published {
	p := &Published{Ref: map[string]string{}, Tags: map[string][]string{}, None: map[string]string{}}
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch f[0] {
		case "published":
			p.Ref[f[1]] = f[2]
		case "tag":
			p.Tags[f[1]] = append(p.Tags[f[1]], f[2])
		case "unpublished":
			p.None[f[1]] = f[2]
		case "gathered":
			p.When = f[2]
		}
	}
	return p
}

// why puts the collector's one word into the sentence a reader acts on. An
// unrecognised word is quoted rather than dropped: the collector is the party that
// knows, and a new reason arriving as silence is the failure JC66 names.
func (p *Published) why(service string) string {
	switch word := p.None[service]; word {
	case "":
		// Named in no line of the record at all. The collector walks services/*/ the
		// way everything else here does, so this is the record being older than the
		// service — gathered before it was declared.
		return "the record says nothing about it — it was gathered before this service was declared, so gather it again"
	case "never":
		return "its CI has published nothing — the image has to exist before anything can bring it up"
	case "unplaceable":
		return "its manifest's image is not a ghcr.io/<owner>/<name>, so nothing could ask what was published"
	default:
		return fmt.Sprintf("the collector reported %q", word)
	}
}

// Assess answers for one manifest against one env file's keys, one state file, and
// the publisher's record when one was gathered.
func Assess(prof *Profile, m *Manifest, keys map[string]bool, imagesEnv string, pub *Published) Readiness {
	r := Readiness{Service: m.Name, Pinned: pinnedDigest(prof, m, imagesEnv)}
	if pub != nil {
		r.Asked = true
		if ref, ok := pub.Ref[m.Name]; ok {
			r.Published, r.Tags = ref, pub.Tags[m.Name]
		} else {
			r.Unpublished = pub.why(m.Name)
		}
	}
	required, fixed := RequiredEnv(m)
	for _, key := range required {
		if !keys[key] {
			r.Missing = append(r.Missing, key)
		}
	}
	// A fixed value that somebody set anyway is reported and is not a refusal: the
	// deploy ignores it, so the cost is a person believing they configured something.
	for _, key := range fixed {
		if keys[key] {
			r.Fixed = append(r.Fixed, key)
		}
	}
	return r
}

// up is the command. With no service it answers for every manifest it finds, because
// the failure JC97 is about belongs to a service nobody was deploying.
func up(args []string) int {
	root, envPath, statePath, publishedPath := ".", "", "", ""
	var services []string
	for len(args) > 0 {
		switch args[0] {
		case "--root", "--env", "--state", "--published":
			if len(args) < 2 {
				fmt.Fprintf(os.Stderr, "jcloud up: %s takes a path\n", args[0])
				return 2
			}
			switch args[0] {
			case "--root":
				root = args[1]
			case "--env":
				envPath = args[1]
			case "--state":
				statePath = args[1]
			case "--published":
				publishedPath = args[1]
			}
			args = args[2:]
		default:
			services = append(services, args[0])
			args = args[1:]
		}
	}
	// Read before anything else, because every path below that the caller did not
	// name is one it names (JCT2).
	prof, err := LoadProfile(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud up: %v\n", err)
		return 2
	}
	if envPath == "" {
		envPath = filepath.Join(root, ".env")
	}
	if statePath == "" {
		statePath = filepath.Join(root, filepath.FromSlash(prof.PinFile))
	}

	envBody, err := os.ReadFile(envPath)
	if err != nil {
		// The one hard failure. Every answer below is about what that file carries,
		// so without it this would report every key as missing — which reads as a
		// configuration that was wiped rather than a file that was not found.
		fmt.Fprintf(os.Stderr, "jcloud up: %v\n  the deploy renders this from the SERVICES_ENV secret; --env says where it is\n", err)
		return 2
	}
	// Absent is empty: nothing has been deployed yet, and every service then reports
	// no digest, which is true.
	stateBody, _ := os.ReadFile(statePath)

	// No default path, unlike the two above. The record is an observation with a
	// freshness that matters — it says what the publisher had pushed at the moment it
	// was gathered — and a file quietly picked up from the tree could be a week old
	// while reading as the answer to "what is published now". Named or not consulted,
	// the way `survey` takes its observations as an argument (JC96).
	var pub *Published
	if publishedPath != "" {
		body, err := os.ReadFile(publishedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "jcloud up: %v\n  scripts/published-digests.sh is what gathers it\n", err)
			return 2
		}
		pub = ParsePublished(string(body))
		// Said once, at the top, before any digest below is read as current. An
		// unstamped record is named as such rather than passed over: it came from
		// something older than the stamp, and "no date" is a worse answer than an
		// old one only when nobody says so.
		if pub.When != "" {
			fmt.Printf("  the publisher was asked at %s (%s)\n", pub.When, filepath.Base(publishedPath))
		} else {
			fmt.Printf("  %s carries no date — what it says was true whenever it was gathered\n", filepath.Base(publishedPath))
		}
	}

	if len(services) == 0 {
		found, err := prof.Services(root)
		if err != nil || len(found) == 0 {
			fmt.Fprintf(os.Stderr, "jcloud up: no %s under %s\n", prof.Manifest, root)
			return 2
		}
		services = found
	}

	keys := envKeys(string(envBody))
	blocked := 0
	for _, service := range services {
		path := filepath.Join(root, prof.ManifestPath(service))
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s: %v\n", service, err)
			blocked++
			continue
		}
		m, err := Parse(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s: %v\n", path, err)
			blocked++
			continue
		}
		// Validated first, for run()'s reason next door: a manifest that does not
		// state what a service must declare disagrees with everything, and a list of
		// missing variables derived from it would be a second answer about one fault.
		if problems := m.Validate(); len(problems) > 0 {
			fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s (%s)\n", path, filepath.Dir(path))
			for _, problem := range problems {
				fmt.Fprintf(os.Stderr, "      %s\n", problem)
			}
			blocked++
			continue
		}
		if !report(prof, Assess(prof, m, keys, string(stateBody), pub), envPath) {
			blocked++
		}
	}

	if blocked == 0 {
		fmt.Printf("\n\033[32m%d service(s) could be deployed as things stand\033[0m\n", len(services))
		return 0
	}
	// Named rather than counted, because the point of the whole command is that the
	// service standing in the way is not the one somebody is trying to deploy.
	fmt.Fprintf(os.Stderr, "\n\033[31m%d of %d service(s) would stop a deploy — of any service, not only their own\033[0m\n",
		blocked, len(services))
	return 1
}

// report prints one service's answer and says whether it was ready.
func report(prof *Profile, r Readiness, envPath string) bool {
	for _, key := range r.Fixed {
		// Not a failure: the deploy ignores it. Worth saying, because somebody set it.
		fmt.Printf("  \033[33m!\033[0m %s: %s is set in %s and the manifest fixes that value — it is ignored\n",
			r.Service, key, filepath.Base(envPath))
	}
	if r.Ready() {
		fmt.Printf("  \033[32m✓\033[0m %s: every variable is set, pinned at %s\n", r.Service, shortDigest(r.Pinned))
		if r.Behind() {
			// Not a fault and not a refusal: what is pinned is running and is fine.
			// It is the one thing a report about readiness can say that nobody could
			// have derived from this machine alone.
			fmt.Printf("      its CI has since published %s%s — deploy it with:\n", shortDigest(r.Published), tagList(r.Tags))
			fmt.Printf("      %s\n", prof.DeployCommand(r.Service, r.Published))
		}
		return true
	}
	fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s\n", r.Service)
	for _, key := range r.Missing {
		fmt.Fprintf(os.Stderr, "      %s is declared and not in %s — add it to the SERVICES_ENV secret\n", key, filepath.Base(envPath))
		// On the diff where somebody can act on it, the way check-layout.sh does.
		fmt.Printf("::error::%s is declared by %s and missing from the configuration\n",
			key, filepath.ToSlash(prof.ManifestPath(r.Service)))
	}
	if r.Pinned == "" {
		reportUnpinned(prof, r)
	}
	return false
}

// reportUnpinned is the three answers to "there is no digest on this machine", and
// they are three because the thing to do about each is different: gather the record,
// go and look at the service's CI, or run one command.
func reportUnpinned(prof *Profile, r Readiness) {
	switch {
	case !r.Asked:
		fmt.Fprintf(os.Stderr, "      no image pinned — its CI has to publish one, and the deploy takes the digest from there (JC8)\n")
		fmt.Fprintf(os.Stderr, "      what it did publish is not looked up here: scripts/published-digests.sh gathers it, and --published reads it\n")
	case r.Published == "":
		fmt.Fprintf(os.Stderr, "      no image pinned, and %s\n", r.Unpublished)
	default:
		// The whole of the second half of JC30 in two lines: the digest came from
		// the party that pushed it, and the command below is the deploy's own
		// arguments rather than a sentence about them.
		fmt.Fprintf(os.Stderr, "      no image pinned, and its CI published %s%s — deploy it with:\n", shortDigest(r.Published), tagList(r.Tags))
		fmt.Fprintf(os.Stderr, "      %s\n", prof.DeployCommand(r.Service, r.Published))
	}
}

// The deploy command is the profile's (Profile.DeployCommand): on Japode it is what
// scripts/deploy.sh is called with, spelled the way it reads its two variables —
// printed, never run.
//
// Printed because this build shells out to nothing and because the deploy is the one
// thing that brings anything up: a reporter that could start it is a second deploy
// path, and JC1 already settled that there is one. Copyable in a single selection,
// which is the only interface a person needs between reading the answer and acting
// on it.

// tagList names the tags the resolved version carries, so a reader can tell which
// build was picked without going to the package page. Parenthesised and never the
// thing that picked it.
func tagList(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return " (" + strings.Join(tags, ", ") + ")"
}

// A digest is 71 characters of which the first few identify it to a person.
func shortDigest(image string) string {
	if at := strings.Index(image, "@sha256:"); at >= 0 && len(image) > at+20 {
		return image[:at+20] + "…"
	}
	return image
}
