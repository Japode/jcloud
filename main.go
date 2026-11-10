// jcloud — the tool docs/JCLOUD.md specifies. This build implements four commands.
//
//	jcloud check [--repo <root> [--repository <owner/name>]
//	             [--dockerfile <path>] [--caddyfile <path>]] <japode.yml> …
//	jcloud agree [--routes <caddy-adapt.json | nginx-T.txt>] [--compose <config.json>]
//	             <root> <japode.yml> …
//	jcloud compare <ours.yml> <theirs.yml>
//	jcloud env <verb> …
//
// `check` reads each manifest and refuses one that does not state what a service
// must declare. With `--repo` it goes on to compare the manifest against the
// service's own repository, which is the check that runs *there* (JC27). `agree`
// is the other direction: the manifest against the four files this repository
// deploys beside it. `compare` is the third: this repository's copy of a manifest
// against the service's own, which is every shared fact in one question.
//
// Those three are about a manifest, and none of them renders, resolves a digest or
// reaches the network. That is JC30's, and it is why `compare` takes two paths
// rather than a repository: a check that needed the network could not run on a
// fork's pull request, which is where the interesting mistakes arrive. The fetching
// lives in the script that holds the token, scripts/check-upstream-manifest.sh.
//
// `env` is about none of that. It is the configuration — .env.age, where a key is
// readable and its value is not (JC92) — and it is here rather than in a script
// beside the others because it needs age, and this is what already arrives with a
// pinned toolchain. It reads no manifest and the three above read no configuration.
//
// Exit codes are the whole contract, because a CI step reads nothing else: 0 every
// manifest is valid, 1 at least one is not, 2 the arguments were wrong.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
)

// version is the tool's own, distinct from the schema's, printed so that a report
// about a manifest says which build read it (cloud's JC31).
//
// The tag is the version, so it is read from the build and not written here: a
// literal is one more thing to bump per release, and it went stale the moment a tag
// moved past it. GoReleaser sets it with -ldflags "-X main.version=…". Otherwise
// `go install`/`go run module@vX` records the module version in the binary, and a
// build from a checkout records the VCS pseudo-version or "(devel)".
var version = ""

