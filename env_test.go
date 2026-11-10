package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"filippo.io/age"
)

// A store with two recipients, comments and a key already in it — the shape the real
// file has, because the properties worth asserting are all about what survives a write
// that was not about them.
func newStore(t *testing.T) (path string, ids []age.Identity, second []age.Identity) {
	t.Helper()
	one, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generating an identity: %v", err)
	}
	two, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generating the second identity: %v", err)
	}

	f := ParseEnvFile([]byte("# the edge\n\nrecipient " + one.Recipient().String() + "\nrecipient " + two.Recipient().String() + "\n"))
	recipients, err := f.Recipients()
	if err != nil {
		t.Fatalf("the fixture names no recipient: %v", err)
	}
	b64, err := seal([]byte("first"), recipients)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	f.Set("ACME_EMAIL", b64)

	path = filepath.Join(t.TempDir(), EnvFileName)
	if err := WriteEnvFile(path, f.Bytes()); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return path, []age.Identity{one}, []age.Identity{two}
}

func load(t *testing.T, path string) *EnvFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return ParseEnvFile(data)
}

// The round trip, and the reason there is a second recipient at all: a value written
// once opens with either key. A lost laptop must not be a lost configuration, and the
// only way that holds is if `set` encrypts to every recipient the file names — which
// is a property of a write nobody re-reads until the bad day.
func TestAValueOpensWithEitherRecipientsKey(t *testing.T) {
	path, first, second := newStore(t)
	f := load(t, path)
	b64, ok := f.Get("ACME_EMAIL")
	if !ok {
		t.Fatal("ACME_EMAIL is not in the store this test just wrote")
	}
	for name, ids := range map[string][]age.Identity{"the first": first, "the second": second} {
		value, err := open(b64, ids)
		if err != nil {
			t.Fatalf("%s identity cannot open the value: %v", name, err)
		}
		if string(value) != "first" {
			t.Fatalf("%s identity opened %q, and %q was written", name, value, "first")
		}
	}
}

// The property the whole format exists for. scripts/check-env.sh reads this file with
// `sed -n 's/^\([A-Z][A-Z0-9_]*\)=.*/\1/p'` on a runner holding no private key, so the
// keys have to be legible to a reader that cannot decrypt a thing.
func TestTheKeysAreReadableWithoutAnIdentity(t *testing.T) {
	path, _, _ := newStore(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the store: %v", err)
	}
	if !strings.Contains(string(data), "\nACME_EMAIL=") {
		t.Fatalf("no readable ACME_EMAIL= line in:\n%s", data)
	}
	// And the value is not. The check that matters is the negative one: a format
	// that stored the key legibly and the value beside it would pass the line above.
	if strings.Contains(string(data), "first") {
		t.Fatalf("the cleartext value is in the file:\n%s", data)
	}
	if got := load(t, path).Keys(); len(got) != 1 || got[0] != "ACME_EMAIL" {
		t.Fatalf("Keys() is %v, and the file has one key", got)
	}
}

