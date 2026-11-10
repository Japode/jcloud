package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseValid(t *testing.T) *Manifest {
	t.Helper()
	m, err := Parse([]byte(valid))
	if err != nil {
		t.Fatalf("the fixture does not parse: %v", err)
	}
	return m
}

// The rule this command exists to apply, and it is agree.go's: a variable the
// manifest declares has to be in the configuration, a route's hosts variable with it,
// and a variable with a fixed `value:` is not configuration at all.
func TestRequiredEnvIsWhatTheSecretHasToCarry(t *testing.T) {
	m := parseValid(t)
	required, _ := RequiredEnv(m)
	for _, want := range []string{m.SCREAM() + "_HOSTS"} {
		if !contains(required, want) {
			t.Fatalf("%s is not required, and a route reads its hosts from it: %v", want, required)
		}
	}
	for _, key := range required {
		if !strings.HasPrefix(key, m.SCREAM()+"_") {
			t.Fatalf("%s is not prefixed with this service's name, so it could collide with another's", key)
		}
	}
}

// A fixed value reaching neither compose nor .env is the one agree.go refuses in the
// template. Here it must not be reported missing, or every service that fixes a value
// would block its own deploy forever.
func TestAFixedValueIsNotMissing(t *testing.T) {
	fixed := "x"
	m := parseValid(t)
	m.Env = append(m.Env, EnvVar{Name: "PINNED_THING", Value: &fixed})

	required, fixedKeys := RequiredEnv(m)
	key := m.SCREAM() + "_PINNED_THING"
	if contains(required, key) {
		t.Fatalf("%s is required and the manifest fixes its value", key)
	}
	if !contains(fixedKeys, key) {
		t.Fatalf("%s is not reported as fixed: %v", key, fixedKeys)
	}

	r := Assess(m, envKeys(""), "", nil)
	if contains(r.Missing, key) {
		t.Fatalf("%s was reported missing, and it is not configuration: %v", key, r.Missing)
	}
}

// Setting one anyway is worth saying and is not a refusal — the deploy ignores it, so
// what it costs is somebody believing they configured something.
func TestAFixedValueSetAnywayIsReportedAndDoesNotBlock(t *testing.T) {
	fixed := "x"
	m := parseValid(t)
	m.Env = append(m.Env, EnvVar{Name: "PINNED_THING", Value: &fixed})

	body := everyRequiredKey(t, m) + m.SCREAM() + "_PINNED_THING=set-by-hand\n"
	r := Assess(m, envKeys(body), m.SCREAM()+"_IMAGE=ghcr.io/x/y@sha256:abc\n", nil)
	if !contains(r.Fixed, m.SCREAM()+"_PINNED_THING") {
		t.Fatalf("a fixed value set by hand was not reported: %+v", r)
	}
	if !r.Ready() {
		t.Fatalf("a fixed value set by hand blocked the deploy: %+v", r)
	}
}

// The measurement JC97 is: a service declared without its variables. Every one of them
// has to be named, because a person fixing them one run at a time is the loop the whole
// command exists to close.
func TestEveryMissingKeyIsNamedAtOnce(t *testing.T) {
	m := parseValid(t)
	required, _ := RequiredEnv(m)
	if len(required) < 2 {
		t.Fatalf("the fixture declares %d key(s), which is too few to test this", len(required))
	}

	r := Assess(m, envKeys(""), "", nil)
	if len(r.Missing) != len(required) {
		t.Fatalf("%d of %d missing key(s) reported: %v", len(r.Missing), len(required), r.Missing)
	}
	if r.Ready() {
		t.Fatal("a service with no configuration at all was reported ready")
	}
}

// A service whose CI has never published has nothing to bring up, and that is a
// different sentence from a missing variable. JC8 is why it is not looked up here.
func TestAServiceWithNoPinnedDigestIsNotReady(t *testing.T) {
	m := parseValid(t)
	r := Assess(m, envKeys(everyRequiredKey(t, m)), "", nil)
	if len(r.Missing) != 0 {
		t.Fatalf("keys reported missing when all are set: %v", r.Missing)
	}
	if r.Ready() {
		t.Fatal("a service with no pinned digest was reported ready")
	}
	if r.Pinned != "" {
		t.Fatalf("a digest came from somewhere: %q", r.Pinned)
	}
}

// Fully configured and pinned is the only shape that reports ready.
func TestAConfiguredAndPinnedServiceIsReady(t *testing.T) {
	m := parseValid(t)
	state := m.SCREAM() + "_IMAGE=ghcr.io/japode/x@sha256:abcdef0123456789\n"
	r := Assess(m, envKeys(everyRequiredKey(t, m)), state, nil)
	if !r.Ready() {
		t.Fatalf("a configured, pinned service is not ready: %+v", r)
	}
}

