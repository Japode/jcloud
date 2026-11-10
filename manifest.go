// The schema of japode.yml, and nothing else.
//
// docs/JCLOUD.md is the specification; this file is the half of it a machine can
// refuse. What it deliberately does not do is render: turning a manifest into the
// compose file, the route, backup.conf and the .env.example block is JC28, and a
// renderer that also decided what was legal would be the only statement of the
// rules, written where nobody reads it.
//
// Every rule below is stated once. A field a service may leave out is a pointer or
// a zero value; a field it may not is checked here and named in the error, because
// "invalid manifest" sends somebody back to the specification to guess which line.
package main

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the only version this build accepts. `version:` in the manifest
// is the schema's and not the tool's — the tool's own version is JC31, and the two
// being one number is how a renderer's bug fix becomes a breaking schema change.
const SchemaVersion = 1

// Manifest is one service's whole declaration.
//
// KnownFields is asserted on decode (see Parse), so a misspelt key is an error and
// not a silently ignored line. That matters more here than in most schemas: every
// field that is quietly dropped is a fact that stays written by hand in the four
// files this replaces, which is the drift the manifest exists to remove.
type Manifest struct {
	Version int    `yaml:"version"`
	Name    string `yaml:"name"`
	Image   string `yaml:"image"`
	// The alternative to Image, for a platform that builds a service on its own
	// host (JCT7): the Dockerfile, relative to the platform root. Nothing is
	// pinned for it, because nothing is pulled. Refused by agree and up unless
	// jcloud.yml says `builds: true`, which the Japode platform does not.
	Build string `yaml:"build"`
	Port  int    `yaml:"port"`

	Health  Health   `yaml:"health"`
	Volumes []Volume `yaml:"volumes"`
	Routes  []Route  `yaml:"routes"`
	Env     []EnvVar `yaml:"env"`

	// A dependency the platform brings along — a database, a cache. Declared from
	// version 1 and refused when non-empty until JC32, which is what resolves one:
	// each entry names a type, and addons/<type>/addon.yml is the provider.
	Needs []Need `yaml:"needs"`

	// The compose profile the service runs under, when it is not the default one
	// (JCT4). A platform that declares every service in one compose file keeps the
	// opt-in ones apart this way, and both the model check and `survey` read it:
	// the first holds the compose file to it, the second does not report a service
	// missing whose profile was never switched on.
	Profile string `yaml:"profile"`

	// What this service's configuration keys start with, `_` included (JCT8).
	// Absent, it is the short name upper-cased with `-` as `_`, which is what the
	// Japode platform's keys have always been. EnvPrefix answers either.
	EnvPrefixDecl string `yaml:"env_prefix"`

	// Copies of this service a platform renders per client (JCT9), each a compose
	// service of its own. Declared by template, never listed: the platform's
	// generator owns the registry, and the copies are found in the compose model.
	Instances *Instances `yaml:"instances"`
}

// Instances names a service's per-client copies by templates over {client}.
type Instances struct {
	Service string `yaml:"service"` // the compose service, e.g. turing-{client}
	Host    string `yaml:"host"`    // its route's host, e.g. turing-{client}.viglet.cloud
	Profile string `yaml:"profile"` // its compose profile, e.g. client-{client}
}

type Health struct {
	Path string `yaml:"path"`
	// How the container probes itself. The three named probes render a compose
	// `test:` for images that carry them; Test is the escape hatch for an image
	// that carries none, and naming both is refused.
	Probe string   `yaml:"probe"`
	Test  []string `yaml:"test"`
	// How long the *container* waits before its own healthcheck counts, and how
	// often the *edge* asks. Two different things, and both were in the route or
	// the compose file rather than here — `health_interval` in particular was
	// declared nowhere at all, so nothing would have noticed it becoming 15m.
	StartPeriod string `yaml:"start_period"`
	Interval    string `yaml:"interval"`
}

