// The route half of `agree` for a platform whose edge is nginx (JCT3).
//
// The Caddy half reads `caddy adapt` JSON rather than the Caddyfile, so that a
// commented-out block cannot pass (JC33). The nginx equivalent is `nginx -T`: the
// whole configuration with every included file printed after the one including
// it, run by the platform's own script in a pinned nginx image. jcloud reads that
// output and never runs nginx, the same division `--adapted` already makes.
//
// What it reads, and where it ends. nginx's grammar is small enough to tokenise
// exactly: words, quoted strings, `;`, `{`, `}` and `#` comments, so a comment
// cannot fake a directive here either. Its *semantics* are not re-implemented
// beyond what the comparison needs:
//
//   - A route is the `server` block whose server_name lists the host, and inside it
//     the `location /` block, or the server itself when it has none. Narrower
//     locations are not compared: a manifest route has one upstream, and the
//     catch-all is where it goes.
//   - Values inherit the way nginx inherits them: location over server over http.
//     `proxy_set_header` is inherited as a whole list, only when the level below
//     sets none. `nginx -T` prints included files flat, so a server from conf.d
//     has no http{} around it in the text; every directive outside a server block
//     is read as the http level.
//   - `proxy_pass http://$var:port` resolves `$var` through `set $var value;` in
//     the same scopes, which is how a platform lets nginx boot before an upstream
//     container exists. An `upstream` block name resolves to its servers.
//   - `client_max_body_size` uses nginx's units, powers of 1024, and defaults to
//     1m when nothing sets it.
//   - `proxy_set_header Name ""` removes a header, which is what Caddy's
//     `header_up -Name` means.
//   - Open-source nginx has no active health check, so health_uri and its interval
//     are not compared. That is said by the absence of the comparison and not
//     reported as a fault: the manifest's health block is still checked against
//     the compose healthcheck the deploy waits on.
//   - An `auth_request` on the route, inherited like everything else, is its gate,
//     compared with the route's `gate:` (JCT18) both ways.
package main

import (
	"fmt"
	"strconv"
	"strings"
)

// ngxDirective is one directive: its name, its arguments, and its block when it
// has one. Block is nil for a directive ended by `;`.
type ngxDirective struct {
	Name  string
	Args  []string
	Block []*ngxDirective
}

// ngxTokens splits nginx configuration text into words and the three punctuation
// tokens. Quoted strings arrive unquoted, so `""` is an empty word and not two
// quote characters.
func ngxTokens(text string) ([]string, error) {
	var out []string
	i := 0
	for i < len(text) {
		c := text[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '#':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '{' || c == '}' || c == ';':
			out = append(out, string(c))
			i++
		case c == '"' || c == '\'':
			var b strings.Builder
			j := i + 1
			for ; j < len(text) && text[j] != c; j++ {
				if text[j] == '\\' && j+1 < len(text) {
					j++
				}
				b.WriteByte(text[j])
			}
			if j >= len(text) {
				return nil, fmt.Errorf("a %c-quoted string is never closed", c)
			}
			out = append(out, b.String())
			i = j + 1
		default:
			j := i
			for j < len(text) {
				d := text[j]
				if d == ' ' || d == '\t' || d == '\n' || d == '\r' || d == ';' || d == '{' || d == '}' {
					// `${name}` is a variable and not a block.
					if d == '{' && j > i && text[j-1] == '$' {
						if end := strings.IndexByte(text[j:], '}'); end >= 0 {
							j += end + 1
							continue
						}
					}
					break
				}
				j++
			}
			out = append(out, text[i:j])
			i = j
		}
	}
	return out, nil
}

// parseNginx reads `nginx -T` output into a directive tree.
func parseNginx(data []byte) ([]*ngxDirective, error) {
	tokens, err := ngxTokens(string(data))
	if err != nil {
		return nil, fmt.Errorf("not an `nginx -T` output: %w", err)
	}
	pos := 0
	var block func(depth int) ([]*ngxDirective, error)
	block = func(depth int) ([]*ngxDirective, error) {
		var out []*ngxDirective
		var cur *ngxDirective
		for pos < len(tokens) {
			t := tokens[pos]
			pos++
			switch t {
			case ";":
				if cur == nil {
					return nil, fmt.Errorf("not an `nginx -T` output: a `;` ends no directive")
				}
				out, cur = append(out, cur), nil
			case "{":
				if cur == nil {
					return nil, fmt.Errorf("not an `nginx -T` output: a `{` opens no directive")
				}
				inner, err := block(depth + 1)
				if err != nil {
					return nil, err
				}
				cur.Block = append([]*ngxDirective{}, inner...)
				out, cur = append(out, cur), nil
			case "}":
				if depth == 0 {
					return nil, fmt.Errorf("not an `nginx -T` output: a `}` closes nothing")
				}
				if cur != nil {
					return nil, fmt.Errorf("not an `nginx -T` output: %s is never ended", cur.Name)
				}
				return out, nil
			default:
				if cur == nil {
					cur = &ngxDirective{Name: t}
				} else {
					cur.Args = append(cur.Args, t)
				}
			}
		}
		if depth > 0 {
			return nil, fmt.Errorf("not an `nginx -T` output: a block is never closed")
		}
		if cur != nil {
			return nil, fmt.Errorf("not an `nginx -T` output: %s is never ended", cur.Name)
		}
		return out, nil
	}
	return block(0)
}

// nginx's own placeholders for what the manifest writes in the Caddyfile's.
var ngxPlaceholders = strings.NewReplacer(
	"{remote_host}", "$remote_addr",
	"{host}", "$host",
	"{scheme}", "$scheme",
)

