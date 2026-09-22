// The configuration this repository versions: `.env.age`, one line per key, the key
// in the clear and the value encrypted to the recipients the file itself names.
//
// Today the unit of edit is the whole configuration. `SERVICES_ENV` is write-only —
// GitHub hides a secret the moment it is saved — so the only readable copy is a file
// on somebody's workstation, changing one password means having every other one to
// paste beside it, and nothing compares that file to what is deployed (JC92).
//
// Keeping the keys readable is what buys all three back. `git log -p` names which key
// changed and when, without naming the value. scripts/check-env.sh reads this file
// against .env.example with the reader it already has — `^[A-Z][A-Z0-9_]*=` is the
// shape both files use — so coverage is refused at the pull request instead of at
// 03:20, by a check that decrypts nothing and needs no identity. And a value is never
// in the clear on disk at any point.
//
// The whole file could have been one age blob instead, which is what sops does with a
// `.env`. It encrypts the same bytes and loses exactly the two properties above: every
// change is one opaque hunk in `git log -p`, and any check about which keys exist has
// to hold the identity to ask.
//
// filippo.io/age and not sops as a library: sops brings three cloud SDKs into a module
// whose only other dependency is a YAML parser. What is used here is age's own two
// calls, on a file format this package owns.
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"filippo.io/age"
)

// EnvFileName is the default `--file`, and the name docs/JCLOUD.md specifies.
const EnvFileName = ".env.age"

// A key is upper case, digits and underscores, starting with a letter. Not this
// package's taste: it is exactly what scripts/check-env.sh's `sed` picks up, and a key
// outside it is one that file cannot see — present here, absent from every coverage
// report, and discovered when whatever reads it finds nothing. Refused at `set`, where
// the person who typed it is still there to fix it.
var envKeyRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// The directive that names who can open this file. A word and a value rather than a
// comment: a recipient is data `set` reads, and a `#` line is the first thing somebody
// tidying a file deletes.
const recipientPrefix = "recipient "

// EnvFile is the file as lines, not as a map. Every `set` rewrites the whole file, so
// what it does with the lines it was not asked about is the difference between a
// one-line diff and a diff nobody reads: comments, blank lines, key order and the
// recipient block all survive unchanged because they are carried, never regenerated.
type EnvFile struct {
	lines []envLine
}

// A line is either a value — `KEY=<base64>` — or anything else, kept verbatim.
type envLine struct {
	raw string // the whole line, for everything that is not a value
	key string // "" unless this is a value line
	b64 string // the encrypted value, base64, for a value line
}

// ParseEnvFile reads the format. Nothing here decrypts: the keys are readable without
// an identity, which is the property the whole design rests on, and a caller that only
// wants to know which keys exist must never need a private key to find out.
func ParseEnvFile(data []byte) *EnvFile {
	f := &EnvFile{}
	// A trailing newline ends the last line rather than starting an empty one.
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" && len(data) == 0 {
		return f
	}
	for _, raw := range strings.Split(text, "\n") {
		// `KEY=` before anything else, and only in that exact shape. A line that
		// merely contains an `=` — a comment, a directive — is not a value, and
		// treating it as one would rewrite it on the next `set`.
		if key, b64, ok := strings.Cut(raw, "="); ok && envKeyRE.MatchString(key) {
			f.lines = append(f.lines, envLine{key: key, b64: b64})
			continue
		}
		f.lines = append(f.lines, envLine{raw: raw})
	}
	return f
}

