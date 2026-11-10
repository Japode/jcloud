package main

import (
	"reflect"
	"strings"
	"testing"
)

// JCT8: a declared prefix and a shared key. The Japode fixture, its keys spelled
// the way Viglet Cloud spells them: VIGLET_PASTE_* for its own, and one key
// several services read with no prefix at all.
func prefixed(t *testing.T) *Manifest {
	t.Helper()
	// The env list is the manifest's last block, so the shared variable is
	// appended into it rather than after env_prefix.
	body := strings.Replace(fixtureManifest, "    value: /data/paste.db\n",
		"    value: /data/paste.db\n  - name: OPENAI_API_KEY\n    shared: true\n    secret: true\n", 1) + "env_prefix: VIGLET_PASTE_\n"
	m, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Validate(); len(p) != 0 {
		t.Fatalf("the prefixed manifest is not valid:\n  %s", strings.Join(p, "\n  "))
	}
	return m
}

func TestTheDeclaredPrefixNamesEveryKey(t *testing.T) {
	m := prefixed(t)
	required, fixed := RequiredEnv(DefaultProfile(), m)
	if want := []string{"VIGLET_PASTE_SITE_URL", "OPENAI_API_KEY", "VIGLET_PASTE_HOSTS"}; !reflect.DeepEqual(required, want) {
		t.Errorf("required: got %v, want %v", required, want)
	}
	if want := []string{"VIGLET_PASTE_DATABASE_PATH"}; !reflect.DeepEqual(fixed, want) {
		t.Errorf("fixed: got %v, want %v", fixed, want)
	}

	// The template carries them under those names, and the old derived spelling
	// is no longer this service's.
	example := "VIGLET_PASTE_HOSTS=x\nVIGLET_PASTE_SITE_URL=y\nOPENAI_API_KEY=\nPASTE_SITE_URL=old\n"
	if p := m.agreeEnvExample(DefaultProfile(), nil, ".env.example", example); len(p) != 0 {
		t.Fatalf("got:\n  %s", strings.Join(p, "\n  "))
	}
	missing := strings.Join(m.agreeEnvExample(DefaultProfile(), nil, ".env.example", "VIGLET_PASTE_HOSTS=x\nVIGLET_PASTE_SITE_URL=y\n"), "\n")
	if !strings.Contains(missing, "no OPENAI_API_KEY=") {
		t.Fatalf("a shared key is still one the template has to carry, got:\n%s", missing)
	}
}

// Against the compose model the variables read the declared keys.
func TestTheDeclaredPrefixReachesCompose(t *testing.T) {
	m := prefixed(t)
	model, err := parseComposeModel([]byte(strings.NewReplacer(
		`"SITE_URL": "${PASTE_SITE_URL:-https://paste.example.com}"`,
		`"SITE_URL": "${VIGLET_PASTE_SITE_URL:-https://paste.example.com}", "OPENAI_API_KEY": "${OPENAI_API_KEY}"`,
	).Replace(fixtureModel)))
	if err != nil {
		t.Fatal(err)
	}
	if p := m.agreeComposeModel(model, nil, DefaultProfile()); len(p) != 0 {
		t.Fatalf("got:\n  %s", strings.Join(p, "\n  "))
	}
}

// One prefix can hold another. Neither service's keys are the other's orphans.
func TestANestedPrefixIsNotAnOrphan(t *testing.T) {
	turing := &Manifest{Name: "turing", EnvPrefixDecl: "VIGLET_TURING_", Env: []EnvVar{{Name: "URL"}}}
	demo := &Manifest{Name: "turing-demo", EnvPrefixDecl: "VIGLET_TURING_DEMO_", Env: []EnvVar{{Name: "URL"}}}
	body := "VIGLET_TURING_URL=a\nVIGLET_TURING_DEMO_URL=b\n"
	if p := turing.orphanEnvKeys(DefaultProfile(), []*Manifest{demo}, ".env.example", body); len(p) != 0 {
		t.Fatalf("a key under the longer prefix is the other service's, got:\n  %s", strings.Join(p, "\n  "))
	}
	if p := turing.orphanEnvKeys(DefaultProfile(), []*Manifest{demo}, ".env.example", body+"VIGLET_TURING_GONE=c\n"); len(p) != 1 {
		t.Fatalf("a key of its own nothing declares is still an orphan, got %v", p)
	}
}

// Every manifest declaring a shared key agrees on its default and its secret.
func TestSharedKeysAgreeAcrossManifests(t *testing.T) {
	m := prefixed(t)
	same := &Manifest{Name: "notes", Env: []EnvVar{{Name: "OPENAI_API_KEY", Shared: true, Secret: true}}}
	if p := m.agreeShared([]*Manifest{same}); len(p) != 0 {
		t.Fatalf("got:\n  %s", strings.Join(p, "\n  "))
	}
	def := "sk-none"
	other := &Manifest{Name: "notes", Env: []EnvVar{{Name: "OPENAI_API_KEY", Shared: true, Default: &def}}}
	p := strings.Join(m.agreeShared([]*Manifest{other}), "\n")
	for _, want := range []string{"its default is \"\" here and \"sk-none\" in notes", "secret is true here and false in notes"} {
		if !strings.Contains(p, want) {
			t.Errorf("want %q, got:\n%s", want, p)
		}
	}
}

// And the whole of agree finds the siblings itself, through the profile.
func TestAgreeReadsSharedKeysFromItsSiblings(t *testing.T) {
	m := prefixed(t)
	root := layTree(t, map[string]string{
		"services/paste/japode.yml": "unused: the manifest under test is parsed above\n",
		"services/notes/japode.yml": "version: 1\nname: notes\nimage: ghcr.io/x/notes\nport: 1\nenv:\n  - name: OPENAI_API_KEY\n    shared: true\n",
	})
	p := strings.Join(m.AgreeFrom(root, AgreeInputs{}), "\n")
	if !strings.Contains(p, "secret is true here and false in notes") {
		t.Fatalf("got:\n%s", p)
	}
}

func TestABadEnvPrefixIsRefused(t *testing.T) {
	for _, bad := range []string{"viglet_", "VIGLET", "_VIGLET_"} {
		m := &Manifest{EnvPrefixDecl: bad}
		if p := strings.Join(m.Validate(), "\n"); !strings.Contains(p, "env_prefix:") {
			t.Errorf("%q should be refused", bad)
		}
	}
}