// The edge's active health check interval, which the route carries and Caddy
// defaults to 30s. Declared here so the proxy body is fully described and the
// comparison against it can be strict (JC41).
const DefaultHealthInterval = "15s"

func (h *Health) interval() string {
	if h.Interval == "" {
		return DefaultHealthInterval
	}
	return h.Interval
}

type Volume struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	// Files whose writer holds them open. These become backup.conf's `sqlite`
	// lines, which is what buys VACUUM INTO instead of a byte-for-byte copy.
	SQLite []string `yaml:"sqlite"`
	// Paths inside the volume the backup skips — backup.conf's `exclude`.
	Exclude []string `yaml:"exclude"`
}

type Route struct {
	Hosts   []string `yaml:"hosts"`
	MaxBody string   `yaml:"max_body"`
	// Names this route's log file and its hosts variable, so two routes on one
	// service do not both derive the same name. Unique within a manifest.
	Log string `yaml:"log"`
	// The route is inert until this variable is set. Named without the service
	// prefix, which is derived.
	EnabledBy string `yaml:"enabled_by"`
	// The body of the rendered reverse_proxy block, declared once and rendered
	// into both this repository's route and the service's own Caddyfile.
	Proxy []string `yaml:"proxy"`
	// The authorisation subrequest the edge makes before proxying (JCT18): the
	// URI of nginx's `auth_request` or of Caddy's `forward_auth`. Which upstream
	// answers it is that upstream's own route, stated by its own manifest.
	Gate string `yaml:"gate"`
}

type EnvVar struct {
	Name string `yaml:"name"`
	// Default is what the compose file falls back to and what .env.example
	// suggests; Value is fixed and reaches neither. Naming both is refused,
	// because one of the two would have to win silently.
	Default *string `yaml:"default"`
	Value   *string `yaml:"value"`
	Secret  bool    `yaml:"secret"`
	Doc     string  `yaml:"doc"`
	// One key several services read, such as OPENAI_API_KEY (JCT8): its key is
	// the bare name with no prefix, other manifests may declare it too, and agree
	// holds all of them to one default and one secret.
	Shared bool `yaml:"shared"`
}

// Need is one dependency beside the service. The name is this service's word for
// it — `db` — and everything derived from it carries the service's own name in
// front, so two services needing a database get two of everything and never one
// shared: `<service>-<need>` is the compose service, the volume and, with
// `japode-` in front, the container. A database two services share is not
// expressible here, on purpose (see tools/jcloud/addon.go).
//
// Shared is the other shape (JCT5): a platform service several services reach by
// name, such as one postgres holding a database per product. Then the need names
// that compose service, nothing is derived from the service's own name, and the
// platform backs its volume up once rather than each service in its backup.conf.
type Need struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Shared string `yaml:"shared"`
}

// A short name is a container name, a volume name, a compose service and a file
// name, so it is held to what all four accept rather than to what each does.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)

// An environment variable name, before the service prefix is put on it. Upper
// snake case because that is what the rendered compose and .env.example carry, and
// two spellings of one variable is the drift this refuses.
var envRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// The path of an image reference, which the OCI grammar holds to lower case. Two
// components at most is not assumed: a registry may nest, and `ghcr.io/a/b/c` is a
// repository like any other.
var imagePathRE = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

// imagePath drops the registry host, which is not held to that rule: a hostname is
// case-insensitive and `Ghcr.io` reaches the same registry as `ghcr.io`. Docker's
// own test for whether the first component is a host is a dot, a colon, or the word
// localhost — the colon cannot appear here, since a tag and a digest are already
// refused above, so a port is refused with them.
func imagePath(image string) string {
	host, rest, found := strings.Cut(image, "/")
	if found && (strings.Contains(host, ".") || host == "localhost") {
		return rest
	}
	return image
}

// A duration compose and Caddy both accept. Deliberately narrow: `20s` and `1m30s`
// pass, `20 s` and `20seconds` do not, because a value this tool passes through
// without understanding is one the render discovers on the server.
var durationRE = regexp.MustCompile(`^([0-9]+(ms|s|m|h))+$`)

