package main

import (
	"os"
	"path/filepath"
	"testing"
)

// platformRoot is the platform repository the contract tests read: its
// services/*/japode.yml, its addons/*/addon.yml, its top-level compose file, the
// Caddy config adapted from its Caddyfile, and the two scripts whose wording this
// package has to match (scripts/deploy.sh, scripts/check-env.sh).
//
// Those tests asserted "the manifests committed here pass" while this module lived
// under tools/jcloud in that repository, and they reached the tree as ../../. Out
// here there is no committed tree, so the platform names itself through
// JCLOUD_PLATFORM, and without it the tests skip and say so instead of passing on
// an empty glob. The platform's own check also runs `jcloud agree` over every
// manifest, which covers the same ground from the other side.
func platformRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("JCLOUD_PLATFORM")
	if root == "" {
		t.Skip("JCLOUD_PLATFORM is not set: no platform repository to read committed manifests from")
	}
	return root
}

func platformGlob(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(platformRoot(t), filepath.FromSlash(pattern)))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}
