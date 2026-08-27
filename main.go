// jcloud — the tool docs/JCLOUD.md specifies. This build implements one command.
//
//	jcloud check <japode.yml> [<japode.yml> …]
//
// `check` reads each manifest and refuses one that does not state what a service
// must declare. It does not render, resolve a digest, or reach the network: those
// are JC28, JC30 and JC29, and a check that needed any of them could not run on a
// fork's pull request, which is where the interesting mistakes arrive.
//
// Exit codes are the whole contract, because a CI step reads nothing else: 0 every
// manifest is valid, 1 at least one is not, 2 the arguments were wrong.
package main

import (
	"fmt"
	"os"
	"path/filepath"
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
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "jcloud check: name at least one japode.yml")
			os.Exit(2)
		}
		os.Exit(check(args[1:]))
	case "version", "--version":
		fmt.Printf("jcloud %s (schema version %d)\n", Version, SchemaVersion)
	default:
		fmt.Fprintf(os.Stderr, "jcloud: no such command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `jcloud %s — what a service declares (docs/JCLOUD.md)

  jcloud check <japode.yml> …   refuse a manifest that does not state what a
                               service must declare; every problem at once
  jcloud version               this build, and the schema version it accepts

render, init, adopt and up are specified in docs/JCLOUD.md and not implemented
here — they are JC28, JC26's own scaffold, JC30 and JC30.
`, Version)
}

func check(paths []string) int {
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
		if len(problems) == 0 {
			fmt.Printf("  \033[32m✓\033[0m %s: %s, %d volume(s), %d route(s), %d variable(s)\n",
				path, m.Name, len(m.Volumes), len(m.Routes), len(m.Env))
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
		fmt.Printf("\n\033[32m%d manifest(s) state what they declare\033[0m\n", len(paths))
		return 0
	}
	fmt.Fprintf(os.Stderr, "\n\033[31m%d of %d manifest(s) refused\033[0m\n", bad, len(paths))
	return 1
}
