package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The platform repositories the contract tests read: each one's manifests, its
// addons, its top-level compose file, the route configuration its own check
// script produced, and — on the Japode layout — the two scripts whose wording this
// package has to match (scripts/deploy.sh, scripts/check-env.sh).
//
// Those tests asserted "the manifests committed here pass" while this module lived
// under tools/jcloud in the Japode platform, and they reached the tree as ../../.
// Out here there is no committed tree, so the platforms name themselves through
// JCLOUD_PLATFORM, a list in the OS's own separator (`:` in the CI container), and
// without it the tests skip and say so instead of passing on an empty glob. Each
// platform's own check also runs `jcloud agree` over every manifest, which covers
// the same ground from the other side.
//
// Each platform is read through its own jcloud.yml (JCT10), so a test asserts only
// what that platform's profile says it has: its manifest template, its addon
// template, its route kind. A test about one Japode script runs where the layout is
// Japode's — no jcloud.yml — and nowhere else.
func platformRoots(t *testing.T) []string {
	t.Helper()
	var roots []string
	for _, root := range filepath.SplitList(os.Getenv("JCLOUD_PLATFORM")) {
		if root == "" {
			continue
		}
		if _, err := os.Stat(root); err != nil {
			// Absent is skipped, as ci/check.sh skips it: a developer without one
			// of the checkouts still runs the rest.
			t.Logf("JCLOUD_PLATFORM names %s, which is not there: skipped", root)
			continue
		}
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		t.Skip("JCLOUD_PLATFORM names no platform repository to read committed manifests from")
	}
	return roots
}

// eachPlatform runs body once per platform, as a subtest named after it, with that
// platform's profile read.
func eachPlatform(t *testing.T, body func(t *testing.T, root string, prof *Profile)) {
	t.Helper()
	for _, root := range platformRoots(t) {
		t.Run(filepath.Base(filepath.Clean(root)), func(t *testing.T) {
			prof, err := LoadProfile(root)
			if err != nil {
				t.Fatalf("%s: %v", root, err)
			}
			body(t, root, prof)
		})
	}
}

// japodePlatform runs body on each platform with the Japode layout — no jcloud.yml
// — and skips the others: what it asserts is a Japode script's wording.
func japodePlatform(t *testing.T, body func(t *testing.T, root string)) {
	t.Helper()
	eachPlatform(t, func(t *testing.T, root string, _ *Profile) {
		if _, err := os.Stat(filepath.Join(root, ProfileFile)); err == nil {
			t.Skipf("%s carries a jcloud.yml: its scripts are its own, not the Japode ones this asserts", root)
		}
		body(t, root)
	})
}

// manifestPaths is every manifest the platform's profile finds.
func manifestPaths(t *testing.T, root string, prof *Profile) []string {
	t.Helper()
	names, err := prof.Services(root)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, name := range names {
		paths = append(paths, filepath.Join(root, prof.ManifestPath(name)))
	}
	return paths
}

// addonPaths is every provider the platform's addon template finds.
func addonPaths(t *testing.T, root string, prof *Profile) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(prof.Addon, "{type}", "*"))))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// Where a platform's own check script leaves agree's inputs: the route
// configuration for its kind and, when it builds one, the compose model. This
// package's convention and not the profile's — the files exist for agree and this
// test, written by the same script, and naming them in jcloud.yml would be
// configuration for a test.
var routesFile = map[string]string{
	"caddy": ".deploy-state/caddy-adapt.json",
	"nginx": ".deploy-state/nginx-T.txt",
}

const composeFile = ".deploy-state/compose-config.json"
