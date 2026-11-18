// The configuration half of `jcloud export` (JCT33): the values of exactly the keys
// one service reads, encrypted, inside the archive.
//
// Which keys is derived, as the file set is. The manifest names every variable it
// declares; one with `value:` is fixed in the compose file and is not configuration,
// and every other one is read by EnvKey's rule (the prefix and its name, or a shared
// key by its bare name). The edge reads more: a Caddy route's `{$KEY}` placeholders,
// such as <NAME>_HOSTS, are found by reading the route and not by a list kept here.
//
// The values come from the deployed `.env` when there is one, because that is what
// the service runs with, and otherwise from `.env.age` opened with the identity
// `jcloud env render` would use. Only this service's keys travel: a server holding
// several services has one `.env` with all of them.
//
// They are written in .env.age's own format (env.go), sealed to the recipients given
// on the command line, as jcloud-env.age at the archive's root. There is no
// recipient-less export: an archive carrying a database password in clear is a file
// that leaks by being copied. The keys stay readable, so what the archive carries is
// visible without an identity, the property .env.age was built on.
//
// A key read with no value on the source and no default is reported, never invented,
// and listed in the index, so the install that reads it can refuse to start the
// service without it.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"filippo.io/age"
)

// ExportEnvMember is where the sealed configuration sits in the archive.
const ExportEnvMember = "jcloud-env.age"

// exportEnvKey is one key the service reads, and whether a default stands in for it
// when the configuration does not set it.
type exportEnvKey struct {
	key        string
	hasDefault bool
}

// placeholderRE is a Caddyfile environment placeholder, `{$KEY}` or `{$KEY:default}`.
var placeholderRE = regexp.MustCompile(`\{\$([A-Z][A-Z0-9_]*)(:[^}]*)?\}`)

// exportEnvKeys is every configuration key the service reads, in the manifest's
// order and then the route's.
func exportEnvKeys(root string, prof *Profile, m *Manifest) []exportEnvKey {
	var out []exportEnvKey
	seen := map[string]bool{}
	add := func(k exportEnvKey) {
		if !seen[k.key] {
			seen[k.key] = true
			out = append(out, k)
		}
	}
	for _, e := range m.Env {
		if e.Value != nil {
			continue
		}
		add(exportEnvKey{m.EnvKey(e), e.Default != nil})
	}
	// The keys the host's timers read beside the service's (JCT43): where its
	// backups go, where its watch alerts. Without them a timer install wrote fails
	// at its first run.
	for _, k := range prof.Host.timerEnv() {
		add(exportEnvKey{k, false})
	}
	// Only a Caddy route is the service's own and reads the environment; an nginx
	// edge states its hosts in server_name.
	if prof.RouteKind == "caddy" {
		if route, err := os.ReadFile(filepath.Join(root, prof.RouteFile(m.Name))); err == nil {
			for _, match := range placeholderRE.FindAllStringSubmatch(string(route), -1) {
				add(exportEnvKey{match[1], match[2] != ""})
			}
		}
	}
	return out
}

// parseDotEnv reads `KEY=value` lines as docker compose does for this purpose: a
// comment or a blank line is skipped, and one layer of matching quotes is taken off.
func parseDotEnv(data string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		key, value, ok := strings.Cut(line, "=")
		if !ok || !envKeyRE.MatchString(key) {
			continue
		}
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		out[key] = value
	}
	return out
}

// sourceEnv is the configuration the service runs with: `.env` when deployed, and
// `.env.age`, opened, otherwise. Only the keys asked for are opened.
func sourceEnv(root, identity string, keys []exportEnvKey) (map[string]string, string, error) {
	plain, err := os.ReadFile(filepath.Join(root, ".env"))
	if err == nil {
		return parseDotEnv(string(plain)), ".env", nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, "", err
	}
	sealed, err := os.ReadFile(filepath.Join(root, EnvFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("neither .env nor %s is under the root, so there is no configuration to carry", EnvFileName)
	}
	if err != nil {
		return nil, "", err
	}
	f := ParseEnvFile(sealed)
	ids, err := LoadIdentities(identity)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", EnvFileName, err)
	}
	out := map[string]string{}
	for _, k := range keys {
		b64, ok := f.Get(k.key)
		if !ok {
			continue
		}
		value, err := open(b64, ids)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %s: %w", EnvFileName, k.key, err)
		}
		out[k.key] = string(value)
	}
	return out, EnvFileName, nil
}

// ExportEnv seals the service's configuration to recipients. It answers the member,
// the keys read with nothing to stand in for them, and the file the values came
// from.
func ExportEnv(root, name, identity string, recipients []string) (member exportFile, missing []string, from string, err error) {
	if len(recipients) == 0 {
		return exportFile{}, nil, "", fmt.Errorf("no --recipient: nothing could open what this wrote, and the values are not carried in clear")
	}
	var sealTo []age.Recipient
	for _, r := range recipients {
		parsed, err := age.ParseX25519Recipient(r)
		if err != nil {
			return exportFile{}, nil, "", fmt.Errorf("--recipient %s: %w", r, err)
		}
		sealTo = append(sealTo, parsed)
	}
	prof, err := LoadProfile(root)
	if err != nil {
		return exportFile{}, nil, "", err
	}
	data, err := os.ReadFile(filepath.Join(root, prof.ManifestPath(name)))
	if err != nil {
		return exportFile{}, nil, "", unwrapPath(err)
	}
	m, err := Parse(data)
	if err != nil {
		return exportFile{}, nil, "", err
	}

	keys := exportEnvKeys(root, prof, m)
	values, from, err := sourceEnv(root, identity, keys)
	if err != nil {
		return exportFile{}, nil, "", err
	}

	f := ParseEnvFile([]byte("# " + name + "'s configuration, from " + from + " by jcloud export\n"))
	for _, r := range recipients {
		f.lines = append(f.lines, envLine{raw: recipientPrefix + r})
	}
	for _, k := range keys {
		value, ok := values[k.key]
		if !ok {
			if !k.hasDefault {
				missing = append(missing, k.key)
			}
			continue
		}
		if err := ValidateEnvValue([]byte(value)); err != nil {
			return exportFile{}, nil, "", fmt.Errorf("%s: %w", k.key, err)
		}
		b64, err := seal([]byte(value), sealTo)
		if err != nil {
			return exportFile{}, nil, "", fmt.Errorf("%s: %w", k.key, err)
		}
		f.Set(k.key, b64)
	}
	return newExportFile(ExportEnvMember, f.Bytes(), false, false), missing, from, nil
}