// Version is what this build calls itself.
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage()
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}

	switch args[0] {
	case "check":
		os.Exit(check(args[1:]))
	case "agree":
		// The repository root is an argument and not derived from the manifest's
		// path, because the manifest is read where it sits and the files it is
		// compared against are found from the root — and a service adopted from
		// another repository will not have the two in the same place.
		rest := args[1:]
		adapted, compose := "", ""
	agreeFlags:
		for len(rest) >= 2 {
			switch rest[0] {
			// Files and not commands to run: this build shells out to nothing, and
			// `caddy adapt`, `nginx -T` and `docker compose config` belong to the
			// script that already has pinned images of them. route_json.go and
			// compose_model.go say why each is read at all. --adapted is the
			// spelling from before nginx (JCT3), kept so a platform script
			// written against it still runs.
			case "--routes", "--adapted":
				adapted = rest[1]
			case "--compose":
				compose = rest[1]
			default:
				break agreeFlags
			}
			rest = rest[2:]
		}
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "jcloud agree [--routes <caddy-adapt.json | nginx-T.txt>] [--compose <compose-config.json>] <repository-root> <japode.yml> …")
			os.Exit(2)
		}
		os.Exit(agree(rest[1:], rest[0], adapted, compose))
	case "compare":
		// Two paths and no fetching: the copy in the service's repository arrives
		// as a file somebody else read, which is what keeps the network out of
		// this build. scripts/check-upstream-manifest.sh is what holds the token.
		rest := args[1:]
		if len(rest) != 2 {
			fmt.Fprintln(os.Stderr, "jcloud compare <ours.yml> <theirs.yml>")
			os.Exit(2)
		}
		os.Exit(compareManifests(rest[0], rest[1]))
	case "survey":
		// Takes the observations as a file and reaches no network, the division
		// JC29 already made for `compare` — scripts/survey-server.sh holds the SSH
		// credential and does the reading.
		os.Exit(survey(args[1:]))
	case "up":
		// Reports and brings nothing up. `--published` is what makes the digest
		// resolved rather than pasted, and it is a file for `survey`'s reason: the
		// asking belongs in scripts/published-digests.sh, which holds the token.
		os.Exit(up(args[1:]))
	case "env":
		// The only command here that is about configuration rather than about a
		// manifest, and the only one that writes a file. Both are deliberate and
		// neither reverses JC34: what that refused is rendering the four
		// hand-written files from a manifest, and .env was never one of them —
		// it is rendered from a secret today, by the workflow.
		os.Exit(envCmd(args[1:]))
	case "version", "--version":
		// The compiler is printed beside the tool's own version because it is the
		// other half of what determines this binary's behaviour, and a report about
		// a manifest is only traceable if both are known (JC31). runtime.Version()
		// is what actually built this, not what go.mod asked for.
		fmt.Printf("jcloud %s (schema version %d, built with %s)\n", Version(), SchemaVersion, runtime.Version())
	default:
		fmt.Fprintf(os.Stderr, "jcloud: no such command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `jcloud %s — what a service declares (docs/JCLOUD.md)

  jcloud check <japode.yml> …          refuse a manifest that does not state
                                       what a service must declare
  jcloud check --repo <root>           that, and then compare it against the
       [--repository <owner/name>]     Dockerfile, Caddyfile and workflows of
       [--dockerfile <path>]           the service's own repository — the two
       [--caddyfile <path>]            paths are relative to <root> and default
       <japode.yml> …                  to that file's name in it
  jcloud agree <root> <japode.yml> …   that, and then compare it against the
       [--routes <path>]               compose file, the route, backup.conf and
                                       .env.example deployed beside it. The
                                       route is read from --routes: caddy adapt
                                       JSON, or nginx -T when jcloud.yml says
                                       route.kind: nginx. --compose takes
       [--compose <path>]              docker compose config --no-interpolate
                                       --format json, and without it the
                                       service's own compose file is read
  jcloud compare <ours> <theirs>       report where this repository's copy of a
                                       manifest and the service's own disagree
  jcloud survey [--root <dir>]         the server, as scripts/survey-server.sh
       <observations>                  observed it, against what services/*/
                                       declares. Reads a file, not a host.
  jcloud up [<service> …]              what stands between each service and a
       [--root <dir>] [--env <path>]   deploy: the variables its manifest
       [--state <path>]                declares and the configuration lacks, and
       [--published <path>]            whether a digest is pinned. With the
                                       record scripts/published-digests.sh
                                       gathers, it also says what that service's
                                       CI published and prints the deploy's own
                                       arguments. Brings nothing up.
  jcloud env <verb>                    the configuration in .env.age: list, get,
                                       set, unset, render — "jcloud env" alone
                                       prints the verbs and their flags
  jcloud version                       this build, and the schema version

Every problem is reported at once: a manifest is written in one sitting, and a
caller who fixes one field and runs again has been sent round a loop this could
have answered the first time.

init and adopt are specified in docs/JCLOUD.md and not implemented.
`, Version())
}

// check parses its own flags rather than reaching for the flag package, which
// would reorder the manifest paths behind them and print a usage message written
// by somebody else.
func check(args []string) int {
	root, repository := "", ""
	var layout RepoLayout
flags:
	for len(args) >= 2 {
		switch args[0] {
		case "--repo":
			root = args[1]
		case "--repository":
			repository = args[1]
		// Both relative to --repo, both defaulting to the file's own name there.
		// A repository that builds its image from a context below its root keeps
		// the Dockerfile in it, and until JC87 that repository could not run this
		// check at all.
		case "--dockerfile":
			layout.Dockerfile = args[1]
		case "--caddyfile":
			layout.Caddyfile = args[1]
		default:
			break flags // not a flag: the manifests start here
		}
		args = args[2:]
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "jcloud check [--repo <root> [--repository <owner/name>] [--dockerfile <path>] [--caddyfile <path>]] <japode.yml> …")
		return 2
	}
	if root == "" {
		// The three say the same thing about the same missing flag, so they are one
		// message: --repo is what makes this the repository check, and anything
		// qualifying it without one is a caller that meant to pass it. In a fixed
		// order, because a message that changes between runs of the same wrong
		// command reads as two different defects.
		for _, f := range []struct{ name, value string }{
			{"--repository", repository},
			{"--dockerfile", layout.Dockerfile},
			{"--caddyfile", layout.Caddyfile},
		} {
			if f.value != "" {
				fmt.Fprintf(os.Stderr, "jcloud check: %s qualifies the repository --repo is rooted at, and there is no --repo\n", f.name)
				return 2
			}
		}
		return run(args, nil, "states what it declares", "state what they declare")
	}
	return run(args, func(m *Manifest) ([]string, []string) { return m.AgreeWithRepo(root, repository, layout), nil },
		"agrees with the repository it sits in", "agree with the repositories they sit in")
}

// agree reads the adapted configuration, if one was named, and hands it on.
func agree(paths []string, root, adaptedPath, composePath string) int {
	var in AgreeInputs
	for _, f := range []struct {
		path string
		into *[]byte
	}{{adaptedPath, &in.Routes}, {composePath, &in.Compose}} {
		if f.path == "" {
			continue
		}
		data, err := os.ReadFile(f.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "jcloud agree: %v\n", err)
			return 2
		}
		*f.into = data
	}
	return run(paths, func(m *Manifest) ([]string, []string) { return m.AgreeReport(root, in) },
		"agrees with the files deployed beside it", "agree with the files deployed beside them")
}

// compareManifests reads two manifests and reports where they disagree.
//
// It does not validate either. Whether a manifest states what a service must
// declare is `check`'s question, and both copies already have a `check` of their
// own — ours in this repository's CI, theirs in the service's (JC27). Asking it a
// third time here would report one broken field in two repositories and leave the
// person fixing it wondering which one is authoritative.
func compareManifests(oursPath, theirsPath string) int {
	load := func(path string) *Manifest {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s: %v\n", path, err)
			return nil
		}
		m, err := Parse(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s: %v\n", path, err)
			return nil
		}
		return m
	}

	// Both are loaded before either failure returns, so a caller who broke the
	// YAML in both copies is told twice rather than sent round the loop.
	ours, theirs := load(oursPath), load(theirsPath)
	if ours == nil || theirs == nil {
		return 1
	}

	problems := ours.CompareTo(theirs, oursPath, theirsPath)
	if len(problems) == 0 {
		fmt.Printf("  \033[32m✓\033[0m %s and %s agree about %s (%d volume(s), %d route(s), %d variable(s))\n",
			oursPath, theirsPath, ours.Name, len(ours.Volumes), len(ours.Routes), len(ours.Env))
		fmt.Printf("\n\033[32mthe two copies of the manifest agree\033[0m\n")
		return 0
	}

	fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s and %s disagree\n", oursPath, theirsPath)
	for _, problem := range problems {
		fmt.Fprintf(os.Stderr, "      %s\n", problem)
		// Annotated against our copy: this runs in this repository, and the file
		// a reviewer here can actually change is this one.
		annotate("error", oursPath, problem)
	}
	fmt.Fprintf(os.Stderr, "\n\033[31mthe two copies of the manifest disagree\033[0m\n")
	return 1
}

// run validates each manifest and then, when compare is not nil, asks it the
// second question. One function for all three modes because that second question
// is only worth asking of a manifest that survived the first: a port that is not a
// port disagrees with everything, and reporting that four times over is noise
// standing where one answer belongs.
//
// compare answers problems, which refuse the manifest, and notes, which are said
// and refuse nothing: a comparison the platform's profile switched off (JCT6).
func run(paths []string, compare func(*Manifest) ([]string, []string), what, whatAll string) int {
	bad := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s: %v\n", path, err)
			bad++
			continue
		}

		m, err := Parse(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s: %v\n", path, err)
			bad++
			continue
		}

		problems := m.Validate()
		var notes []string
		if len(problems) == 0 && compare != nil {
			problems, notes = compare(m)
		}
		// Under the verdict, whichever it is, and on stdout with the ✓: a note is
		// not a failure, and a reader who sees only the errors loses nothing.
		say := func() {
			for _, note := range notes {
				fmt.Printf("      \033[33m!\033[0m %s\n", note)
				annotate("warning", path, note)
			}
		}
		if len(problems) == 0 {
			fmt.Printf("  \033[32m✓\033[0m %s: %s %s (%d volume(s), %d route(s), %d variable(s))\n",
				path, m.Name, what, len(m.Volumes), len(m.Routes), len(m.Env))
			say()
			continue
		}

		// The directory and not the file: every manifest is called japode.yml,
		// so the path is the only thing that says which service is wrong.
		fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s (%s)\n", path, filepath.Dir(path))
		for _, problem := range problems {
			fmt.Fprintf(os.Stderr, "      %s\n", problem)
			// A GitHub annotation puts the message on the diff, which is the
			// point of checking here rather than on the server. Inside Actions
			// only (annotate.go): elsewhere it was each problem twice.
			annotate("error", path, problem)
		}
		say()
		bad++
	}

	if bad == 0 {
		fmt.Printf("\n\033[32m%d manifest(s) %s\033[0m\n", len(paths), whatAll)
		return 0
	}
	fmt.Fprintf(os.Stderr, "\n\033[31m%d of %d manifest(s) refused\033[0m\n", bad, len(paths))
	return 1
}
