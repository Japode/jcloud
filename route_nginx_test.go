package main

import (
	"reflect"
	"strings"
	"testing"
)

// JCT3: an nginx edge, in the shape `nginx -T` prints it — the main file first,
// every included file after it under its own comment, so the server block sits at
// the top level with no http{} around it. The upstream is resolved at request time
// through a variable, the way a platform lets nginx boot before the container is up.
const fixtureNginx = `# configuration file /etc/nginx/nginx.conf:
http {
    client_max_body_size 1m;
    proxy_set_header X-From-Http yes;
    include /etc/nginx/conf.d/*.conf;
}

# configuration file /etc/nginx/conf.d/default.conf:
server {
    listen 80;
    server_name _;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name paste.example.com;   # the route the manifest declares
    client_max_body_size 4m;

    location = /robots.txt {
        root /usr/share/nginx/html;
    }

    location / {
        resolver 127.0.0.11 valid=30s ipv6=off;
        set $paste_upstream paste;
        proxy_pass http://$paste_upstream:8080;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-View-Id "";
    }
}
`

const nginxProfile = "route:\n  path: nginx/default.conf\n  kind: nginx\n"

// The Japode fixture under an nginx edge: no Caddy route file at all, and the
// body limit in a unit nginx can state (its m is a MiB).
func nginxTree(t *testing.T) string {
	t.Helper()
	return layTree(t, map[string]string{
		"jcloud.yml":                        nginxProfile,
		"services/paste/japode.yml":         strings.Replace(fixtureManifest, "max_body: 4MB", "max_body: 4MiB", 1),
		"services/paste/docker-compose.yml": fixtureCompose,
		"services/paste/backup.conf":        fixtureBackupConf,
		".env.example":                      nginxEnvExample,
	})
}

// No PASTE_HOSTS: the hosts are in server_name, and nothing reads the variable.
const nginxEnvExample = "PASTE_SITE_URL=https://paste.example.com\n"

// Under an nginx edge the hosts key is neither required nor reserved: a
// PASTE_HOSTS= left in .env.example is a key nothing reads, and `up` does not ask
// the secret for it.
func TestNginxNeedsNoHostsKey(t *testing.T) {
	prof, err := ParseProfile([]byte(nginxProfile))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse([]byte(fixtureManifest))
	if err != nil {
		t.Fatal(err)
	}
	required, _ := RequiredEnv(prof, m)
	if !reflect.DeepEqual(required, []string{"PASTE_SITE_URL"}) {
		t.Errorf("required: got %v", required)
	}
	p := strings.Join(m.agreeEnvExample(prof, nil, ".env.example", fixtureEnvExample), "\n")
	if !strings.Contains(p, "PASTE_HOSTS= is not declared by the manifest") {
		t.Errorf("a hosts key under nginx should read as an orphan, got:\n%s", p)
	}
}

func agreeNginx(t *testing.T, conf string) []string {
	t.Helper()
	m, err := Parse([]byte(strings.Replace(fixtureManifest, "max_body: 4MB", "max_body: 4MiB", 1)))
	if err != nil {
		t.Fatal(err)
	}
	return m.AgreeWith(nginxTree(t), []byte(conf))
}

func TestNginxFixtureAgreesWithItself(t *testing.T) {
	if p := agreeNginx(t, fixtureNginx); len(p) != 0 {
		t.Fatalf("the nginx fixture should agree, got:\n  %s", strings.Join(p, "\n  "))
	}
}

// Without the nginx -T output the route is unchecked and said so, as with Caddy.
func TestNginxUnsuppliedIsReported(t *testing.T) {
	p := strings.Join(agreeNginx(t, ""), "\n")
	if !strings.Contains(p, "the nginx configuration was not supplied") || !strings.Contains(p, "nginx -T") {
		t.Fatalf("got:\n%s", p)
	}
}