// What `set` does to the lines it was not asked about, which is what decides whether
// `git log -p` names the change or buries it. Comments, blank lines, the recipient
// block and the order of the other keys all have to come back byte for byte.
func TestSettingOneKeyLeavesEveryOtherLineAlone(t *testing.T) {
	path, ids, _ := newStore(t)
	f := load(t, path)
	recipients, err := f.Recipients()
	if err != nil {
		t.Fatalf("recipients: %v", err)
	}
	b64, err := seal([]byte("second"), recipients)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	f.Set("BACKUP_PASSWORD", b64)
	if err := WriteEnvFile(path, f.Bytes()); err != nil {
		t.Fatalf("writing: %v", err)
	}

	after := string(mustRead(t, path))
	for _, want := range []string{"# the edge\n", "\nrecipient age1", "\nACME_EMAIL="} {
		if !strings.Contains(after, want) {
			t.Fatalf("a write about BACKUP_PASSWORD lost %q:\n%s", want, after)
		}
	}
	// The new key is last, and the old one has not moved.
	keys := load(t, path).Keys()
	if len(keys) != 2 || keys[0] != "ACME_EMAIL" || keys[1] != "BACKUP_PASSWORD" {
		t.Fatalf("keys are %v, and a new key belongs at the end", keys)
	}
	// And the untouched value still opens, which a rewrite that re-encrypted
	// everything would also pass — so this asserts the bytes are the same too.
	got, _ := load(t, path).Get("ACME_EMAIL")
	if old, _ := f.Get("ACME_EMAIL"); got != old {
		t.Fatal("ACME_EMAIL's ciphertext was rewritten by a `set` about another key")
	}
	value, err := open(got, ids)
	if err != nil || string(value) != "first" {
		t.Fatalf("ACME_EMAIL no longer opens to what it held: %q, %v", value, err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}

// The coupling the two comments above assert in prose, asserted here against the file:
// the class scripts/check-env.sh's `sed` picks a key out with, compiled and run over a
// store this package wrote.
//
// Both sides were right and neither was checked. A `[A-Z][A-Z0-9_]*` narrowed over
// there — or a store that grew a prefix over here — would leave the coverage check
// reading a file it finds no keys in, and finding no keys is how that script reports a
// configuration that carries every one of them (JC92).
//
// The class is lifted from the script rather than written out again, which is the whole
// point: a copy here would agree with a comment and not with what runs.
func TestCheckEnvReadsTheKeysThisPackageWrites(t *testing.T) {
	japodePlatform(t, func(t *testing.T, root string) { checkEnvReads(t, root) })
}

func checkEnvReads(t *testing.T, root string) {
	script, err := os.ReadFile(filepath.Join(root, "scripts", "check-env.sh"))
	if err != nil {
		t.Fatalf("scripts/check-env.sh is the reader under test: %v", err)
	}
	// `sed -n 's/^\(<class>\)=.*/\1/p'`, and only that line: the script has one.
	lifted := regexp.MustCompile(`s/\^\\\((.+?)\\\)=`).FindSubmatch(script)
	if lifted == nil {
		t.Fatal("no `sed -n 's/^\\(…\\)=.*/\\1/p'` in scripts/check-env.sh — this test cannot see what it reads keys with")
	}
	// A POSIX bracket expression of this shape is the same text in Go's syntax, so
	// nothing is translated. A class that stopped being both would fail to compile
	// here, which is the honest outcome: it is no longer a class two readers share.
	reader, err := regexp.Compile(`(?m)^` + string(lifted[1]) + `=`)
	if err != nil {
		t.Fatalf("check-env.sh reads keys with %q, which is not a class this can compile: %v", lifted[1], err)
	}

	path, _, _ := newStore(t)
	store := mustRead(t, path)
	found := reader.FindAllString(string(store), -1)
	if len(found) != 1 || !strings.HasPrefix(found[0], "ACME_EMAIL") {
		t.Fatalf("check-env.sh's reader finds %v in a store holding exactly ACME_EMAIL:\n%s", found, store)
	}
	// And it reads no recipient line as a key. `recipient age1…` has no `=` at all
	// today; a format that gave it one would put an unchecked pseudo-key into every
	// coverage report.
	for _, line := range ParseEnvFile(store).RecipientLines() {
		if reader.MatchString(line) {
			t.Fatalf("check-env.sh's reader takes %q for a key", line)
		}
	}
}

// A key outside the shape scripts/check-env.sh reads is a key nothing checks. Refused
// where somebody can still fix it, rather than stored and never reported.
func TestAKeyTheCoverageCheckCannotSeeIsRefused(t *testing.T) {
	for _, key := range []string{"lowercase", "With-Dash", "9LEADING", "HAS SPACE", ""} {
		if err := ValidateEnvKey(key); err == nil {
			t.Fatalf("%q was accepted, and scripts/check-env.sh's sed does not read it", key)
		}
	}
	for _, key := range []string{"A", "ACME_EMAIL", "SKILLGIST_GITHUB_CLIENT_ID", "X9"} {
		if err := ValidateEnvKey(key); err != nil {
			t.Fatalf("%q was refused and is the shape .env.example uses: %v", key, err)
		}
	}
}

// A newline in a value renders a .env whose next line is a key nothing declared.
// Refused at `set`, because `render` runs on the server with nobody watching.
func TestAValueCarryingANewlineIsRefused(t *testing.T) {
	if err := ValidateEnvValue([]byte("two\nlines")); err == nil {
		t.Fatal("a value with a newline was accepted, and a .env line cannot carry one")
	}
	if err := ValidateEnvValue([]byte("carriage\rreturn")); err == nil {
		t.Fatal("a value with a carriage return was accepted")
	}
	if err := ValidateEnvValue([]byte("s3:s3.us-west-004.backblazeb2.com/japode-backup")); err != nil {
		t.Fatalf("an ordinary value was refused: %v", err)
	}
}

// The identity that cannot open the file is the whole point of encrypting it.
func TestAStrangersIdentityOpensNothing(t *testing.T) {
	path, _, _ := newStore(t)
	stranger, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	b64, _ := load(t, path).Get("ACME_EMAIL")
	if _, err := open(b64, []age.Identity{stranger}); err == nil {
		t.Fatal("a key that is not a recipient opened the value")
	}
}

// A value somebody edited by hand is one no identity can open, and the message has to
// say that rather than report a cryptographic failure nobody can act on.
func TestAHandEditedValueSaysSo(t *testing.T) {
	_, ids, _ := newStore(t)
	_, err := open("not base64 at all !!", ids)
	if err == nil {
		t.Fatal("a value that is not base64 was opened")
	}
	if !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("the message does not point at the edit: %v", err)
	}
}

// A store naming nobody is one whose writes could never be read back. Refused at
// `set`, and named as that rather than as an empty list.
func TestAStoreWithNoRecipientRefusesToBeWrittenTo(t *testing.T) {
	f := ParseEnvFile([]byte("# nobody\nACME_EMAIL=\n"))
	_, err := f.Recipients()
	if err == nil {
		t.Fatal("a store with no recipient accepted a write")
	}
	if !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("the message does not name what is missing: %v", err)
	}
}

