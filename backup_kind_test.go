package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// JCT6: a platform whose backup tooling reads no per-service file says
// `backup: none`. The Japode fixture declares a sqlite file and an exclude, so
// under the default it needs a backup.conf; under none it needs nothing, the
// report says the comparison did not run, and the sqlite file is named.
func TestBackupNoneComparesNothingAndSaysSo(t *testing.T) {
	root := write(t, "", "", "")
	if err := os.Remove(filepath.Join(root, "services", "paste", "backup.conf")); err != nil {
		t.Fatal(err)
	}
	m, err := Parse([]byte(fixtureManifest))
	if err != nil {
		t.Fatal(err)
	}

	// The default still refuses the missing file.
	if p := strings.Join(m.AgreeWith(root, []byte(fixtureAdapted)), "\n"); !strings.Contains(p, "this file is not there") {
		t.Fatalf("under backup-conf a missing backup.conf is refused, got:\n%s", p)
	}

	if err := os.WriteFile(filepath.Join(root, "jcloud.yml"), []byte("backup: none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, notes := m.AgreeReport(root, AgreeInputs{Routes: []byte(fixtureAdapted)})
	if len(problems) != 0 {
		t.Fatalf("under backup: none nothing is compared, got:\n  %s", strings.Join(problems, "\n  "))
	}
	said := strings.Join(notes, "\n")
	for _, want := range []string{"backup not compared: jcloud.yml says backup: none", "volumes[data] holds sqlite paste.db"} {
		if !strings.Contains(said, want) {
			t.Errorf("want note %q, got:\n%s", want, said)
		}
	}

	// A backup.conf left in the tree is a file nothing reads, and is not compared.
	if err := os.WriteFile(filepath.Join(root, "services", "paste", "backup.conf"), []byte("sqlite other x.db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if problems, _ := m.AgreeReport(root, AgreeInputs{Routes: []byte(fixtureAdapted)}); len(problems) != 0 {
		t.Fatalf("got:\n  %s", strings.Join(problems, "\n  "))
	}
}

// A need whose addon asks a directive is a live writer too, and is named.
func TestBackupNoneNamesALiveDatabase(t *testing.T) {
	root := writeNeedy(t, "", "", "")
	if err := os.WriteFile(filepath.Join(root, "jcloud.yml"), []byte("backup: none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Parse([]byte(needyManifest))
	if err != nil {
		t.Fatal(err)
	}
	_, notes := m.AgreeReport(root, AgreeInputs{Routes: []byte(fixtureAdapted)})
	if said := strings.Join(notes, "\n"); !strings.Contains(said, "needs[db] is a mariadb database whose addon asks for `mysql`") {
		t.Fatalf("got:\n%s", said)
	}
}

func TestAnUnknownBackupKindIsRefused(t *testing.T) {
	if _, err := ParseProfile([]byte("backup: restic\n")); err == nil || !strings.Contains(err.Error(), "backup is \"restic\"") {
		t.Fatalf("got %v", err)
	}
}

// A note is said and refuses nothing: the exit code is a CI step's whole answer.
func TestNotesDoNotRefuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "japode.yml")
	if err := os.WriteFile(path, []byte(fixtureManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	noted := func(*Manifest) ([]string, []string) { return nil, []string{"said"} }
	if code := run([]string{path}, noted, "agrees", "agree"); code != 0 {
		t.Fatalf("a note alone should exit 0, got %d", code)
	}
	refused := func(*Manifest) ([]string, []string) { return []string{"wrong"}, []string{"said"} }
	if code := run([]string{path}, refused, "agrees", "agree"); code != 1 {
		t.Fatalf("a problem should exit 1, got %d", code)
	}
}
