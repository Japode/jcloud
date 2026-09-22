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
// side may not be the one that went green where the image was published, so the digest it
// reports is the one already pinned and never one it looked up. Resolving what a
// service's CI published, and handing the rest to scripts/deploy.sh, is the other half of
// JC30 and is not here.
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
}

// Ready is the question the exit code answers: a deploy of this service would get
// past `check-env.sh` and have an image to bring up.
func (r Readiness) Ready() bool { return len(r.Missing) == 0 && r.Pinned != "" }

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
// `<SCREAM>_IMAGE=` is scripts/deploy.sh's image_var() spelling, which SCREAM already
// derives. Absent is not an error: a service that has never been deployed has no
// digest, and saying so is this command's job.
func pinnedDigest(m *Manifest, body string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(m.SCREAM()+"_IMAGE") + `=(.+)$`)
	if match := re.FindStringSubmatch(body); match != nil {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// Assess answers for one manifest against one env file's keys and one state file.
func Assess(m *Manifest, keys map[string]bool, imagesEnv string) Readiness {
	r := Readiness{Service: m.Name, Pinned: pinnedDigest(m, imagesEnv)}
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
	root, envPath, statePath := ".", "", ""
	var services []string
	for len(args) > 0 {
		switch args[0] {
		case "--root", "--env", "--state":
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
			}
			args = args[2:]
		default:
			services = append(services, args[0])
			args = args[1:]
		}
	}
	if envPath == "" {
		envPath = filepath.Join(root, ".env")
	}
	if statePath == "" {
		statePath = filepath.Join(root, ".deploy-state", "images.env")
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

	if len(services) == 0 {
		found, err := filepath.Glob(filepath.Join(root, "services", "*", "japode.yml"))
		if err != nil || len(found) == 0 {
			fmt.Fprintf(os.Stderr, "jcloud up: no services/*/japode.yml under %s\n", root)
			return 2
		}
		for _, path := range found {
			services = append(services, filepath.Base(filepath.Dir(path)))
		}
	}

	keys := envKeys(string(envBody))
	blocked := 0
	for _, service := range services {
		path := filepath.Join(root, "services", service, "japode.yml")
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
		if !report(Assess(m, keys, string(stateBody)), envPath) {
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
func report(r Readiness, envPath string) bool {
	for _, key := range r.Fixed {
		// Not a failure: the deploy ignores it. Worth saying, because somebody set it.
		fmt.Printf("  \033[33m!\033[0m %s: %s is set in %s and the manifest fixes that value — it is ignored\n",
			r.Service, key, filepath.Base(envPath))
	}
	if r.Ready() {
		fmt.Printf("  \033[32m✓\033[0m %s: every variable is set, pinned at %s\n", r.Service, shortDigest(r.Pinned))
		return true
	}
	fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s\n", r.Service)
	for _, key := range r.Missing {
		fmt.Fprintf(os.Stderr, "      %s is declared and not in %s — add it to the SERVICES_ENV secret\n", key, filepath.Base(envPath))
		// On the diff where somebody can act on it, the way check-layout.sh does.
		fmt.Printf("::error::%s is declared by services/%s/japode.yml and missing from the configuration\n", key, r.Service)
	}
	if r.Pinned == "" {
		fmt.Fprintf(os.Stderr, "      no image pinned — its CI has to publish one, and the deploy takes the digest from there (JC8)\n")
	}
	return false
}

// A digest is 71 characters of which the first few identify it to a person.
func shortDigest(image string) string {
	if at := strings.Index(image, "@sha256:"); at >= 0 && len(image) > at+20 {
		return image[:at+20] + "…"
	}
	return image
}
