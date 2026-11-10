// The route half of `agree`, asked of Caddy's own parse instead of of the text.
//
// JC33: comparing substrings meant a commented-out route still counted. Measured —
// prefixing skillgist's second block with `#` left one live `reverse_proxy` and the
// check still found two, and passed. Every substring assertion had the same hole.
//
// Stripping comments before comparing would close the measured case and open a
// smaller one, because `#` inside a quoted string is not a comment and Caddy allows
// those. So the grammar is not re-implemented here at all: `caddy adapt` hands back
// the JSON configuration Caddy would actually run, comments already gone, defaults
// already applied, and this file asserts against that. The tool that owns the
// grammar answers the question about it.
//
// What stays textual is what is about the *source* rather than the meaning: whether
// an address block reads `{$SVC_HOSTS:…}` is a fact about the file, and the adapted
// JSON has already resolved it away.
package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Only the shape this asserts against. Caddy's config is far larger; decoding the
// parts nobody reads would be a second copy of its schema to keep in step.
type adapted struct {
	Apps struct {
		HTTP struct {
			Servers map[string]struct {
				Routes []adaptedRoute `json:"routes"`
			} `json:"servers"`
		} `json:"http"`
	} `json:"apps"`
}

type adaptedRoute struct {
	Match []struct {
		Host []string `json:"host"`
	} `json:"match"`
	Handle []json.RawMessage `json:"handle"`
}

// proxy is what one route's reverse_proxy handler says, flattened out of the
// subroute nesting Caddy produces — or, for an nginx edge, out of the server and
// location blocks route_nginx.go reads (JCT3).
type proxy struct {
	Upstreams      []string
	HealthURI      string
	HealthInterval int64
	MaxSize        int64
	SetHeader      map[string][]string
	DelHeader      []string
	found          bool

	// The authorisation subrequest the route makes before proxying (JCT18):
	// nginx's `auth_request` URI, or the rewrite URI of Caddy's `forward_auth`.
	Gate string

	// What differs by kind. noHealth is an edge with no active health check to
	// compare; placeholders turns the manifest's Caddyfile placeholders into the
	// edge's own spelling (nil keeps Caddy's adapted one).
	noHealth     bool
	placeholders *strings.Replacer
}

func parseAdapted(data []byte) (map[string]*proxy, error) {
	var a adapted
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("not a `caddy adapt` output: %w", err)
	}
	// Keyed by host, because that is what a manifest route names. One host is
	// matched by one route — `agree` already refuses a manifest that claims a host
	// twice, and Caddy would take the first anyway.
	byHost := map[string]*proxy{}
	for _, server := range a.Apps.HTTP.Servers {
		for _, r := range server.Routes {
			p := &proxy{SetHeader: map[string][]string{}}
			walk(r.Handle, p)
			for _, m := range r.Match {
				for _, h := range m.Host {
					byHost[h] = p
				}
			}
		}
	}
	return byHost, nil
}

// Caddy wraps a site's directives in a `subroute` handler, and may nest further.
// Rather than model that, walk everything and pick up the handlers by name.
func walk(raw []json.RawMessage, p *proxy) {
	for _, item := range raw {
		var h struct {
			Handler string `json:"handler"`
			MaxSize int64  `json:"max_size"`
			Headers struct {
				Request struct {
					Set    map[string][]string `json:"set"`
					Delete []string            `json:"delete"`
				} `json:"request"`
			} `json:"headers"`
			HealthChecks struct {
				Active struct {
					URI      string `json:"uri"`
					Interval int64  `json:"interval"`
				} `json:"active"`
			} `json:"health_checks"`
			Upstreams []struct {
				Dial string `json:"dial"`
			} `json:"upstreams"`
			Routes []struct {
				Handle []json.RawMessage `json:"handle"`
			} `json:"routes"`
			Rewrite struct {
				URI string `json:"uri"`
			} `json:"rewrite"`
			HandleResponse []json.RawMessage `json:"handle_response"`
		}
		if json.Unmarshal(item, &h) != nil {
			continue
		}
		switch {
		// `forward_auth` adapts to a reverse_proxy of its own, rewritten to the
		// gate's URI and answering through handle_response (JCT18). Its upstream
		// and headers are the gate's, so they are kept out of the route's.
		case h.Handler == "reverse_proxy" && h.Rewrite.URI != "" && len(h.HandleResponse) > 0:
			p.Gate = h.Rewrite.URI
			continue
		case h.Handler == "reverse_proxy":
			p.found = true
			for _, u := range h.Upstreams {
				p.Upstreams = append(p.Upstreams, u.Dial)
			}
			p.HealthURI = h.HealthChecks.Active.URI
			p.HealthInterval = h.HealthChecks.Active.Interval
			for k, v := range h.Headers.Request.Set {
				p.SetHeader[k] = v
			}
			p.DelHeader = append(p.DelHeader, h.Headers.Request.Delete...)
		case h.Handler == "request_body":
			p.MaxSize = h.MaxSize
		}
		for _, nested := range h.Routes {
			walk(nested.Handle, p)
		}
	}
}

