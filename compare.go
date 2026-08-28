// One manifest against the copy in the service's own repository.
//
// Every fact this repository shares with that one is in the manifest: the port,
// the health path, the volume names, the variables and the route bodies. So one
// comparison covers all of them, and a field added to the schema after this was
// written is covered without a line changing here — which is the whole of §JC29,
// and the reason it is not a second script shaped like the first.
//
// What it compares is meanings and not bytes. Both files are parsed and rendered
// again from the struct, so the two copies may order their keys differently,
// indent differently and carry entirely different comments. Prose is allowed to
// differ — the rule scripts/check-upstream-routes.sh already applies to the route,
// applied here to everything else.
package main

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Canon renders the manifest as the schema understands it: the struct's field
// order, no comments, and nothing invented that the file did not say. Two
// manifests that mean the same thing render to the same bytes.
func (m *Manifest) Canon() ([]byte, error) { return yaml.Marshal(m) }

// CompareTo reports where two manifests disagree, one problem per differing line
// and both sides shown.
//
// The common head and tail are trimmed and what is left is reported whole. This is
// not a general diff and does not need to be: the field order is the struct's and
// not the file's, so two manifests that disagree about a field disagree at the
// same place in both renderings. A list that gained an entry shifts what follows
// it, and reporting that region from both sides is more honest than guessing which
// entry is the new one.
//
// Lines are quoted rather than trimmed, because the indentation is what says which
// field a `path:` belongs to.
func (m *Manifest) CompareTo(other *Manifest, ours, theirs string) []string {
	a, err := m.Canon()
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", ours, err)}
	}
	b, err := other.Canon()
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", theirs, err)}
	}
	if bytes.Equal(a, b) {
		return nil
	}

	ourLines := lines(a)
	theirLines := lines(b)

	head := 0
	for head < len(ourLines) && head < len(theirLines) && ourLines[head] == theirLines[head] {
		head++
	}
	tail := 0
	for tail < len(ourLines)-head && tail < len(theirLines)-head &&
		ourLines[len(ourLines)-1-tail] == theirLines[len(theirLines)-1-tail] {
		tail++
	}

	var problems []string
	for _, line := range ourLines[head : len(ourLines)-tail] {
		problems = append(problems, fmt.Sprintf("%s has %q", ours, line))
	}
	for _, line := range theirLines[head : len(theirLines)-tail] {
		problems = append(problems, fmt.Sprintf("%s has %q", theirs, line))
	}
	return problems
}

func lines(b []byte) []string {
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}
