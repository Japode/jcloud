// jcloud — the tool docs/JCLOUD.md specifies. This build implements three commands.
//
//	jcloud check [--repo <root> [--repository <owner/name>]] <japode.yml> …
//	jcloud agree [--adapted <caddy-adapt.json>] <root> <japode.yml> …
//	jcloud compare <ours.yml> <theirs.yml>
//
// `check` reads each manifest and refuses one that does not state what a service
// must declare. With `--repo` it goes on to compare the manifest against the
// service's own repository, which is the check that runs *there* (JC27). `agree`
// is the other direction: the manifest against the four files this repository
// deploys beside it. `compare` is the third: this repository's copy of a manifest
// against the service's own, which is every shared fact in one question.
//
// None of them renders, resolves a digest, or reaches the network. That is JC30's,
// and it is why `compare` takes two paths rather than a repository: a check that
// needed the network could not run on a fork's pull request, which is where the
// interesting mistakes arrive. The fetching lives in the script that holds the
// token, scripts/check-upstream-manifest.sh.
//
// Exit codes are the whole contract, because a CI step reads nothing else: 0 every
// manifest is valid, 1 at least one is not, 2 the arguments were wrong.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Version is the tool's own, distinct from the schema's. Pinning it and asserting
// it against what generated files claim is JC31; it is printed here so that a
// report about a manifest says which build read it.
const Version = "0.1.0"

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
		adapted := ""
		if len(rest) >= 2 && rest[0] == "--adapted" {
			// A file and not a command to run: this build shells out to nothing,
			// and `caddy adapt` belongs to the script that already has a pinned
			// caddy image. See tools/jcloud/route_json.go for why it is read at all.
			adapted, rest = rest[1], rest[2:]
		}
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "jcloud agree [--adapted <caddy-adapt.json>] <repository-root> <japode.yml> …")
			os.Exit(2)
		}
		os.Exit(agree(rest[1:], rest[0], adapted))
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
	case "version", "--version":
		// The compiler is printed beside the tool's own version because it is the
		// other half of what determines this binary's behaviour, and a report about
		// a manifest is only traceable if both are known (JC31). runtime.Version()
		// is what actually built this, not what go.mod asked for.
		fmt.Printf("jcloud %s (schema version %d, built with %s)\n", Version, SchemaVersion, runtime.Version())
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
       <japode.yml> …                  the service's own repository
  jcloud agree <root> <japode.yml> …   that, and then compare it against the
                                       compose file, the route, backup.conf and
                                       .env.example deployed beside it
  jcloud compare <ours> <theirs>       report where this repository's copy of a
                                       manifest and the service's own disagree
  jcloud version                       this build, and the schema version

Every problem is reported at once: a manifest is written in one sitting, and a
caller who fixes one field and runs again has been sent round a loop this could
have answered the first time.

init, adopt and up are specified in docs/JCLOUD.md and not implemented.
`, Version)
}

// check parses its own flags rather than reaching for the flag package, which
// would reorder the manifest paths behind them and print a usage message written
// by somebody else.
func check(args []string) int {
	root, repository := "", ""
	for len(args) >= 2 && (args[0] == "--repo" || args[0] == "--repository") {
		if args[0] == "--repo" {
			root = args[1]
		} else {
			repository = args[1]
		}
		args = args[2:]
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "jcloud check [--repo <root> [--repository <owner/name>]] <japode.yml> …")
		return 2
	}
	if root == "" {
		if repository != "" {
			fmt.Fprintln(os.Stderr, "jcloud check: --repository names the repository --repo is rooted at, and there is no --repo")
			return 2
		}
		return run(args, nil, "states what it declares", "state what they declare")
	}
	return run(args, func(m *Manifest) []string { return m.AgreeWithRepo(root, repository) },
		"agrees with the repository it sits in", "agree with the repositories they sit in")
}

// agree reads the adapted configuration, if one was named, and hands it on.
func agree(paths []string, root, adaptedPath string) int {
	var adaptedConfig []byte
	if adaptedPath != "" {
		var err error
		if adaptedConfig, err = os.ReadFile(adaptedPath); err != nil {
			fmt.Fprintf(os.Stderr, "jcloud agree: %v\n", err)
			return 2
		}
	}
	return run(paths, func(m *Manifest) []string { return m.AgreeWith(root, adaptedConfig) },
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
		fmt.Printf("::error file=%s::%s\n", oursPath, problem)
	}
	fmt.Fprintf(os.Stderr, "\n\033[31mthe two copies of the manifest disagree\033[0m\n")
	return 1
}

// run validates each manifest and then, when compare is not nil, asks it the
// second question. One function for all three modes because that second question
// is only worth asking of a manifest that survived the first: a port that is not a
// port disagrees with everything, and reporting that four times over is noise
// standing where one answer belongs.
func run(paths []string, compare func(*Manifest) []string, what, whatAll string) int {
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
		if len(problems) == 0 && compare != nil {
			problems = compare(m)
		}
		if len(problems) == 0 {
			fmt.Printf("  \033[32m✓\033[0m %s: %s %s (%d volume(s), %d route(s), %d variable(s))\n",
				path, m.Name, what, len(m.Volumes), len(m.Routes), len(m.Env))
			continue
		}

		// The directory and not the file: every manifest is called japode.yml,
		// so the path is the only thing that says which service is wrong.
		fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s (%s)\n", path, filepath.Dir(path))
		for _, problem := range problems {
			fmt.Fprintf(os.Stderr, "      %s\n", problem)
			// A GitHub annotation puts the message on the diff, which is the
			// point of checking here rather than on the server. It degrades to
			// an ordinary line outside Actions.
			fmt.Printf("::error file=%s::%s\n", path, problem)
		}
		bad++
	}

	if bad == 0 {
		fmt.Printf("\n\033[32m%d manifest(s) %s\033[0m\n", len(paths), whatAll)
		return 0
	}
	fmt.Fprintf(os.Stderr, "\n\033[31m%d of %d manifest(s) refused\033[0m\n", bad, len(paths))
	return 1
}