// agreeRouteJSON is agreeRoute's replacement wherever the adapted config is
// available: every assertion below is about what Caddy would run.
func (m *Manifest) agreeRouteJSON(at string, byHost map[string]*proxy) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	want := fmt.Sprintf("%s:%d", m.Name, m.Port)
	for i, r := range m.Routes {
		at := fmt.Sprintf("%s routes[%d]", at, i)
		if len(r.Hosts) == 0 {
			continue // already reported by Validate
		}

		// Every host the manifest declares has to reach the same block. A route
		// commented out is a host the adapted config simply does not carry, which
		// is the whole reason this reads JSON.
		var px *proxy
		for _, h := range r.Hosts {
			got, ok := byHost[h]
			if !ok {
				bad("%s: nothing in the route configuration answers for %s — is that block commented out?", at, h)
				continue
			}
			if px != nil && px != got {
				bad("%s: %s is answered by a different block than the manifest's other hosts for this route", at, h)
			}
			px = got
		}
		if px == nil {
			continue
		}
		if !px.found {
			bad("%s: the block answering for %s proxies to nothing", at, r.Hosts[0])
			continue
		}

		if len(px.Upstreams) != 1 || px.Upstreams[0] != want {
			bad("%s: proxies to %v, and the manifest declares %s", at, px.Upstreams, want)
		}
		if !px.noHealth {
			p = append(p, px.agreeHealth(at, m)...)
		}
		if r.MaxBody != "" {
			switch n, err := bytesOf(r.MaxBody); {
			case err != nil:
				bad("%s: %v", at, err)
			case px.MaxSize != n:
				bad("%s: max_size is %d bytes, and the manifest declares %s (%d)", at, px.MaxSize, r.MaxBody, n)
			}
		}
		p = append(p, px.agreeProxy(at, r.Proxy)...)

		// The gate both ways (JCT18). One the route runs and the manifest does not
		// state is a check nobody declared; one the manifest states and the route
		// does not run is a route open to anyone, which is the worse of the two.
		switch {
		case px.Gate == r.Gate:
		case r.Gate == "":
			bad("%s: the route gates every request through %s and the manifest states no gate: — add gate: %s", at, px.Gate, px.Gate)
		case px.Gate == "":
			bad("%s: the manifest declares gate: %s and the route runs none, so it is open to anyone", at, r.Gate)
		default:
			bad("%s: the route gates through %s and the manifest declares gate: %s", at, px.Gate, r.Gate)
		}
	}
	return p
}

// agreeHealth compares the edge's active health check, which Caddy has and
// open-source nginx does not.
func (px *proxy) agreeHealth(at string, m *Manifest) []string {
	var p []string
	if px.HealthURI != m.Health.Path {
		p = append(p, fmt.Sprintf("%s: health_uri is %q, and the manifest declares %q — the edge would take an upstream out of rotation on a different path than the deploy waits on",
			at, px.HealthURI, m.Health.Path))
	}
	// Caddy stores the interval in nanoseconds. Compared because it was in the
	// route and in no schema, so nothing noticed it changing.
	if want, err := nanosOf(m.Health.interval()); err != nil {
		p = append(p, fmt.Sprintf("%s: %v", at, err))
	} else if px.HealthInterval != want {
		p = append(p, fmt.Sprintf("%s: health_interval is %s, and the manifest declares %s",
			at, humanNanos(px.HealthInterval), m.Health.interval()))
	}
	return p
}