// parseNginxRoutes answers what each host's route proxies to, keyed by host the
// way parseAdapted is, so the comparison after it is one function for both kinds.
func parseNginxRoutes(data []byte) (map[string]*proxy, error) {
	tree, err := parseNginx(data)
	if err != nil {
		return nil, err
	}

	// The http level: everything outside a server block, at the top or under http{}.
	// upstream blocks are collected from the same places.
	var httpLevel []*ngxDirective
	var servers []*ngxDirective
	upstreams := map[string][]string{}
	var collect func(ds []*ngxDirective)
	collect = func(ds []*ngxDirective) {
		for _, d := range ds {
			switch {
			case d.Name == "server" && d.Block != nil:
				servers = append(servers, d)
			case d.Name == "upstream" && d.Block != nil && len(d.Args) == 1:
				for _, s := range d.Block {
					if s.Name == "server" && len(s.Args) > 0 {
						upstreams[d.Args[0]] = append(upstreams[d.Args[0]], s.Args[0])
					}
				}
			case d.Name == "http" && d.Block != nil:
				collect(d.Block)
			case d.Block == nil:
				httpLevel = append(httpLevel, d)
			}
		}
	}
	collect(tree)

	byHost := map[string]*proxy{}
	for _, srv := range servers {
		var names []string
		for _, d := range srv.Block {
			if d.Name == "server_name" {
				names = append(names, d.Args...)
			}
		}
		px := nginxProxy(httpLevel, srv.Block, upstreams)
		for _, h := range names {
			// The first server naming a host is the one nginx picks, as Caddy's
			// first route is.
			if _, taken := byHost[h]; !taken {
				byHost[h] = px
			}
		}
	}
	return byHost, nil
}

// nginxProxy flattens one server's catch-all route into the shape the comparison
// reads.
func nginxProxy(httpLevel, server []*ngxDirective, upstreams map[string][]string) *proxy {
	var loc []*ngxDirective
	for _, d := range server {
		if d.Name == "location" && d.Block != nil && len(d.Args) == 1 && d.Args[0] == "/" {
			loc = d.Block
			break
		}
	}
	// Innermost first: the first scope that sets a directive is the one that wins.
	scopes := [][]*ngxDirective{loc, server, httpLevel}

	px := &proxy{SetHeader: map[string][]string{}, noHealth: true, placeholders: ngxPlaceholders, MaxSize: 1 << 20}

	first := func(name string) *ngxDirective {
		for _, s := range scopes {
			for _, d := range s {
				if d.Name == name && d.Block == nil {
					return d
				}
			}
		}
		return nil
	}

	if d := first("client_max_body_size"); d != nil && len(d.Args) == 1 {
		if n, ok := ngxSize(d.Args[0]); ok {
			px.MaxSize = n
		}
	}

	// proxy_set_header is inherited as a list, and only by a level that sets none.
	for _, s := range scopes {
		sets := false
		for _, d := range s {
			if d.Name != "proxy_set_header" || d.Block != nil || len(d.Args) < 1 {
				continue
			}
			sets = true
			value := strings.Join(d.Args[1:], " ")
			if value == "" {
				px.DelHeader = append(px.DelHeader, d.Args[0])
				continue
			}
			px.SetHeader[d.Args[0]] = append(px.SetHeader[d.Args[0]], value)
		}
		if sets {
			break
		}
	}

	if d := first("proxy_pass"); d != nil && len(d.Args) == 1 {
		px.found = true
		target := d.Args[0]
		target = strings.TrimPrefix(strings.TrimPrefix(target, "http://"), "https://")
		if slash := strings.IndexByte(target, '/'); slash >= 0 {
			target = target[:slash]
		}
		target = ngxExpand(target, scopes)
		if servers, ok := upstreams[target]; ok {
			px.Upstreams = append(px.Upstreams, servers...)
		} else {
			if !strings.Contains(target, ":") {
				target += ":80"
			}
			px.Upstreams = append(px.Upstreams, target)
		}
	}

	if d := first("auth_request"); d != nil && len(d.Args) == 1 && d.Args[0] != "off" {
		px.Gate = d.Args[0]
	}
	return px
}

// ngxExpand resolves each `$name` or `${name}` through the `set` that names it,
// innermost scope first. A variable nothing sets stays as written, and then fails
// the upstream comparison by name, which says what went unresolved.
func ngxExpand(s string, scopes [][]*ngxDirective) string {
	value := func(name string) (string, bool) {
		for _, sc := range scopes {
			for _, d := range sc {
				if d.Name == "set" && len(d.Args) == 2 && d.Args[0] == "$"+name {
					return d.Args[1], true
				}
			}
		}
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j, name := i+1, ""
		if j < len(s) && s[j] == '{' {
			end := strings.IndexByte(s[j:], '}')
			if end < 0 {
				b.WriteString(s[i:])
				break
			}
			name, j = s[j+1:j+end], j+end+1
		} else {
			for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
				j++
			}
			name = s[i+1 : j]
		}
		if v, ok := value(name); ok {
			b.WriteString(v)
		} else {
			b.WriteString(s[i:j])
		}
		i = j
	}
	return b.String()
}

// ngxSize reads nginx's size syntax: a number, optionally k, m or g in either
// case, each a power of 1024.
func ngxSize(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	factor := int64(1)
	switch strings.ToLower(s[len(s)-1:]) {
	case "k":
		factor = 1 << 10
	case "m":
		factor = 1 << 20
	case "g":
		factor = 1 << 30
	}
	if factor != 1 {
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n * factor, true
}
