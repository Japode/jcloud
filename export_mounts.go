// What `jcloud export` would leave behind without a word (JCT35).
//
// The manifest is what export carries state from, and a manifest can be wrong in
// the one way no file comparison sees: state the running container keeps somewhere
// nobody declared. fespsp-institucional is the case that found it. Its compose
// leaves DISALLOW_FILE_MODS off so wp-admin can install plugins, and they land in
// wp-content/plugins, in the image's anonymous volume, not declared and not backed
// up. A move built from the manifest alone would carry the uploads and the
// database and arrive with every plugin gone, while the healthcheck still passes.
//
// So export asks docker what each container the service owns has mounted and
// compares that against what is declared: the manifest's volumes on the service's
// own container, the addon's volume on each need's, and a bind mount from inside
// the tree, which export carries as a file. Anything else is a refusal, and the
// remedy names both doors: declare it in the manifest, so it is carried like the
// rest, or pass `--leave <path>` to say it is known and meant to stay behind. What
// was left that way is recorded in the index.
//
// `agree` cannot ask this, because it reads files and not a running container.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// dockerMount is the part of `docker inspect`'s .Mounts this reads.
type dockerMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

// UndeclaredMounts answers every mount on the service's containers that export
// would not carry and leave does not name, as one problem each, and the mounts
// that leave did name.
func UndeclaredMounts(root, name string, leave []string, run streamRunner) (left, problems []string) {
	prof, err := LoadProfile(root)
	if err != nil {
		return nil, []string{err.Error()}
	}
	m, err := loadManifest(root, prof, name)
	if err != nil {
		return nil, []string{err.Error()}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, []string{err.Error()}
	}
	absRoot = filepath.ToSlash(absRoot)

	// Each container the service owns, with the mount points it declares. A
	// shared need's container is the platform's, and so are its mounts.
	declared := map[string][]string{}
	var order []string
	own := prof.Container(m.Name)
	order = append(order, own)
	for _, v := range m.Volumes {
		declared[own] = append(declared[own], v.Path)
	}
	needs, needProblems := m.resolveNeeds(root, prof)
	problems = append(problems, needProblems...)
	for _, n := range needs {
		if !n.owned() {
			continue
		}
		c := prof.Container(n.svc)
		order = append(order, c)
		declared[c] = append(declared[c], n.addon.Volume.Path)
	}

	wanted := map[string]bool{}
	for _, l := range leave {
		wanted[path.Clean(l)] = true
	}
	used := map[string]bool{}
	for _, c := range order {
		var out bytes.Buffer
		if err := run(&out, "docker", "inspect", "--format", "{{json .Mounts}}", c); err != nil {
			problems = append(problems, fmt.Sprintf("%s: its mounts could not be read, so what a move would leave behind is unknown: %v", c, err))
			continue
		}
		var mounts []dockerMount
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &mounts); err != nil {
			problems = append(problems, fmt.Sprintf("%s: docker inspect: %v", c, err))
			continue
		}
		sort.Slice(mounts, func(i, j int) bool { return mounts[i].Destination < mounts[j].Destination })
		for _, mt := range mounts {
			dest := path.Clean(mt.Destination)
			switch {
			case contains(declared[c], dest):
				continue
			case mt.Type == "tmpfs":
				continue // nothing on it outlives the container
			case mt.Type == "bind" && within(absRoot, filepath.ToSlash(mt.Source)):
				continue // a file of the tree, carried as one
			case wanted[dest]:
				used[dest] = true
				left = append(left, c+":"+dest)
				continue
			}
			what := "an anonymous volume"
			switch {
			case mt.Type == "bind":
				what = "a bind mount of " + mt.Source
			case mt.Type == "volume" && !anonymousVolume(mt.Name):
				what = "the volume " + mt.Name
			}
			problems = append(problems, fmt.Sprintf(
				"%s: %s is %s the manifest does not declare, and a move would arrive without it — declare it under volumes:, or pass --leave %s if it is meant to stay behind",
				c, dest, what, dest))
		}
	}
	for _, l := range leave {
		if !used[path.Clean(l)] {
			problems = append(problems, fmt.Sprintf("--leave %s: no container of %s mounts that path undeclared", l, name))
		}
	}
	return left, problems
}

// anonymousVolume is a volume docker named itself: 64 hex digits.
func anonymousVolume(name string) bool {
	if len(name) != 64 {
		return false
	}
	for _, r := range name {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// within is whether p is root or under it, both slash-separated and absolute.
func within(root, p string) bool {
	root = strings.TrimSuffix(root, "/")
	return p == root || strings.HasPrefix(p, root+"/")
}

// loadManifest reads and parses one service's manifest under root.
func loadManifest(root string, prof *Profile, name string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, prof.ManifestPath(name)))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", filepath.ToSlash(prof.ManifestPath(name)), unwrapPath(err))
	}
	return Parse(data)
}
