package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// layoutTarget is a site's own repository checked out on the new server, as FI194
// lays it out (JCT45): japode.yml at the repository root, the platform in jcloud/
// with its compose file under service/. It answers jcloud/, the root install is
// given, with the service's compose file and backup.conf already declared there,
// byte for byte the source's, and init.sql changed.
func layoutTarget(t *testing.T, withManifest bool) string {
	t.Helper()
	files := map[string]string{
		"jcloud/jcloud.yml":                 "manifest: ../japode.yml\nservice_dir: service\n",
		"jcloud/service/docker-compose.yml": needyCompose,
		"jcloud/service/backup.conf":        needyBackupConf,
		"jcloud/service/init.sql":           "-- the repository's newer seed\n",
		"jcloud/.env.example":               needyEnvExample,
	}
	if withManifest {
		files["japode.yml"] = needyManifest
	}
	return filepath.Join(layTree(t, files), "jcloud")
}

// layoutArchive exports the Japode-shaped fixture service.
func layoutArchive(t *testing.T) string {
	t.Helper()
	root := exportTree(t, nil)
	id := testRecipient(t)
	archive := filepath.Join(t.TempDir(), "paste.tar.gz")
	if code := export([]string{"paste", "--root", root, "--recipient", id.Recipient().String(), "-o", archive}); code != 0 {
		t.Fatalf("export: exit %d", code)
	}
	t.Setenv("JCLOUD_IDENTITY", id.String())
	return archive
}

// JCT45: into a target with its own jcloud.yml, each of the service's files goes
// where that layout puts it, nothing lands at the source's paths, a file the
// target already declares is kept (a difference reported, never refused), the
// source's jcloud.yml is not laid, and agree passes on the result.
func TestInstallLaysTheServiceIntoTheTargetsOwnLayout(t *testing.T) {
	archive := layoutArchive(t)
	target := layoutTarget(t, true)
	d := newFakeInstall()
	p, problems := planInstall(archive, target, "", d.run)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	for rel := range p.write {
		if strings.HasPrefix(rel, "services/") || rel == ProfileFile || strings.HasPrefix(rel, "../") {
			t.Fatalf("laid %s, at the source's layout", rel)
		}
	}
	for _, rel := range []string{"service/docker-compose.yml", "service/backup.conf", "service/init.sql"} {
		if _, laid := p.write[rel]; laid {
			t.Fatalf("overwrote %s, which the target declares", rel)
		}
	}
	for _, rel := range []string{"caddy/sites/paste.caddy", "addons/mariadb/addon.yml", ".deploy-state/images.env"} {
		if _, laid := p.write[rel]; !laid {
			t.Fatalf("%s was not laid where the target's layout puts it; laid %v", rel, keys(p.write))
		}
	}
	if !strings.Contains(strings.Join(p.notes, "\n"), "service/init.sql differs from the source's services/paste/init.sql; the target's is kept") {
		t.Fatalf("notes: %v", p.notes)
	}

	if code := runInstall(archive, target, "", 60, registryLogin{}, d.run); code != 0 {
		t.Fatalf("exit %d", code)
	}
	joined := strings.Join(d.calls, "\n")
	if !strings.Contains(joined, filepath.Join(target, "service", "docker-compose.yml")) {
		t.Fatalf("compose did not run the target's compose file:\n%s", joined)
	}
	if _, err := os.Stat(filepath.Join(target, "services")); err == nil {
		t.Fatal("a services/ directory was laid into the target")
	}
	// Agree reads every file from the target's layout and finds them in agreement;
	// the one thing it reports is that no `caddy adapt` output was passed, which
	// every agree without it says.
	problems, _ = p.m.AgreeReport(target, AgreeInputs{})
	for _, pr := range problems {
		if !strings.Contains(pr, "the caddy configuration was not supplied") {
			t.Fatalf("agree on the installed tree:\n  %s", strings.Join(problems, "\n  "))
		}
	}
}

// JCT45: a manifest the target's layout puts outside the root has to be there,
// since install never writes outside it; and without a jcloud.yml of its own the
// target still gets the source's layout.
func TestInstallIntoAnOwnLayoutNeedsItsManifest(t *testing.T) {
	archive := layoutArchive(t)
	_, problems := planInstall(archive, layoutTarget(t, false), "", newFakeInstall().run)
	if !strings.Contains(strings.Join(problems, "\n"), "../japode.yml: the target's jcloud.yml puts it there, outside the root, and it is not there") {
		t.Fatalf("problems: %v", problems)
	}

	p, problems := planInstall(archive, t.TempDir(), "", newFakeInstall().run)
	if len(problems) > 0 || p.mapped {
		t.Fatalf("problems %v, mapped %v", problems, p.mapped)
	}
	if _, laid := p.write["services/paste/docker-compose.yml"]; !laid {
		t.Fatal("a target with no layout of its own did not get the source's")
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