// A body size Caddy accepts, which is not a compose duration and not a bare number.
var sizeRE = regexp.MustCompile(`^[0-9]+(B|KB|MB|GB|KiB|MiB|GiB)$`)

var probes = map[string]bool{"node": true, "curl": true, "wget": true}

// Parse reads one manifest. A misspelt key is an error here rather than a field
// silently left at its zero value.
func Parse(data []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("not a japode.yml: %w", err)
	}
	return &m, nil
}

// Validate returns every problem it finds, not the first. A manifest is written in
// one sitting, so a caller who fixes one field and runs again to find the next has
// been sent round a loop this could have answered once — the same argument
// read_backup_conf makes in scripts/backup-lib.sh.
func (m *Manifest) Validate() []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	switch {
	case m.Version == 0:
		bad("version: is missing — it states the schema, and this build accepts %d", SchemaVersion)
	case m.Version != SchemaVersion:
		bad("version: %d, but this build accepts %d", m.Version, SchemaVersion)
	}

	if m.Name == "" {
		bad("name: is missing — it is the container name, the volume prefix and the route file name")
	} else if !nameRE.MatchString(m.Name) {
		bad("name: %q is not a short name: lower case, digits and dashes, starting with a letter", m.Name)
	}

	switch {
	case m.Build != "" && m.Image != "":
		bad("image: and build: are both declared — an image is either pulled or built on the host, and one of the two would have to win silently")
	case m.Build != "":
		// Whether the platform builds anything at all is its profile's to say
		// (`builds:`, JCT7), and agree and up refuse it there. Here only the path.
		if path.IsAbs(m.Build) || strings.Split(path.Clean(m.Build), "/")[0] == ".." {
			bad("build: %q is not a path inside the platform tree", m.Build)
		}
	case m.Image == "":
		bad("image: is missing — the registry path, with no tag and no digest (or build:, the Dockerfile a platform that builds on the host builds it from)")
	case strings.ContainsAny(m.Image, "@:"):
		// The digest is resolved by whoever published it and pinned into
		// .deploy-state/images.env by the deploy. A tag here would be a second
		// place that decides which build runs, which is what JC8 refused.
		bad("image: %q carries a tag or a digest — the manifest names the repository, the deploy pins the digest", m.Image)
	case !imagePathRE.MatchString(imagePath(m.Image)):
		// A registry refuses an upper-case repository outright — GHCR answers
		// `repository name must be lowercase` — so a manifest carrying one names
		// an image nothing can pull, and the first thing to say so is a `docker
		// pull` on the server, mid-deploy. Refused here and never lowered: this
		// name decides which bytes run, and rewriting it quietly is the render
		// JC34 refused.
		bad("image: %q is not a repository a registry will serve — after the host it is lower case, digits and . _ -", m.Image)
	}

	if m.Port == 0 {
		bad("port: is missing — one port, the one the container listens on")
	} else if m.Port < 1 || m.Port > 65535 {
		bad("port: %d is not a port", m.Port)
	}

	p = append(p, m.Health.validate()...)
	p = append(p, m.validateVolumes()...)
	p = append(p, m.validateRoutes()...)
	p = append(p, m.validateEnv()...)

	p = append(p, m.validateNeeds()...)

	return p
}

func (h *Health) validate() []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	if h.Path == "" {
		bad("health.path: is missing — the deploy waits on it and the route probes it")
	} else if !strings.HasPrefix(h.Path, "/") {
		bad("health.path: %q is not a path", h.Path)
	}

	switch {
	case h.Probe == "" && len(h.Test) == 0:
		bad("health: needs a probe: of node, curl or wget, or a test: for an image with none of them")
	case h.Probe != "" && len(h.Test) > 0:
		bad("health: names both probe: %q and test: — one of the two would have to win silently", h.Probe)
	case h.Probe != "" && !probes[h.Probe]:
		bad("health.probe: %q is not one of node, curl, wget", h.Probe)
	}

	if h.StartPeriod != "" && !durationRE.MatchString(h.StartPeriod) {
		bad("health.start_period: %q is not a duration like 20s or 1m30s", h.StartPeriod)
	}
	if h.Interval != "" && !durationRE.MatchString(h.Interval) {
		bad("health.interval: %q is not a duration like 15s or 1m30s", h.Interval)
	}
	return p
}

