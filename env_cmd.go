// `jcloud env` — the five things anybody does to the configuration, against the file
// tools/jcloud/env.go describes.
//
// It is in this tool and not a script beside the others because installing age or sops
// on a workstation is a step that gets forgotten, and the one forgotten on the day
// somebody has to rotate a password in a hurry. jcloud already runs inside the pinned
// golang image (scripts/go-toolchain.sh), so these commands cost nothing new to reach.
//
// A value arrives on stdin and leaves on stdout or into a named file. Never in argv:
// the command line of a running process is world-readable on the server, and on a
// workstation it is the shell history as well.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// envUsage is printed for `jcloud env` with no verb, and for one it does not have.
const envUsage = `jcloud env <verb> — the configuration in .env.age (docs/JCLOUD.md)

  jcloud env list                      the keys, and who can open them
  jcloud env get <KEY>                 one value, to stdout
  jcloud env set <KEY>                 one value, read from stdin
  jcloud env unset <KEY>               drop one key
  jcloud env render --out <path>       every key, as a .env

  --file <path>       the store (default: .env.age)
  --identity <path>   the private key, for get and render; or set JCLOUD_IDENTITY
                      to the key itself, which is how the runner passes a secret

list needs no identity, and that is deliberate: the keys are in the clear so that
scripts/check-env.sh can read them against .env.example without holding one.
`

// envCmd dispatches the verb and owns the flags, in the same hand-rolled shape as
// `check` next door: the flag package would reorder the key behind them and print a
// usage message written by somebody else.
//
// Exit codes are main.go's contract, unchanged: 0 it worked, 1 it did not, 2 the
// arguments were wrong.
func envCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, envUsage)
		return 2
	}
	verb, args := args[0], args[1:]

	path, identity, out := EnvFileName, "", ""
	var positional []string
	for len(args) > 0 {
		switch args[0] {
		case "--file", "--identity", "--out":
			if len(args) < 2 {
				fmt.Fprintf(os.Stderr, "jcloud env: %s takes a path\n", args[0])
				return 2
			}
			switch args[0] {
			case "--file":
				path = args[1]
			case "--identity":
				identity = args[1]
			case "--out":
				out = args[1]
			}
			args = args[2:]
		default:
			positional = append(positional, args[0])
			args = args[1:]
		}
	}

	key := ""
	switch verb {
	case "get", "set", "unset":
		if len(positional) != 1 {
			fmt.Fprintf(os.Stderr, "jcloud env %s <KEY>\n", verb)
			return 2
		}
		key = positional[0]
	default:
		if len(positional) != 0 {
			fmt.Fprintf(os.Stderr, "jcloud env %s takes no arguments, and got %q\n", verb, positional[0])
			return 2
		}
	}

	switch verb {
	case "list":
		return envList(path)
	case "get":
		return envGet(path, identity, key)
	case "set":
		return envSet(path, key)
	case "unset":
		return envUnset(path, key)
	case "render":
		return envRender(path, identity, out)
	default:
		fmt.Fprintf(os.Stderr, "jcloud env: no such verb %q\n\n", verb)
		fmt.Fprint(os.Stderr, envUsage)
		return 2
	}
}

// readEnvFile is the one place the store is read, so a missing file says the same
// thing to all five verbs — and says what to do about it.
func readEnvFile(path string) (*EnvFile, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "jcloud env: no %s — `jcloud env set <KEY>` writes one, once it names a recipient\n", path)
			return nil, 1
		}
		fmt.Fprintf(os.Stderr, "jcloud env: %v\n", err)
		return nil, 1
	}
	return ParseEnvFile(data), 0
}

// list prints the keys and who can open them, and no values. It holds no identity and
// cannot: this is the read scripts/check-env.sh makes on a runner that has none.
func envList(path string) int {
	f, code := readEnvFile(path)
	if f == nil {
		return code
	}
	keys := f.Keys()
	recipients := f.RecipientLines()
	for _, k := range keys {
		fmt.Printf("  %s\n", k)
	}
	fmt.Printf("\n%d key(s), readable by %d recipient(s):\n", len(keys), len(recipients))
	for _, r := range recipients {
		fmt.Printf("  %s\n", r)
	}
	return 0
}

