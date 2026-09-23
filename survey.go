// `jcloud survey` — the machine against what this repository declares.
//
// The server is a copy of what is declared here, maintained by hand, and until JC96
// nothing compared the two. It is the shape JC28 settled for the port written in three
// files and JC49 for the toolchain digest written in three places, turned against the
// machine instead of against the tree.
//
// The measurement is JC2, which found three divergences on the live host with ad-hoc
// `ssh` nobody can reproduce and nothing records — a directory mode, a firewall
// installed and never enabled, and three systemd timers that had never been installed
// at all, leaving 26 days of three services serving with no copy of anything taken.
//
// **This file reaches no network.** The observations arrive as a file, gathered by
// scripts/survey-server.sh, which is the same division `compare` already makes: it
// takes two paths rather than a repository, because the fetching belongs in the script
// that holds the credential (JC29). It also means every assertion here is testable
// without a server, which is the half of this that has to keep working.
//
// What it compares is what the manifests declare: a service that should be running, a
// container that nothing declares, and the digest pinned against the digest actually
// up. The machine's own facts — modes, the firewall, the units — are asserted by the
// collector, which is where the knowledge of how to read them already lives.
//
// It writes nothing and repairs nothing. The repair half owes a decision record first:
// JC1 refused deploying over SSH from a hosted runner, and a second path that writes to
// the server has to argue why repair is not a deploy before a line of it exists.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Observed is what the collector saw, as a file of `<kind> <name> <value>` lines.
//
// Three fields and no more: a format a shell script can emit with `printf` and this
// can read with `strings.Fields` is one neither side can get subtly wrong. Anything
// else in the file is ignored rather than refused — the collector reports machine
// facts this does not compare, and a format that grew a field would otherwise break
// the half that was still right.
type Observed struct {
	Containers map[string]string // name -> status word, e.g. "healthy"
	Pinned     map[string]string // service -> digest in .deploy-state/images.env
	Running    map[string]string // service -> digest the container is actually on
}

func ParseObserved(body string) *Observed {
	o := &Observed{
		Containers: map[string]string{},
		Pinned:     map[string]string{},
		Running:    map[string]string{},
	}
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch f[0] {
		case "container":
			o.Containers[f[1]] = f[2]
		case "pinned":
			o.Pinned[f[1]] = f[2]
		case "running":
			o.Running[f[1]] = f[2]
		}
	}
	return o
}

// Divergence is one disagreement, named by the service it is about so a report can be
// read service by service rather than assertion by assertion.
type Divergence struct {
	Service string
	Says    string
}

// Declared is everything this repository says should be up, and the distinction that
// matters is which of them the deploy pins a digest for.
//
// Services are the `services/*/` directories: the deploy globs them and writes one
// `<SCREAM>_IMAGE` per directory, so a digest comparison means something. Others are
// containers this repository declares by other means and pins nothing for — the edge,
// named in the top-level compose file and deliberately given no manifest
// (scripts/check-layout.sh says why), and each service's addons, whose container is
// `<service>-<need>` derived from `needs:` and whose image is the addon's own.
//
// Told apart because reporting an addon as "declared by nothing" is the false positive
// this comparison would otherwise open with: japode-fespsp-institucional-db is a
// container nobody wrote a directory for and every bit as declared as its service.
type Declared struct {
	Services []string
	Others   []string
}

// standing answers the one question asked of everything declared: is it up, and is it
// well. Nil when it is both. Factored out because the edge, the addons and the services
// are three lists asking it, and three spellings of one question is the drift this
// whole comparison exists to end.
func standing(name string, o *Observed) *Divergence {
	status, up := o.Containers[name]
	switch {
	case !up:
		// The state fespsp-iwl is in as this is written: declared in JC90, never
		// deployed, and the only thing that ever said so was a deploy failing.
		return &Divergence{name, "declared here and not running on the server"}
	case status != "healthy":
		return &Divergence{name, fmt.Sprintf("running and %s, not healthy", status)}
	}
	return nil
}