// One service's state file holds every service's digest, so the prefix has to be exact.
// `SKILL_IMAGE=` must not answer for `SKILLGIST_IMAGE=`.
func TestOneServicesDigestIsNotAnothers(t *testing.T) {
	m := parseValid(t)
	other := "SOMETHING_ELSE_IMAGE=ghcr.io/japode/other@sha256:9999\n"
	if got := pinnedDigest(m, other); got != "" {
		t.Fatalf("another service's digest was read as this one's: %q", got)
	}
	both := other + m.SCREAM() + "_IMAGE=ghcr.io/japode/mine@sha256:1111\n"
	if got := pinnedDigest(m, both); !strings.Contains(got, "mine") {
		t.Fatalf("this service's digest was not found among others: %q", got)
	}
}

// The keys are read with the shape scripts/check-env.sh reads, so that a key this sees
// and a key the deploy's own check sees are one key. A commented-out line is not one.
func TestOnlyARealKeyLineCounts(t *testing.T) {
	keys := envKeys("# SKILLGIST_HOSTS=commented.example.com\n  INDENTED=x\nlower=case\nREAL_KEY=value\n")
	if keys["SKILLGIST_HOSTS"] {
		t.Fatal("a commented-out key was read as set")
	}
	if keys["INDENTED"] || keys["lower"] {
		t.Fatalf("an indented or lower-case line was read as a key: %v", keys)
	}
	if !keys["REAL_KEY"] {
		t.Fatal("a plain key was not read")
	}
}

// The record is three fields a shell script prints and this reads, so the two halves
// cannot come to disagree about its shape. A kind this build does not know is ignored
// rather than refused: a collector that learns to report more must not stop this one
// reading what it already understood.
func TestThePublishedRecordIsThreeFields(t *testing.T) {
	p := ParsePublished(`# gathered 2026-09-23
published skillgist ghcr.io/japode/skillgist@sha256:0261ea
tag skillgist latest
tag skillgist sha-53b7948
unpublished fespsp-iwl never
gathered at 2026-09-23T12:00:00Z
published
`)
	if got := p.Ref["skillgist"]; got != "ghcr.io/japode/skillgist@sha256:0261ea" {
		t.Fatalf("the published reference did not survive the round trip: %q", got)
	}
	if got := p.Tags["skillgist"]; len(got) != 2 || got[0] != "latest" {
		t.Fatalf("the tags were not read: %v", got)
	}
	if p.None["fespsp-iwl"] != "never" {
		t.Fatalf("a service with nothing published was not recorded: %v", p.None)
	}
	if len(p.Ref) != 1 || len(p.None) != 1 {
		t.Fatalf("an unknown kind or a short line was read as a fact: %+v", p)
	}
	// The stamp is the one line that is about the file rather than about a service.
	// Without it a record the last deploy left behind reads exactly like one gathered
	// a minute ago, which is the whole difficulty with keeping it on the machine.
	if p.When != "2026-09-23T12:00:00Z" {
		t.Fatalf("the record's date was not read: %q", p.When)
	}
	if ParsePublished("published x ghcr.io/a/b@sha256:1\n").When != "" {
		t.Fatal("an undated record claimed a date")
	}
}

// The half of JC30 this is: the digest arrives from the party that published it, with
// the tags of the version it came from, and nothing here looked anything up.
func TestAPublishedDigestIsResolvedAndCarriesItsTags(t *testing.T) {
	m := parseValid(t)
	ref := "ghcr.io/japode/" + m.Name + "@sha256:0261ea9f9358"
	p := ParsePublished("published " + m.Name + " " + ref + "\ntag " + m.Name + " latest\n")

	r := Assess(m, envKeys(everyRequiredKey(t, m)), "", p)
	if r.Published != ref {
		t.Fatalf("the published digest was not resolved: %+v", r)
	}
	if !contains(r.Tags, "latest") {
		t.Fatalf("the tags of the resolved version were lost: %v", r.Tags)
	}
	if r.Unpublished != "" {
		t.Fatalf("a service with a published digest was given a reason for having none: %q", r.Unpublished)
	}
}

