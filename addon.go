// The other half of a service's declaration: what the platform brings along when
// a manifest says it needs one.
//
// JC32 left two questions to be answered before any of this was built. The first
// — whether a provider is data this repository carries or something fetched from
// elsewhere — is answered by this file existing: an addon is `addons/<type>/addon.yml`,
// read from the tree, so the bytes a database runs on are in the same `git log` as
// the service that asked for it. The second, what a shared database means for a
// restore that puts back whole volumes one service at a time, is answered by
// refusing the question: a need is *one service's*, its volume is named after that
// service, and two services wanting a database get two containers. Nothing here
// can express a database two services share, which is what keeps `restore-test.sh`
// meaning what it already meant.
//
// Like the manifest, this renders nothing. The db container is written by hand in
// services/<name>/docker-compose.yml, and Agree is what proves the two agree —
// same rule, same reason (JC34).
package main

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AddonSchemaVersion is the only version this build accepts, and it is the
// manifest's own number rather than a second one. The two files are read by one
// build and change together; a provider format that could be a version behind the
// manifest format is a second thing to keep in step, which is what the whole of
// jcloud exists to have less of.
const AddonSchemaVersion = SchemaVersion

// Addon is one resource type, as the platform provides it.
type Addon struct {
	Version int    `yaml:"version"`
	Type    string `yaml:"type"`
	// Carries a digest, which the manifest's `image:` is refused for. The two
	// rules are opposite because the pinning is: the deploy resolves and pins one
	// `<SVC>_IMAGE` per service and knows nothing about this file, so an addon
	// image that is a bare tag is the one container here whose bytes nothing
	// decides — see JC8, which is the same argument about the other half.
	Image string `yaml:"image"`
	Port  int    `yaml:"port"`

	Volume AddonVolume `yaml:"volume"`
	Health AddonHealth `yaml:"health"`

	// Names the container reads, whose values are the asking service's to state.
	Env []string `yaml:"env"`
	// And what the platform decides instead of the service, value and all.
	Fixed map[string]string `yaml:"fixed"`
}

type AddonVolume struct {
	Path string `yaml:"path"`
	// The backup.conf directive this volume needs, or empty for one a
	// byte-for-byte copy is right for. The word is not checked against a list
	// here: scripts/backup-lib.sh is what a directive means, and a second copy of
	// that list in another language is exactly the drift this tool exists to
	// remove. An unknown one fails scripts/check-backup-conf.sh, in the file that
	// owns the vocabulary.
	Backup string `yaml:"backup"`
}

type AddonHealth struct {
	// The whole compose `test:`, element by element. There is no `probe:` here:
	// the three named probes exist for an application image whose health path is
	// the manifest's, and a database is probed by whatever its own image ships.
	Test        []string `yaml:"test"`
	StartPeriod string   `yaml:"start_period"`
}

// ParseAddon reads one provider. KnownFields for the manifest's reason: a
// misspelt key here is a fact silently left unchecked in the compose file.
func ParseAddon(data []byte) (*Addon, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	var a Addon
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("not an addon.yml: %w", err)
	}
	return &a, nil
}

// Validate returns every problem it finds, for Manifest.Validate's reason.
//
// `at` is the path it was read from, because an addon is found by type and the
// reader of the error is looking for a file.
func (a *Addon) Validate(at string) []string {
	var p []string
	bad := func(format string, args ...any) {
		p = append(p, fmt.Sprintf("%s: %s", at, fmt.Sprintf(format, args...)))
	}

	switch {
	case a.Version == 0:
		bad("version: is missing — it states the schema, and this build accepts %d", AddonSchemaVersion)
	case a.Version != AddonSchemaVersion:
		bad("version: %d, but this build accepts %d", a.Version, AddonSchemaVersion)
	}

	if a.Type == "" {
		bad("type: is missing — it is the directory this file sits in, stated again so the file says what it is")
	} else if !nameRE.MatchString(a.Type) {
		bad("type: %q is not a short name: lower case, digits and dashes, starting with a letter", a.Type)
	}

	switch {
	case a.Image == "":
		bad("image: is missing")
	case !strings.Contains(a.Image, "@sha256:"):
		bad("image: %q carries no digest — nothing pins an addon's image, so a tag here is a container whose bytes change under a deploy that changed nothing", a.Image)
	}

	if a.Port == 0 {
		bad("port: is missing — the port the service in front of it dials")
	} else if a.Port < 1 || a.Port > 65535 {
		bad("port: %d is not a port", a.Port)
	}

	if a.Volume.Path == "" {
		bad("volume.path: is missing — a database with no volume loses everything on the next deploy")
	} else if !strings.HasPrefix(a.Volume.Path, "/") {
		bad("volume.path: %q is not an absolute path", a.Volume.Path)
	}

	if len(a.Health.Test) == 0 {
		bad("health.test: is missing — the service in front waits on it through depends_on: service_healthy")
	}
	if a.Health.StartPeriod != "" && !durationRE.MatchString(a.Health.StartPeriod) {
		bad("health.start_period: %q is not a duration like 20s or 1m30s", a.Health.StartPeriod)
	}

	for _, e := range a.Env {
		if !envRE.MatchString(e) {
			bad("env: %q is not a variable name, upper case", e)
		}
	}
	for k := range a.Fixed {
		if !envRE.MatchString(k) {
			bad("fixed: %q is not a variable name, upper case", k)
		}
	}
	return p
}
