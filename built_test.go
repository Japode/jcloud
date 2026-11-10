package main

import (
	"strings"
	"testing"
)

// JCT7: a service the platform builds on its own host names its Dockerfile in
// place of an image.
var builtManifest = strings.Replace(fixtureManifest, "image: ghcr.io/x/paste\n", "build: apps/paste/Dockerfile\n", 1)

// The model as compose prints it on the machine that ran it: the context is
// absolute there, so the manifest's path is its suffix.
var builtModel = strings.Replace(fixtureModel,
	`"image": "${PASTE_IMAGE:?no image pinned}",`,
	`"build": {"context": "C:\\work\\platform\\apps\\paste", "dockerfile": "Dockerfile"}, "image": "platform-paste",`, 1)

func builtProfile(t *testing.T) *Profile {
	t.Helper()
	prof, err := ParseProfile([]byte("builds: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	return prof
}

func TestBuildIsTheAlternativeToImage(t *testing.T) {
	m, err := Parse([]byte(builtManifest))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Validate(); len(p) != 0 {
		t.Fatalf("a built manifest is valid, got:\n  %s", strings.Join(p, "\n  "))
	}
	both, _ := Parse([]byte(builtManifest + "image: ghcr.io/x/paste\n"))
	if p := strings.Join(both.Validate(), "\n"); !strings.Contains(p, "image: and build: are both declared") {
		t.Errorf("got:\n%s", p)
	}
	outside, _ := Parse([]byte(strings.Replace(builtManifest, "build: apps/paste/Dockerfile", "build: ../elsewhere/Dockerfile", 1)))
	if p := strings.Join(outside.Validate(), "\n"); !strings.Contains(p, "is not a path inside the platform tree") {
		t.Errorf("got:\n%s", p)
	}
}

// Against the model: built from the declared Dockerfile, and no image variable
// asked of it. A different Dockerfile is a disagreement.
func TestABuiltServiceAgreesWithTheModel(t *testing.T) {
	m, err := Parse([]byte(builtManifest))
	if err != nil {
		t.Fatal(err)
	}
	model, err := parseComposeModel([]byte(builtModel))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.agreeComposeModel(model, nil, builtProfile(t)); len(p) != 0 {
		t.Fatalf("got:\n  %s", strings.Join(p, "\n  "))
	}
	other, _ := parseComposeModel([]byte(strings.Replace(builtModel, `"dockerfile": "Dockerfile"`, `"dockerfile": "Dockerfile.dev"`, 1)))
	if p := strings.Join(m.agreeComposeModel(other, nil, builtProfile(t)), "\n"); !strings.Contains(p, "and not apps/paste/Dockerfile") {
		t.Fatalf("got:\n%s", p)
	}
}

// Japode's profile builds nothing, so build: is refused there by agree and by up.
func TestBuildIsRefusedWhereNothingIsBuilt(t *testing.T) {
	root := write(t, "", "", "")
	m, err := Parse([]byte(builtManifest))
	if err != nil {
		t.Fatal(err)
	}
	if p := strings.Join(m.AgreeWith(root, []byte(fixtureAdapted)), "\n"); !strings.Contains(p, "does not say builds: true") {
		t.Fatalf("got:\n%s", p)
	}
	r := Assess(DefaultProfile(), m, envKeys(everyRequiredKey(t, m)), "", nil)
	if r.Ready() || !strings.Contains(r.Refused, "builds nothing on its host") {
		t.Fatalf("got %+v", r)
	}
}

// up: a built service needs no pin, so it is ready with none.
func TestABuiltServiceIsReadyWithoutAPin(t *testing.T) {
	m, err := Parse([]byte(builtManifest))
	if err != nil {
		t.Fatal(err)
	}
	r := Assess(builtProfile(t), m, envKeys(everyRequiredKey(t, m)), "", nil)
	if !r.Ready() || r.Built != "apps/paste/Dockerfile" {
		t.Fatalf("got %+v", r)
	}
}

// up: a pin that is a tag is ready and named as moving, apart from a digest.
func TestAMovingTagIsNamed(t *testing.T) {
	m := parseValid(t)
	keys := envKeys(everyRequiredKey(t, m))
	tag := Assess(DefaultProfile(), m, keys, m.SCREAM()+"_IMAGE=ghcr.io/japode/skillgist:latest\n", nil)
	if !tag.Ready() || !tag.Moving() {
		t.Fatalf("a tag pin runs and is moving: %+v", tag)
	}
	digest := Assess(DefaultProfile(), m, keys, m.SCREAM()+"_IMAGE=ghcr.io/japode/skillgist@sha256:abc\n", nil)
	if digest.Moving() {
		t.Fatalf("a digest is not moving: %+v", digest)
	}
}

// survey: a built service's running image is a local build, and no pin is
// compared to it.
func TestSurveyComparesNoPinForABuiltService(t *testing.T) {
	d := Declared{Services: []string{"paste"}, Built: map[string]bool{"paste": true}}
	o := ParseObserved("container paste healthy\npinned paste sha256:aaa\nrunning paste sha256:bbb\n")
	if got := SurveyAgainst(d, o); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
