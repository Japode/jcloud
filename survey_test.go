package main

import (
	"strings"
	"testing"
)

// The agreeing case, so every test below is about one disagreement and not about a
// fixture that never agreed.
const observedAgreeing = `container skillgist healthy
pinned skillgist ghcr.io/japode/skillgist@sha256:aaaa
running skillgist ghcr.io/japode/skillgist@sha256:aaaa
container caddy healthy
`

func TestAServerThatMatchesReportsNothing(t *testing.T) {
	o := ParseObserved(observedAgreeing)
	// caddy is the edge and not a service: it is declared in the top-level compose
	// file, so services/*/ never names it and this must not call it a stranger.
	if d := SurveyAgainst(Declared{Services: []string{"skillgist"}, Others: []string{"caddy"}}, o); len(d) != 0 {
		t.Fatalf("a matching server reported %d divergence(s): %+v", len(d), d)
	}
}

// The state fespsp-iwl is actually in: declared in JC90 and never deployed. Until this
// existed the only thing that said so was a deploy failing for another service.
func TestADeclaredServiceThatIsNotRunningIsNamed(t *testing.T) {
	o := ParseObserved(observedAgreeing)
	d := SurveyAgainst(Declared{Services: []string{"skillgist", "fespsp-iwl"}, Others: []string{"caddy"}}, o)
	if len(d) != 1 {
		t.Fatalf("expected one divergence, got %+v", d)
	}
	if d[0].Service != "fespsp-iwl" || !strings.Contains(d[0].Says, "not running") {
		t.Fatalf("the divergence does not name the missing service: %+v", d[0])
	}
}

// The other direction, and the one a survey is for: something is up that nothing here
// declares. Every other check in this repository walks services/*/, so a container
// nobody declared is invisible to all of them.
func TestAContainerNothingDeclaresIsNamed(t *testing.T) {
	o := ParseObserved(observedAgreeing + "container mystery-thing healthy\n")
	d := SurveyAgainst(Declared{Services: []string{"skillgist"}, Others: []string{"caddy"}}, o)
	if len(d) != 1 {
		t.Fatalf("expected one divergence, got %+v", d)
	}
	if d[0].Service != "mystery-thing" || !strings.Contains(d[0].Says, "declared by nothing") {
		t.Fatalf("the divergence does not name the stranger: %+v", d[0])
	}
}

// A service up on a digest the deploy does not have pinned is a service the next
// deploy would silently change back.
func TestARunningDigestThatIsNotThePinnedOneIsNamed(t *testing.T) {
	o := ParseObserved(strings.Replace(observedAgreeing,
		"running skillgist ghcr.io/japode/skillgist@sha256:aaaa",
		"running skillgist ghcr.io/japode/skillgist@sha256:bbbb", 1))
	d := SurveyAgainst(Declared{Services: []string{"skillgist"}, Others: []string{"caddy"}}, o)
	if len(d) != 1 {
		t.Fatalf("expected one divergence, got %+v", d)
	}
	if !strings.Contains(d[0].Says, "pinned") {
		t.Fatalf("the divergence does not describe the digest drift: %+v", d[0])
	}
}

// Unhealthy is not the same as absent, and reporting it as absent would send whoever
// reads it to look for a container that is right there.
func TestAnUnhealthyServiceIsNotReportedAsMissing(t *testing.T) {
	o := ParseObserved(strings.Replace(observedAgreeing,
		"container skillgist healthy", "container skillgist unhealthy", 1))
	d := SurveyAgainst(Declared{Services: []string{"skillgist"}, Others: []string{"caddy"}}, o)
	if len(d) != 1 {
		t.Fatalf("expected one divergence, got %+v", d)
	}
	if strings.Contains(d[0].Says, "not running") {
		t.Fatalf("an unhealthy container was reported as absent: %+v", d[0])
	}
	if !strings.Contains(d[0].Says, "not healthy") {
		t.Fatalf("the divergence does not say what is wrong: %+v", d[0])
	}
}

// A digest the collector could not read is not a digest that matches. Reporting
// agreement from a half-read observation is the silence JC66 is about.
func TestAnUnobservedDigestIsNotAMatch(t *testing.T) {
	o := ParseObserved("container skillgist healthy\npinned skillgist ghcr.io/x@sha256:aaaa\n")
	d := SurveyAgainst(Declared{Services: []string{"skillgist"}}, o)
	for _, one := range d {
		if strings.Contains(one.Says, "pinned") {
			t.Fatalf("a digest comparison was made with only one side observed: %+v", one)
		}
	}
	if len(d) != 0 {
		t.Fatalf("an unobserved digest produced a divergence of its own: %+v", d)
	}
}

// Read against this repository's own committed manifests, because the false positive
// this guards is the one the first draft had: an addon container is declared by a
// `needs:` entry and by no directory under services/, so a survey that only globbed
// directories reported japode-fespsp-institucional-db — a database that has been
// serving for weeks — as something nobody declared. The edge is the same shape from
// the other side: it is in the top-level compose file and deliberately has no manifest.
func TestAddonsAndTheEdgeAreDeclaredAndNotStrangers(t *testing.T) {
	d, err := DeclaredIn("../..")
	if err != nil {
		t.Fatalf("reading this repository: %v", err)
	}
	if !contains(d.Others, "caddy") {
		t.Fatalf("the edge is not declared: %v", d.Others)
	}
	if !contains(d.Services, "fespsp-institucional") {
		t.Fatalf("a service directory is not declared: %v", d.Services)
	}
	if !contains(d.Others, "fespsp-institucional-db") {
		t.Fatalf("an addon from needs: is not declared, so it would be reported as a stranger: %v", d.Others)
	}
	// And the addon must not be in Services, or a digest would be compared for a
	// container the deploy pins nothing for.
	if contains(d.Services, "fespsp-institucional-db") {
		t.Fatalf("an addon is counted as a service, and the deploy pins no digest for it: %v", d.Services)
	}

	// The whole thing, against a server running exactly what is declared: silence.
	o := &Observed{Containers: map[string]string{}, Pinned: map[string]string{}, Running: map[string]string{}}
	for _, name := range append(append([]string{}, d.Services...), d.Others...) {
		o.Containers[name] = "healthy"
	}
	if got := SurveyAgainst(d, o); len(got) != 0 {
		t.Fatalf("a server running everything this repository declares reported: %+v", got)
	}
}

// The format is three fields a shell script prints and this reads. A line that is
// neither, and a kind this does not know, are ignored rather than refused: the
// collector reports machine facts this does not compare.
func TestTheObservationFormatIgnoresWhatItDoesNotKnow(t *testing.T) {
	o := ParseObserved("# a comment\n\ntimer japode-backup active\ncontainer skillgist healthy\nshort line\n")
	if len(o.Containers) != 1 || o.Containers["skillgist"] != "healthy" {
		t.Fatalf("the container line was not read among the others: %+v", o.Containers)
	}
}