// Bytes renders the file back. A file that had content ends with a newline; an empty
// one stays empty rather than becoming a lone newline.
func (f *EnvFile) Bytes() []byte {
	if len(f.lines) == 0 {
		return nil
	}
	var b strings.Builder
	for _, l := range f.lines {
		if l.key != "" {
			fmt.Fprintf(&b, "%s=%s\n", l.key, l.b64)
			continue
		}
		b.WriteString(l.raw)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// Keys, in the order the file states them. The order is the file's own and not sorted:
// this is what `list` prints, and a listing that disagrees with the file it describes
// sends its reader looking for a line that is somewhere else.
func (f *EnvFile) Keys() []string {
	var keys []string
	for _, l := range f.lines {
		if l.key != "" {
			keys = append(keys, l.key)
		}
	}
	return keys
}

// Recipients as the file spells them, unparsed.
func (f *EnvFile) RecipientLines() []string {
	var out []string
	for _, l := range f.lines {
		if strings.HasPrefix(l.raw, recipientPrefix) {
			if v := strings.TrimSpace(strings.TrimPrefix(l.raw, recipientPrefix)); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// Recipients parsed, which is what `set` encrypts to.
//
// Every one of them, always. A file with two recipients whose values were encrypted to
// one is a configuration the second person cannot open, and the day that is discovered
// is the day the first laptop was lost — which is the whole reason the design asks for
// a second recipient in the password manager.
func (f *EnvFile) Recipients() ([]age.Recipient, error) {
	lines := f.RecipientLines()
	if len(lines) == 0 {
		return nil, fmt.Errorf("no `recipient` line — nothing could open what this wrote, so there is nobody to encrypt to")
	}
	var out []age.Recipient
	for _, s := range lines {
		r, err := age.ParseX25519Recipient(s)
		if err != nil {
			return nil, fmt.Errorf("recipient %s: %w", s, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// Get returns one key's ciphertext, still encrypted.
func (f *EnvFile) Get(key string) (string, bool) {
	for _, l := range f.lines {
		if l.key == key {
			return l.b64, true
		}
	}
	return "", false
}

// Set replaces a key's value where it is, and appends where it is new.
//
// Appended at the end and not in sorted position: this file is grouped by comment the
// way .env.example is, and inserting alphabetically would drop a key into whichever
// section happened to surround it. A new key arriving at the bottom is a line somebody
// moves once, deliberately, and the move is its own diff.
func (f *EnvFile) Set(key, b64 string) {
	for i := range f.lines {
		if f.lines[i].key == key {
			f.lines[i].b64 = b64
			return
		}
	}
	f.lines = append(f.lines, envLine{key: key, b64: b64})
}

// Unset removes a key's line. Reports whether there was one, so the caller can tell a
// key it removed from a key that was never there.
func (f *EnvFile) Unset(key string) bool {
	for i := range f.lines {
		if f.lines[i].key == key {
			f.lines = append(f.lines[:i], f.lines[i+1:]...)
			return true
		}
	}
	return false
}

// seal encrypts one value to every recipient and returns it base64'd onto one line.
//
// Base64 and not age's own armor: armor is a PEM-like block over several lines, and a
// value spanning lines is a value `git log -p` reports as a hunk and scripts/check-env.sh
// reads as several keys. One key, one line, is the format's only real constraint.
func seal(value []byte, recipients []age.Recipient) (string, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(value); err != nil {
		return "", err
	}
	// Closed explicitly and its error returned: age writes its authentication tag on
	// Close, so a deferred Close whose error goes nowhere yields a value that looks
	// written and cannot be opened.
	if err := w.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// open is seal's inverse, and the only place an identity is used.
func open(b64 string, identities []age.Identity) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("not base64 — a value edited by hand is one no identity can open: %w", err)
	}
	r, err := age.Decrypt(bytes.NewReader(raw), identities...)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// ValidateEnvValue refuses what `.env` cannot carry, at the moment somebody types it.
//
// A newline is the whole list. `docker compose --env-file` reads one `KEY=value` per
// line with no continuation, so a value carrying one renders a file whose second line
// is a key nothing declared — and the first thing that notices is the service reading
// a truncated password. Refused here rather than at `render`, which runs on the server
// at the one moment nobody is watching.
func ValidateEnvValue(value []byte) error {
	if i := bytes.IndexAny(value, "\r\n"); i >= 0 {
		return fmt.Errorf("the value carries a newline at byte %d, and a .env line cannot — docker compose would read the rest as another key", i)
	}
	return nil
}

// ValidateEnvKey refuses a key the coverage check could not see.
func ValidateEnvKey(key string) error {
	if !envKeyRE.MatchString(key) {
		return fmt.Errorf("%q is not upper case, digits and underscores starting with a letter — scripts/check-env.sh reads exactly that shape, so a key outside it is one nothing checks", key)
	}
	return nil
}

// Identities, from the flag or from the environment.
//
// Two sources because there are two callers and they are nothing alike. A person has a
// file the bootstrap planted; a runner has a secret, which arrives as an environment
// variable and must never be written to the disk of a machine several jobs share. The
// flag wins where both are given: it is the explicit one, and a caller who passed a
// path meant that path.
//
// JCLOUD_IDENTITY carries the identity itself and not a path to one, because a secret
// that has to become a file first is a secret with a temporary file to clean up.
func LoadIdentities(path string) ([]age.Identity, error) {
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		ids, err := age.ParseIdentities(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return ids, nil
	}
	if key := os.Getenv("JCLOUD_IDENTITY"); key != "" {
		ids, err := age.ParseIdentities(strings.NewReader(key))
		if err != nil {
			return nil, fmt.Errorf("JCLOUD_IDENTITY: %w", err)
		}
		return ids, nil
	}
	return nil, fmt.Errorf("no identity — pass --identity <file>, or set JCLOUD_IDENTITY to the key itself")
}

// WriteEnvFile replaces the file, and only after the new content is complete.
//
// Through a temporary file in the same directory: a `set` interrupted midway through a
// direct write leaves the configuration truncated, and the copy that was going to
// restore it is the one being written. 0600 on both, because the ciphertext is not
// secret but the file's neighbours on a workstation are not a reason to find out.
func WriteEnvFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".env.age.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once the rename below has happened
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
