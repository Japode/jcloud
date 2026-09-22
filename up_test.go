package main

import (
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

	r := Assess(m, envKeys(""), "")
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
	r := Assess(m, envKeys(body), m.SCREAM()+"_IMAGE=ghcr.io/x/y@sha256:abc\n")
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

	r := Assess(m, envKeys(""), "")
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
	r := Assess(m, envKeys(everyRequiredKey(t, m)), "")
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
	r := Assess(m, envKeys(everyRequiredKey(t, m)), state)
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