// strangers is the other direction, and the one a survey exists for: something is up
// that this repository does not declare. A container nobody declared is one nobody
// reviewed, and it is invisible to every other check here, all of which walk
// `services/*/` and would never look at it.
func strangers(declared map[string]bool, o *Observed) []Divergence {
	var names []string
	for name := range o.Containers {
		if !declared[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out []Divergence
	for _, name := range names {
		out = append(out, Divergence{name, "running on the server and declared by nothing here"})
	}
	return out
}

// SurveyAgainst compares what should be up against what was observed.
func SurveyAgainst(d Declared, o *Observed) []Divergence {
	var out []Divergence
	is := map[string]bool{}
	for _, name := range d.Services {
		is[name] = true
	}
	for _, name := range d.Others {
		is[name] = true
	}

	// The edge and the addons get the same up-and-healthy question and no digest one.
	for _, name := range d.Others {
		if bad := standing(name, o); bad != nil {
			out = append(out, *bad)
		}
	}

	for _, service := range d.Services {
		if bad := standing(service, o); bad != nil {
			out = append(out, *bad)
			// Absent is the end of what can be said about it: a digest comparison
			// about a container that is not there would be a second complaint about
			// one fault. Unhealthy is not — it is up, on some digest, and which one
			// is exactly what somebody reading this wants next.
			if strings.Contains(bad.Says, "not running") {
				continue
			}
		}

		// A digest comparison is only meaningful for something that is up, and only
		// when both sides were observed: a collector that could not read one of them
		// reports neither, and silence is not a match.
		pinned, havePinned := o.Pinned[service]
		running, haveRunning := o.Running[service]
		if !havePinned || !haveRunning {
			continue
		}
		if pinned != running {
			out = append(out, Divergence{service, fmt.Sprintf(
				"is running %s and the deploy has %s pinned — the next deploy would change it back",
				shortDigest(running), shortDigest(pinned))})
		}
	}

	return append(out, strangers(is, o)...)
}

// DeclaredIn reads a checkout and says what should be running on a server deployed
// from it.
//
// Three sources, because this repository declares a container in three ways and a
// survey that knew only one would report the other two as strangers. The services are
// `services/*/`, the same glob scripts/deploy.sh walks. Each service's addons are
// `<service>-<need>`, derived from its manifest's `needs:` — that is the container
// name, the volume name and the compose service, all of it carrying the service's own
// name in front so two services needing a database get two and never one shared. And
// the edge is whatever the top-level compose file declares, read rather than assumed:
// `caddy` is deliberately given no manifest (scripts/check-layout.sh says why), so
// hardcoding its name here would be this file holding a copy of a fact that lives
// somewhere else.
func DeclaredIn(root string) (Declared, error) {
	var d Declared

	edge, err := os.ReadFile(filepath.Join(root, "docker-compose.yml"))
	if err != nil {
		return d, err
	}
	var compose struct {
		Services map[string]struct{} `yaml:"services"`
	}
	if err := yaml.Unmarshal(edge, &compose); err != nil {
		return d, fmt.Errorf("docker-compose.yml: %w", err)
	}
	for name := range compose.Services {
		d.Others = append(d.Others, name)
	}

	found, err := filepath.Glob(filepath.Join(root, "services", "*", "japode.yml"))
	if err != nil {
		return d, err
	}
	if len(found) == 0 {
		return d, fmt.Errorf("no services/*/japode.yml under %s", root)
	}
	for _, path := range found {
		service := filepath.Base(filepath.Dir(path))
		d.Services = append(d.Services, service)

		// A manifest that will not parse is `jcloud check`'s to report, and it has
		// its own step in CI. Here it costs the addons of one service, which is
		// named as unparsed rather than passed over.
		data, err := os.ReadFile(path)
		if err != nil {
			return d, err
		}
		m, err := Parse(data)
		if err != nil {
			return d, fmt.Errorf("%s: %w", path, err)
		}
		for _, need := range m.Needs {
			d.Others = append(d.Others, service+"-"+need.Name)
		}
	}

	sort.Strings(d.Services)
	sort.Strings(d.Others)
	return d, nil
}

// survey is the command.
func survey(args []string) int {
	root := "."
	observations := ""
	for len(args) > 0 {
		switch args[0] {
		case "--root":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "jcloud survey: --root takes a path")
				return 2
			}
			root, args = args[1], args[2:]
		default:
			if observations != "" {
				fmt.Fprintln(os.Stderr, "jcloud survey [--root <dir>] <observations>")
				return 2
			}
			observations, args = args[0], args[1:]
		}
	}
	if observations == "" {
		fmt.Fprintln(os.Stderr, "jcloud survey [--root <dir>] <observations>\n\nThe observations are gathered by scripts/survey-server.sh, which is what holds the\nSSH credential — this build reaches no network (JC96).")
		return 2
	}

	body, err := os.ReadFile(observations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud survey: %v\n", err)
		return 2
	}
	o := ParseObserved(string(body))
	// An empty file is refused and an empty *server* is not, which is a distinction
	// the first draft got wrong: it read "no containers" as a collector that never
	// reached the host, and so refused to survey a machine that had been bootstrapped
	// and not yet deployed to — a real state, and one worth a real answer.
	//
	// It is safe to be this permissive here because the failure is loud: a server
	// running nothing reports every declared service as not running, which is red.
	// The silence JC66 warns about would be the opposite, and cannot arise — and the
	// collector refuses an empty answer from the host before it ever writes this file.
	if strings.TrimSpace(string(body)) == "" {
		fmt.Fprintf(os.Stderr, "jcloud survey: %s is empty — that is a collector that gathered nothing, not a server in agreement\n", observations)
		return 2
	}

	d, err := DeclaredIn(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jcloud survey: %v\n", err)
		return 2
	}

	divergences := SurveyAgainst(d, o)
	if len(divergences) == 0 {
		fmt.Printf("  \033[32m✓\033[0m %d service(s) on the digest pinned for each, and %d other container(s) this repository declares\n",
			len(d.Services), len(d.Others))
		fmt.Printf("\n\033[32mthe server is what this repository declares\033[0m\n")
		return 0
	}

	for _, d := range divergences {
		fmt.Fprintf(os.Stderr, "  \033[31m✗\033[0m %s %s\n", d.Service, d.Says)
		fmt.Printf("::error::%s %s\n", d.Service, d.Says)
	}
	fmt.Fprintf(os.Stderr, "\n\033[31m%d divergence(s) between this repository and the server\033[0m\n", len(divergences))
	return 1
}