func (m *Manifest) validateVolumes() []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	seen := map[string]bool{}
	paths := map[string]bool{}
	for i, v := range m.Volumes {
		at := fmt.Sprintf("volumes[%d]", i)
		if v.Name == "" {
			bad("%s.name: is missing — it becomes the volume %s-<name>", at, m.Name)
		} else if !nameRE.MatchString(v.Name) {
			bad("%s.name: %q is not a short name", at, v.Name)
		} else if seen[v.Name] {
			bad("%s.name: %q is declared twice — one volume, one name", at, v.Name)
		}
		seen[v.Name] = true

		if v.Path == "" {
			bad("%s.path: is missing — where the volume is mounted in the container", at)
		} else if !strings.HasPrefix(v.Path, "/") {
			bad("%s.path: %q is not an absolute path", at, v.Path)
		} else if paths[v.Path] {
			bad("%s.path: %q is already mounted by another volume", at, v.Path)
		}
		paths[v.Path] = true

		for _, f := range append(append([]string{}, v.SQLite...), v.Exclude...) {
			// Relative to the volume root, which is what backup.conf's second
			// field is. An absolute path here would read as a host path and the
			// backup would look for it in the wrong place.
			if strings.HasPrefix(f, "/") {
				bad("%s: %q is absolute — these are relative to the volume root, as in backup.conf", at, f)
			}
		}
	}
	return p
}

func (m *Manifest) validateRoutes() []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	logs := map[string]bool{}
	hosts := map[string]bool{}
	for i, r := range m.Routes {
		at := fmt.Sprintf("routes[%d]", i)
		if len(r.Hosts) == 0 {
			bad("%s.hosts: is missing — a route with no address reaches nothing", at)
		}
		for _, h := range r.Hosts {
			if h == "" || strings.ContainsAny(h, " /:") {
				bad("%s.hosts: %q is not a hostname", at, h)
			} else if hosts[h] {
				bad("%s.hosts: %q is claimed by another route — one host answers once", at, h)
			}
			hosts[h] = true
		}

		// The log name is what separates two routes' derived names, so it is
		// required the moment there is more than one and unique either way.
		if r.Log == "" {
			if len(m.Routes) > 1 {
				bad("%s.log: is missing — with more than one route it is what names this one's log file and hosts variable", at)
			}
		} else if !nameRE.MatchString(r.Log) {
			bad("%s.log: %q is not a short name", at, r.Log)
		} else if logs[r.Log] {
			bad("%s.log: %q is used by another route", at, r.Log)
		}
		logs[r.Log] = true

		if r.MaxBody != "" && !sizeRE.MatchString(r.MaxBody) {
			bad("%s.max_body: %q is not a size like 8MB or 20MiB", at, r.MaxBody)
		}
		if r.EnabledBy != "" && !envRE.MatchString(r.EnabledBy) {
			bad("%s.enabled_by: %q is not a variable name, unprefixed and upper case", at, r.EnabledBy)
		}
		if r.Gate != "" && (!strings.HasPrefix(r.Gate, "/") || strings.ContainsAny(r.Gate, " \t")) {
			bad("%s.gate: %q is not a URI path — it is the subrequest the edge makes, like /oauth2/auth", at, r.Gate)
		}
		for _, d := range r.Proxy {
			// The rendered block goes inside `reverse_proxy … { }` in both
			// copies, so a brace here would close a block this did not open.
			if strings.ContainsAny(d, "{}") && !strings.Contains(d, "{remote_host}") {
				bad("%s.proxy: %q carries a brace — these are directives inside the block, not the block", at, d)
			}
		}
	}
	return p
}

