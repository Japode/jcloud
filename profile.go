// jcloud.yml: the platform's own statement of where things are (JCT2).
//
// Every path jcloud opened used to be a literal in its code, spelled the way the
// Japode platform spells it: services/*/japode.yml, caddy/sites/<name>.caddy,
// container_name: japode-<name>, .deploy-state/images.env, the scripts/deploy.sh
// argv, addons/<type>/addon.yml. A second platform keeps the same facts under other
// names, and against those literals every check refused it before asking one real
// question.
//
// So the platform root may carry one file that names them. Absent, every value is
// the one Japode uses, which is why the Japode platform changes no byte. It is data
// the platform repository carries, for addon.yml's reason: what a check holds a tree
// to sits in the same git log as the tree. jcloud reads it and never fetches one.
//
// It names a layout and nothing more. It does not render: JC34 refused generating
// compose files or routes from a manifest, and a second platform does not reopen
// that.
//
// The templates use three words. {name} is the manifest's short name, {NAME} is
// that name as SCREAM spells it (the variable prefix), and {type} is a need's type.
// {image} is the resolved image, and only the deploy command takes it.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProfileFile is what a platform root carries to describe itself.
const ProfileFile = "jcloud.yml"

// Profile is the layout every command reads a platform through. The fields are
// resolved: a key jcloud.yml left out holds the Japode value, so nothing
// downstream asks whether one was given.
type Profile struct {
	Manifest        string // where a manifest sits, with {name}
	RoutePath       string // the route source, with {name}
	RouteKind       string // the grammar that source is written in
	ContainerPrefix string // what container_name puts in front of the short name
	ImageVar        string // the variable the deploy pins an image in, with {NAME}
	PinFile         string // where the deploy writes those variables
	Deploy          string // the command `up` prints, with {name} and {image}
	Addon           string // where a need's provider sits, with {type}
}

// DefaultProfile is the Japode platform's layout, and what a root with no
// jcloud.yml is read as.
func DefaultProfile() *Profile {
	return &Profile{
		Manifest:        "services/{name}/japode.yml",
		RoutePath:       "caddy/sites/{name}.caddy",
		RouteKind:       "caddy",
		ContainerPrefix: "japode-",
		ImageVar:        "{NAME}_IMAGE",
		PinFile:         ".deploy-state/images.env",
		Deploy:          "DEPLOY_SERVICE={name} DEPLOY_IMAGE={image} bash scripts/deploy.sh {name}",
		Addon:           "addons/{type}/addon.yml",
	}
}

// profileFile is jcloud.yml as written. Pointers, because an empty container
// prefix is a value a platform can mean (containers named by their short name
// alone), and it must not read as "left out".
type profileFile struct {
	Manifest *string `yaml:"manifest"`
	Route    *struct {
		Path *string `yaml:"path"`
		Kind *string `yaml:"kind"`
	} `yaml:"route"`
	ContainerPrefix *string `yaml:"container_prefix"`
	ImageVar        *string `yaml:"image_var"`
	PinFile         *string `yaml:"pin_file"`
	Deploy          *string `yaml:"deploy"`
	Addon           *string `yaml:"addon"`
}

// routeKinds is every route grammar this build can read. One, until JCT3.
var routeKinds = []string{"caddy"}

// LoadProfile reads root/jcloud.yml, or answers the Japode layout when there is
// none. Any other read failure is an error: a profile that exists and cannot be
// read is not a platform that has none.
func LoadProfile(root string) (*Profile, error) {
	data, err := os.ReadFile(filepath.Join(root, ProfileFile))
	if errors.Is(err, os.ErrNotExist) {
		return DefaultProfile(), nil
	}
	if err != nil {
		return nil, err
	}
	return ParseProfile(data)
}

// ParseProfile reads one jcloud.yml and refuses it whole when anything in it is
// wrong. KnownFields for the manifest's reason: a misspelt key would leave the
// Japode value standing, and every check would then report the platform's real
// files as missing, which reads as drift rather than as a typo here.
func ParseProfile(data []byte) (*Profile, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	var f profileFile
	// An empty file says nothing, which is the same as saying every default.
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", ProfileFile, err)
	}

	p := DefaultProfile()
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&p.Manifest, f.Manifest)
	if f.Route != nil {
		set(&p.RoutePath, f.Route.Path)
		set(&p.RouteKind, f.Route.Kind)
	}
	set(&p.ContainerPrefix, f.ContainerPrefix)
	set(&p.ImageVar, f.ImageVar)
	set(&p.PinFile, f.PinFile)
	set(&p.Deploy, f.Deploy)
	set(&p.Addon, f.Addon)

	if problems := p.Validate(); len(problems) > 0 {
		return nil, fmt.Errorf("%s: %s", ProfileFile, strings.Join(problems, "; "))
	}
	return p, nil
}