// What the record must not do: turn a service the deploy would refuse into one it
// reports as ready. `deploy.sh` dies on any service with nothing in images.env before
// it brings any of them up, so an image that exists and was never deployed still stops
// every other service's deploy — which is the sentence JC97 measured.
func TestAPublishedDigestIsNotAPinnedOne(t *testing.T) {
	m := parseValid(t)
	p := ParsePublished("published " + m.Name + " ghcr.io/japode/x@sha256:0261ea\n")

	r := Assess(m, envKeys(everyRequiredKey(t, m)), "", p)
	if r.Ready() {
		t.Fatal("a service with nothing pinned was reported ready because its CI had published")
	}
	if r.Behind() {
		t.Fatal("a service that has never been deployed was reported as behind, which reads as a deploy that can be skipped")
	}
}

// Nobody asked and nothing was published are two answers, and the thing to do about
// each is different — pass a token, or go and look at the service's CI.
func TestNobodyAskedIsNotNothingPublished(t *testing.T) {
	m := parseValid(t)

	unasked := Assess(m, envKeys(everyRequiredKey(t, m)), "", nil)
	if unasked.Asked || unasked.Unpublished != "" {
		t.Fatalf("a run with no record claimed to know what was published: %+v", unasked)
	}

	asked := Assess(m, envKeys(everyRequiredKey(t, m)), "", ParsePublished("unpublished "+m.Name+" never\n"))
	if !asked.Asked || asked.Unpublished == "" {
		t.Fatalf("a service the record says published nothing has no reason to show: %+v", asked)
	}
	// A service the record does not mention at all is a record older than the service,
	// and saying "its CI published nothing" about it would send somebody to the wrong
	// repository.
	absent := Assess(m, envKeys(everyRequiredKey(t, m)), "", ParsePublished("published other ghcr.io/japode/other@sha256:1\n"))
	if absent.Unpublished == asked.Unpublished {
		t.Fatalf("a service missing from the record reads as one that published nothing: %q", absent.Unpublished)
	}
}

// Behind is about the digest. The name beside it has two lawful spellings — GitHub
// expands `${{ github.repository }}` with capitals and the registry lowercases them
// (JC47, JC48) — and comparing the text around the digest would announce a deploy
// waiting every time those two met.
func TestBehindComparesDigestsAndNotSpellings(t *testing.T) {
	m := parseValid(t)
	keys := envKeys(everyRequiredKey(t, m))
	same := "sha256:0261ea9f935886de"

	cased := Assess(m, keys, m.SCREAM()+"_IMAGE=ghcr.io/japode/x@"+same+"\n",
		ParsePublished("published "+m.Name+" ghcr.io/Japode/x@"+same+"\n"))
	if cased.Behind() {
		t.Fatalf("one digest written under two spellings of the name reported a deploy waiting: %+v", cased)
	}

	moved := Assess(m, keys, m.SCREAM()+"_IMAGE=ghcr.io/japode/x@"+same+"\n",
		ParsePublished("published "+m.Name+" ghcr.io/japode/x@sha256:3e4b478042d75a35\n"))
	if !moved.Behind() {
		t.Fatalf("a newer published digest was not reported: %+v", moved)
	}
}

// The command handed to a person is the deploy's own two variables. Asserted against
// scripts/deploy.sh rather than restated here: this is the one place a rename over
// there would otherwise print a command that runs and quietly deploys nothing new.
func TestTheDeployCommandNamesTheVariablesTheDeployReads(t *testing.T) {
	deploy, err := os.ReadFile(filepath.Join(platformRoot(t), "scripts", "deploy.sh"))
	if err != nil {
		t.Fatalf("scripts/deploy.sh is what this command is for: %v", err)
	}
	got := deployCommand("skillgist", "ghcr.io/japode/skillgist@sha256:0261ea")
	for _, name := range []string{"DEPLOY_SERVICE", "DEPLOY_IMAGE"} {
		if !strings.Contains(got, name+"=") {
			t.Fatalf("%s is not set by the command handed to a person: %s", name, got)
		}
		if !strings.Contains(string(deploy), "${"+name+":-}") {
			t.Fatalf("scripts/deploy.sh does not read %s, so this command sets a variable nothing uses", name)
		}
	}
	if !strings.HasSuffix(got, " skillgist") {
		t.Fatalf("the command does not name the service it deploys, so it would re-apply every one: %s", got)
	}
}

// everyRequiredKey renders a configuration that satisfies the manifest, so a test
// about one missing thing is not quietly about several.
func everyRequiredKey(t *testing.T, m *Manifest) string {
	t.Helper()
	required, _ := RequiredEnv(m)
	var b strings.Builder
	for _, key := range required {
		b.WriteString(key + "=value\n")
	}
	return b.String()
}