// One case per fact the route carries, each a mistake somebody makes in nginx.
func TestNginxDisagreements(t *testing.T) {
	cases := []struct{ name, old, new, want string }{
		{"commented out", "server_name paste.example.com;", "# server_name paste.example.com;",
			"nothing in the route configuration answers for paste.example.com"},
		{"another port", "$paste_upstream:8080", "$paste_upstream:8081",
			"proxies to [paste:8081]"},
		{"the variable names another service", "set $paste_upstream paste;", "set $paste_upstream pastebin;",
			"proxies to [pastebin:8080]"},
		{"body limit drifted", "client_max_body_size 4m;", "client_max_body_size 8m;",
			"max_size is 8388608 bytes"},
		{"a header nobody declared", "proxy_set_header X-View-Id \"\";", "proxy_set_header X-View-Id \"\";\n        proxy_set_header X-Secret-Admin true;",
			"the route sets X-Secret-Admin"},
		{"the declared header went", "proxy_set_header X-Forwarded-For $remote_addr;", "",
			"the route does not set X-Forwarded-For"},
		{"the strip became a set", "proxy_set_header X-View-Id \"\";", "proxy_set_header X-View-Id 1;",
			"the route does not strip X-View-Id"},
		{"an auth gate", "set $paste_upstream paste;", "set $paste_upstream paste;\n        auth_request /oauth2/auth;",
			"the route gates every request through /oauth2/auth and the manifest states no gate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conf := strings.Replace(fixtureNginx, c.old, c.new, 1)
			if conf == fixtureNginx {
				t.Fatal("the patch changed nothing — the fixture moved under this case")
			}
			p := strings.Join(agreeNginx(t, conf), "\n")
			if !strings.Contains(p, c.want) {
				t.Fatalf("want %q, got:\n%s", c.want, p)
			}
		})
	}
}

// nginx inherits values outward in, and proxy_set_header only as a whole list:
// a location that sets one header drops every header the server set.
func TestNginxInheritance(t *testing.T) {
	routes, err := parseNginxRoutes([]byte(`
client_max_body_size 2m;
server {
    server_name a.example;
    proxy_set_header X-Server 1;
    location / { proxy_pass http://a:1; }
}
server {
    server_name b.example;
    proxy_set_header X-Server 1;
    location / { proxy_pass http://b:2; proxy_set_header X-Location 1; client_max_body_size 0; }
}
server {
    server_name c.example;
    location / { proxy_pass http://c; }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := routes["a.example"], routes["b.example"], routes["c.example"]
	if _, ok := a.SetHeader["X-Server"]; !ok || a.MaxSize != 2<<20 {
		t.Errorf("a should inherit the server's header and the http body limit: %+v", a)
	}
	if _, ok := b.SetHeader["X-Server"]; ok || b.MaxSize != 0 {
		t.Errorf("b's own list replaces the server's, and its limit is its own: %+v", b)
	}
	if !reflect.DeepEqual(c.Upstreams, []string{"c:80"}) {
		t.Errorf("a proxy_pass with no port dials 80: %v", c.Upstreams)
	}
}

// An upstream block name resolves to its servers, and a server with no location
// block proxies from the server level.
func TestNginxUpstreamBlocks(t *testing.T) {
	routes, err := parseNginxRoutes([]byte(`
upstream pool { server paste:8080; }
server { server_name p.example; proxy_pass http://pool/; }
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := routes["p.example"].Upstreams; !reflect.DeepEqual(got, []string{"paste:8080"}) {
		t.Fatalf("got %v", got)
	}
}

// The tokenizer is exact about what nginx is exact about: a `#` inside quotes is
// not a comment, `${var}` is not a block, and a configuration that does not close
// is refused rather than read as far as it goes.
func TestNginxTokens(t *testing.T) {
	routes, err := parseNginxRoutes([]byte(`
server {
    server_name q.example;
    set $up paste;
    location / { proxy_pass http://${up}:8080; proxy_set_header X-Tag "a # b"; }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	q := routes["q.example"]
	if !reflect.DeepEqual(q.Upstreams, []string{"paste:8080"}) || !reflect.DeepEqual(q.SetHeader["X-Tag"], []string{"a # b"}) {
		t.Fatalf("got %+v", q)
	}

	for _, broken := range []string{"server {", "server { listen 80 }", "}", "server_name \"x;"} {
		if _, err := parseNginxRoutes([]byte(broken)); err == nil {
			t.Errorf("%q should be refused", broken)
		}
	}
}

// An nginx profile names one configuration for every host, so its path needs no
// {name}; a Caddy one still does.
func TestNginxRoutePathNeedsNoName(t *testing.T) {
	if _, err := ParseProfile([]byte(nginxProfile)); err != nil {
		t.Fatalf("an nginx route path without {name} should be accepted: %v", err)
	}
	if _, err := ParseProfile([]byte("route:\n  path: caddy/Caddyfile\n")); err == nil {
		t.Fatal("a caddy route path without {name} should still be refused")
	}
}