// Whether the type names a provider this platform has is not asked here: the
// schema is what a manifest must state, and which addons exist is a fact about the
// tree it is deployed in. `agree` reads addons/<type>/addon.yml and says so there,
// which is also where `check --repo` correctly stays quiet — the service's own
// repository has no addons/ directory and never will.
func (m *Manifest) validateNeeds() []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	// A need and a volume both derive `<service>-<name>`, one as a volume and one
	// as a compose service *and* a volume — so the same word twice is two things
	// asking for one volume, and the loser is whichever compose file is merged
	// second.
	volumes := map[string]bool{}
	for _, v := range m.Volumes {
		volumes[v.Name] = true
	}

	seen := map[string]bool{}
	for i, n := range m.Needs {
		at := fmt.Sprintf("needs[%d]", i)
		switch {
		case n.Name == "":
			bad("%s.name: is missing — it becomes the compose service %s-<name> and the volume of the same name", at, m.Name)
		case !nameRE.MatchString(n.Name):
			bad("%s.name: %q is not a short name", at, n.Name)
		case seen[n.Name]:
			bad("%s.name: %q is declared twice — one need, one name", at, n.Name)
		case volumes[n.Name] && n.Shared == "":
			bad("%s.name: %q is also a volume's name, and both derive the volume %s-%s", at, n.Name, m.Name, n.Name)
		}
		seen[n.Name] = true

		if n.Type == "" {
			bad("%s.type: is missing — it names the provider, addons/<type>/", at)
		} else if !nameRE.MatchString(n.Type) {
			bad("%s.type: %q is not a short name", at, n.Type)
		}

		switch {
		case n.Shared == "":
		case !nameRE.MatchString(n.Shared):
			bad("%s.shared: %q is not a short name — it is the compose service the platform runs", at, n.Shared)
		case n.Shared == m.Name:
			bad("%s.shared: %q is this service itself", at, n.Shared)
		}
	}

	// Compose's own grammar for a profile name, so a name compose would refuse is
	// refused here first.
	if m.Profile != "" && !profileRE.MatchString(m.Profile) {
		bad("profile: %q is not a compose profile name", m.Profile)
	}
	if in := m.Instances; in != nil {
		if strings.Count(in.Service, "{client}") != 1 {
			bad("instances.service: %q has to name {client} exactly once — it is how a copy is told from another", in.Service)
		}
		for _, f := range []struct{ key, value string }{{"host", in.Host}, {"profile", in.Profile}} {
			if f.value != "" && !strings.Contains(f.value, "{client}") {
				bad("instances.%s: %q does not name {client}, so every copy would share it", f.key, f.value)
			}
		}
	}
	if m.EnvPrefixDecl != "" && !envPrefixRE.MatchString(m.EnvPrefixDecl) {
		bad("env_prefix: %q is not a variable prefix: upper case, digits and _, ending in _ so that it is a whole word of every key", m.EnvPrefixDecl)
	}
	return p
}

var profileRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

var envPrefixRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*_$`)

func (m *Manifest) validateEnv() []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	seen := map[string]bool{}
	for i, e := range m.Env {
		at := fmt.Sprintf("env[%d]", i)
		if e.Name == "" {
			bad("%s.name: is missing", at)
		} else if !envRE.MatchString(e.Name) {
			bad("%s.name: %q is not a variable name, unprefixed and upper case", at, e.Name)
		} else if seen[e.Name] {
			bad("%s.name: %q is declared twice", at, e.Name)
		}
		seen[e.Name] = true

		if e.Default != nil && e.Value != nil {
			bad("%s: names both default: and value: — a fixed value is not configurable", at)
		}
		if e.Secret && (e.Default != nil || e.Value != nil) {
			// A secret's value belongs in the SERVICES_ENV secret and nowhere a
			// repository can render it. A default would be rendered into
			// .env.example, which is committed.
			bad("%s: is secret: and carries a value — a secret's value is never in this repository", at)
		}
	}
	return p
}
