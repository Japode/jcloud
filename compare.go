// One manifest against the copy in the service's own repository.
//
// Every fact this repository shares with that one is in the manifest: the port,
// the health path, the volume names, the variables and the route bodies. So one
// comparison covers all of them, and a field added to the schema after this was
// written is covered without a line changing here — which is the whole of JC29,
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
//
// Prose is dropped first, which is `doc:` and only `doc:`. It is a sentence for
// whoever opens the file — nothing is rendered from it and `.Doc` is read nowhere
// else in this tool — so it is a comment that happens to be a schema field, and
// the rule above covers it: the copy in a pt-BR repository documents its variables
// in Portuguese, and that is not drift.
//
// Dropped here rather than skipped in CompareTo, so the comparison stays one of
// whole renderings and a field added to the schema is still compared without a
// line changing (TestEveryFieldOfTheSchemaIsCompared).
func (m *Manifest) Canon() ([]byte, error) {
	c := *m
	if c.Env != nil {
		c.Env = append([]EnvVar(nil), m.Env...)
		for i := range c.Env {
			c.Env[i].Doc = ""
		}
	}
	return yaml.Marshal(&c)
}

// CompareTo reports where two manifests disagree, one problem per differing line,
// each side's lines shown and each named by the field it sits under.
//
// A line-level longest common subsequence over the two canonical renderings
// (JCT16). Trimming only the common head and tail, which this did first, reported
// every line between two distant differences: a port and one new variable printed
// some 60 lines, `health:` and `volumes:` included, none of which differed. The
// field order is the struct's and not the file's, so the shared lines between two
// differences are the same lines in both renderings, and the subsequence resyncs on
// them — which keeps the report to the lines that differ, and keeps it exact.
//
// Lines are quoted, and named by field (`env[2]`, `port`, `health.path`) rather
// than by indentation, which is what said which field a `path:` belonged to before.
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

	ourLines, theirLines := lines(a), lines(b)
	ourFields, theirFields := fieldsOf(ourLines), fieldsOf(theirLines)

	// A hunk is a run of lines that are not in the common subsequence, from either
	// side. Both sides of one hunk are reported together, ours first.
	var problems []string
	var hunkOurs, hunkTheirs []string
	flush := func() {
		problems = append(problems, hunkOurs...)
		problems = append(problems, hunkTheirs...)
		hunkOurs, hunkTheirs = nil, nil
	}
	for _, op := range diffLines(ourLines, theirLines) {
		switch op.kind {
		case '=':
			flush()
		case '-':
			hunkOurs = append(hunkOurs, fmt.Sprintf("%s: %s has %q", ourFields[op.i], ours, strings.TrimSpace(ourLines[op.i])))
		case '+':
			hunkTheirs = append(hunkTheirs, fmt.Sprintf("%s: %s has %q", theirFields[op.j], theirs, strings.TrimSpace(theirLines[op.j])))
		}
	}
	flush()
	return problems
}

func lines(b []byte) []string {
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

type diffOp struct {
	kind byte // '=' both, '-' ours only, '+' theirs only
	i, j int  // the line in ours, in theirs
}

// diffLines is the edit script of a longest common subsequence. Quadratic, which
// is nothing at the size of a manifest's rendering.
func diffLines(a, b []string) []diffOp {
	// lcs[i][j] is the length of the common subsequence of a[i:] and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{'=', i, j})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', i, j})
			i++
		default:
			ops = append(ops, diffOp{'+', i, j})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, diffOp{'-', i, j})
	}
	for ; j < len(b); j++ {
		ops = append(ops, diffOp{'+', i, j})
	}
	return ops
}

// fieldsOf names the field each line of a canonical rendering sits under: the
// top-level key, then the list index when that key holds a list (`env[2]`), or
// the second-level key when it holds a mapping (`health.path`). Deeper than that
// the line itself, quoted beside the name, says the rest.
func fieldsOf(rendered []string) []string {
	out := make([]string, len(rendered))
	top, child := "", ""
	index, itemIndent := -1, -1
	childIndent := -1
	for n, line := range rendered {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		key, _, _ := strings.Cut(strings.TrimPrefix(trimmed, "- "), ":")
		switch {
		case indent == 0:
			top, child = key, ""
			index, itemIndent, childIndent = -1, -1, -1
			out[n] = top
			continue
		case childIndent == -1 && itemIndent == -1:
			// The first line under a top-level key says whether it holds a list
			// or a mapping.
			if strings.HasPrefix(trimmed, "- ") {
				itemIndent = indent
			} else {
				childIndent = indent
			}
		}
		switch {
		case itemIndent >= 0:
			if indent == itemIndent && strings.HasPrefix(trimmed, "- ") {
				index++
			}
			out[n] = fmt.Sprintf("%s[%d]", top, index)
		default:
			if indent == childIndent {
				child = key
			}
			out[n] = top + "." + child
		}
	}
	return out
}