// Unset takes one line and reports whether there was one, so a caller can tell a key
// it removed from a key that was never there.
func TestUnsetRemovesOnlyThatKey(t *testing.T) {
	path, _, _ := newStore(t)
	f := load(t, path)
	if f.Unset("NOT_THERE") {
		t.Fatal("unsetting a key the store does not have reported a removal")
	}
	if !f.Unset("ACME_EMAIL") {
		t.Fatal("unsetting a key the store has reported nothing removed")
	}
	if keys := f.Keys(); len(keys) != 0 {
		t.Fatalf("keys are %v after the only one was dropped", keys)
	}
	if len(f.RecipientLines()) != 2 {
		t.Fatalf("dropping a key dropped a recipient: %v", f.RecipientLines())
	}
}

// Parsing has to leave alone every line that is not a value, including one that
// happens to contain an `=`. A comment rewritten as a key is a comment gone.
func TestOnlyAKeyShapedLineIsAValue(t *testing.T) {
	in := "# ACME_EMAIL=admin@example.com is the old one\nrecipient age1abc\nlower=case\n  INDENTED=x\nACME_EMAIL=Zm9v\n"
	f := ParseEnvFile([]byte(in))
	if keys := f.Keys(); len(keys) != 1 || keys[0] != "ACME_EMAIL" {
		t.Fatalf("keys are %v, and only the last line is one", keys)
	}
	if got := string(f.Bytes()); got != in {
		t.Fatalf("a parse and a write changed the file:\n%s\nwant:\n%s", got, in)
	}
}