func envGet(path, identity, key string) int {
	f, code := readEnvFile(path)
	if f == nil {
		return code
	}
	b64, ok := f.Get(key)
	if !ok {
		fmt.Fprintf(os.Stderr, "jcloud env get: %s is not in %s\n", key, path)
		return 1
	}
	ids, err := LoadIdentities(identity)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env get: %v\n", err)
		return 1
	}
	value, err := open(b64, ids)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env get: %s: %v\n", key, err)
		return 1
	}
	// A newline after the value, so the common `jcloud env get K` in a terminal reads
	// as a line. A caller capturing it in `$(…)` has that newline stripped for it.
	fmt.Printf("%s\n", value)
	return 0
}

// set reads the value from stdin and rewrites the file.
//
// One trailing newline is stripped, and only one: `printf '%s\n' secret | jcloud env
// set K` and `printf secret | …` should store the same bytes, and a password that
// genuinely ends in a newline is not a thing `.env` can carry anyway.
func envSet(path, key string) int {
	if err := ValidateEnvKey(key); err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env set: %v\n", err)
		return 2
	}
	f, code := readEnvFile(path)
	if f == nil {
		return code
	}
	recipients, err := f.Recipients()
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env set: %s: %v\n", path, err)
		return 1
	}
	value, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env set: reading the value from stdin: %v\n", err)
		return 1
	}
	value = bytes.TrimSuffix(value, []byte("\n"))
	value = bytes.TrimSuffix(value, []byte("\r"))
	if err := ValidateEnvValue(value); err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env set: %s: %v\n", key, err)
		return 1
	}
	b64, err := seal(value, recipients)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env set: %s: %v\n", key, err)
		return 1
	}
	_, existed := f.Get(key)
	f.Set(key, b64)
	if err := WriteEnvFile(path, f.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env set: %v\n", err)
		return 1
	}
	// Which of the two happened, because they are different acts: one is a rotation
	// and the other is a key arriving, and .env.example has to grow a line for the
	// second. Never the value, and never its length.
	if existed {
		fmt.Printf("%s: replaced in %s, encrypted to %d recipient(s)\n", key, path, len(recipients))
	} else {
		fmt.Printf("%s: added to %s, encrypted to %d recipient(s) — document it in .env.example\n", key, path, len(recipients))
	}
	return 0
}

func envUnset(path, key string) int {
	f, code := readEnvFile(path)
	if f == nil {
		return code
	}
	if !f.Unset(key) {
		fmt.Fprintf(os.Stderr, "jcloud env unset: %s is not in %s\n", key, path)
		return 1
	}
	if err := WriteEnvFile(path, f.Bytes()); err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env unset: %v\n", err)
		return 1
	}
	fmt.Printf("%s: dropped from %s\n", key, path)
	return 0
}

// render writes every key as a `.env`.
//
// `--out` is required, and `-` is the only way to stdout. Rendering is the one command
// that puts the whole configuration somewhere at once, and the default had to be the
// safe one: a person running it to see what is in there would otherwise fill their
// scrollback with every password, and a workflow step missing its redirect would put
// them in a log that outlives the run.
func envRender(path, identity, out string) int {
	if out == "" {
		fmt.Fprintln(os.Stderr, "jcloud env render: --out <path> is required, and --out - is stdout — this writes every value at once, so where they go is not a default")
		return 2
	}
	f, code := readEnvFile(path)
	if f == nil {
		return code
	}
	ids, err := LoadIdentities(identity)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env render: %v\n", err)
		return 1
	}

	// Every key is opened before anything is written. A partial .env is a
	// configuration the deploy would go on to use, and the key that failed is the one
	// nothing would report until whatever reads it comes up wrong.
	var b strings.Builder
	for _, key := range f.Keys() {
		b64, _ := f.Get(key)
		value, err := open(b64, ids)
		if err != nil {
			fmt.Fprintf(os.Stderr, "jcloud env render: %s: %v\n", key, err)
			return 1
		}
		if err := ValidateEnvValue(value); err != nil {
			fmt.Fprintf(os.Stderr, "jcloud env render: %s: %v\n", key, err)
			return 1
		}
		fmt.Fprintf(&b, "%s=%s\n", key, value)
	}

	if out == "-" {
		fmt.Print(b.String())
		return 0
	}
	// 0600: this one is the cleartext.
	if err := os.WriteFile(out, []byte(b.String()), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "jcloud env render: %v\n", err)
		return 1
	}
	// To stderr, so `--out -` stays a clean .env on stdout.
	fmt.Fprintf(os.Stderr, "%d key(s) written to %s\n", len(f.Keys()), out)
	return 0
}