// The manifest states the proxy body as Caddyfile directives, and the adapted
// config states what they mean. Only `header_up` is translated, because it is the
// only one any manifest uses and the only one that is load-bearing here.
//
// Anything else is reported rather than skipped: a directive this cannot verify,
// passed over in silence, is the same false negative JC33 is about.
func (px *proxy) agreeProxy(at string, directives []string) []string {
	var p []string
	bad := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }

	// The other direction, which this did not have: a header manipulation in the
	// block that the manifest does not declare. Measured before it existed — an
	// injected `header_up X-Secret-Admin true` passed, and the only thing that
	// caught it was the upstream comparison, whose job exits 0 with no token.
	//
	// Built from the manifest's own list rather than from a set of names allowed
	// here, so it stays right as the list changes. `health_uri` and
	// `health_interval` are not in it and are not meant to be: they are derived
	// from `health:` and compared above, which is why this looks at headers alone.
	declaredSet := map[string]bool{}
	declaredDel := map[string]bool{}
	for _, d := range directives {
		f := strings.Fields(d)
		if len(f) < 2 || f[0] != "header_up" {
			continue
		}
		if name := strings.TrimPrefix(f[1], "-"); strings.HasPrefix(f[1], "-") {
			declaredDel[name] = true
		} else {
			declaredSet[name] = true
		}
	}
	for name := range px.SetHeader {
		if !declaredSet[name] {
			bad("%s: the route sets %s and the manifest does not declare it — a header nobody declared is one nobody reviewed", at, name)
		}
	}
	for _, name := range px.DelHeader {
		if !declaredDel[name] {
			bad("%s: the route strips %s and the manifest does not declare it", at, name)
		}
	}

	for _, d := range directives {
		f := strings.Fields(d)
		if len(f) < 2 || f[0] != "header_up" {
			bad("%s: %q is not a directive this can check against the adapted config — teach agreeProxy in tools/jcloud/route_json.go, or it is unverified", at, d)
			continue
		}
		name := f[1]
		if strings.HasPrefix(name, "-") {
			name = strings.TrimPrefix(name, "-")
			if !contains(px.DelHeader, name) {
				bad("%s: the route does not strip %s, which the manifest declares", at, name)
			}
			continue
		}
		values, ok := px.SetHeader[name]
		if !ok {
			bad("%s: the route does not set %s, which the manifest declares", at, name)
			continue
		}
		// The Caddyfile placeholder and the edge's are two spellings of one
		// thing, and the manifest is written in the Caddyfile's.
		spell := px.placeholders
		if spell == nil {
			spell = caddyPlaceholders
		}
		want := spell.Replace(strings.Join(f[2:], " "))
		if !contains(values, want) {
			bad("%s: %s is set to %v, and the manifest declares %s", at, name, values, want)
		}
	}
	return p
}

// Caddy's adapted spelling of the one placeholder any manifest uses.
var caddyPlaceholders = strings.NewReplacer("{remote_host}", "{http.request.remote.host}")

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// Caddy's own units: the decimal ones are powers of 1000 and the binary ones
// powers of 1024, which is why `8MB` adapts to 8000000 and not 8388608.
var units = []struct {
	suffix string
	factor int64
}{
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30},
	{"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"B", 1},
}

// Caddy stores a duration as nanoseconds in the adapted config. Parsed with the
// same grammar the schema validates, so `15s` here and `15s` there are one fact.
func nanosOf(d string) (int64, error) {
	if !durationRE.MatchString(d) {
		return 0, fmt.Errorf("health.interval %q is not a duration", d)
	}
	var total int64
	for _, part := range regexp.MustCompile(`[0-9]+(ms|s|m|h)`).FindAllStringSubmatch(d, -1) {
		n, err := strconv.ParseInt(strings.TrimSuffix(part[0], part[1]), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("health.interval %q: %w", d, err)
		}
		switch part[1] {
		case "ms":
			total += n * int64(time.Millisecond)
		case "s":
			total += n * int64(time.Second)
		case "m":
			total += n * int64(time.Minute)
		case "h":
			total += n * int64(time.Hour)
		}
	}
	return total, nil
}

func humanNanos(n int64) string { return time.Duration(n).String() }

func bytesOf(size string) (int64, error) {
	for _, u := range units {
		if !strings.HasSuffix(size, u.suffix) {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(size, u.suffix), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("max_body %q: %w", size, err)
		}
		return n * u.factor, nil
	}
	return 0, fmt.Errorf("max_body %q has no unit this understands", size)
}