// Validate returns every problem at once, for Manifest.Validate's reason.
func (p *Profile) Validate() []string {
	var out []string
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	relative := func(key, value string) bool {
		switch {
		case value == "":
			bad("%s is empty", key)
		case filepath.IsAbs(value) || path.IsAbs(value):
			bad("%s is %q, and a path here is relative to the platform root", key, value)
		case strings.Split(path.Clean(filepath.ToSlash(value)), "/")[0] == "..":
			bad("%s is %q, which leaves the platform root", key, value)
		default:
			return true
		}
		return false
	}

	// The manifest template is read in both directions: forwards to find one
	// service's file, backwards to say which service a file found by the glob
	// belongs to. Both need {name} exactly once and no other wildcard.
	if relative("manifest", p.Manifest) {
		switch {
		case strings.Count(p.Manifest, "{name}") != 1:
			bad("manifest is %q and has to name {name} exactly once", p.Manifest)
		case strings.ContainsAny(p.Manifest, "*?["):
			bad("manifest is %q, and {name} is its only wildcard", p.Manifest)
		}
	}
	if relative("route.path", p.RoutePath) && !strings.Contains(p.RoutePath, "{name}") {
		bad("route.path is %q and does not name {name}, so every service would share one route", p.RoutePath)
	}
	known := false
	for _, k := range routeKinds {
		known = known || p.RouteKind == k
	}
	if !known {
		bad("route.kind is %q, and this build reads %s", p.RouteKind, strings.Join(routeKinds, ", "))
	}
	if !strings.Contains(p.ImageVar, "{NAME}") {
		bad("image_var is %q and does not name {NAME}, so every service would pin one variable", p.ImageVar)
	}
	relative("pin_file", p.PinFile)
	if strings.TrimSpace(p.Deploy) == "" {
		bad("deploy is empty")
	}
	if relative("addon", p.Addon) && !strings.Contains(p.Addon, "{type}") {
		bad("addon is %q and does not name {type}, so every need would read one provider", p.Addon)
	}
	return out
}

// ManifestPath is where one service's manifest sits, relative to the root.
func (p *Profile) ManifestPath(name string) string {
	return filepath.FromSlash(strings.ReplaceAll(p.Manifest, "{name}", name))
}

// ServiceDir is the directory beside a manifest, where its compose file and its
// backup.conf are read from.
func (p *Profile) ServiceDir(name string) string {
	return filepath.Dir(p.ManifestPath(name))
}

// Services finds every manifest under root and answers the service each one is,
// sorted, so a report reads in the same order on every run. The name comes from
// the template and not from the directory, so a layout that does not put {name}
// in a directory of its own is read the same way. ManifestPath turns a name back
// into its file.
func (p *Profile) Services(root string) ([]string, error) {
	glob := filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(p.Manifest, "{name}", "*")))
	found, err := filepath.Glob(glob)
	if err != nil {
		return nil, err
	}
	before, after, _ := strings.Cut(path.Clean(p.Manifest), "{name}")
	var names []string
	for _, file := range found {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(rel, before), after))
	}
	sort.Strings(names)
	return names, nil
}

// RouteFile is one service's route source, relative to the root.
func (p *Profile) RouteFile(name string) string {
	return filepath.FromSlash(strings.ReplaceAll(p.RoutePath, "{name}", name))
}

// Container is what container_name reads for a service or a need's derived name.
func (p *Profile) Container(name string) string {
	return p.ContainerPrefix + name
}

// ImageVarOf is the variable the deploy pins this manifest's image in.
func (p *Profile) ImageVarOf(m *Manifest) string {
	return strings.ReplaceAll(p.ImageVar, "{NAME}", m.SCREAM())
}

// AddonFile is where the provider of a need's type sits, relative to the root.
func (p *Profile) AddonFile(typ string) string {
	return filepath.FromSlash(strings.ReplaceAll(p.Addon, "{type}", typ))
}

// DeployCommand is the deploy's own arguments for one service and one image,
// printed and never run.
func (p *Profile) DeployCommand(name, image string) string {
	return strings.NewReplacer("{name}", name, "{image}", image).Replace(p.Deploy)
}
