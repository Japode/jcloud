# Improvements

## Block A

## Block B — A second platform: jcloud checks a tree that is not the Japode one

### §JCT10 A second platform in the contract tests

The harness landed: platform_test.go reads JCLOUD_PLATFORM as a list, runs every
TestCommitted test once per tree through that tree's jcloud.yml, keeps the tests about
Japode's own scripts to a tree with no jcloud.yml, and ci/check.sh mounts each tree in
`JCLOUD_PLATFORM=../cloud:../../viglet/cloud` and skips an absent one.

What is left is jcloud's own CI. .github/workflows/ci.yml checks out no platform, so
there every contract test skips. Japode/cloud and openviglet/cloud are both private, so
checking them out needs a read-only token as a repository secret (a fine-grained PAT
with contents:read on the two, or a GitHub App installed on both orgs). With it, ci.yml
gains two `actions/checkout` steps into sibling paths and passes their list to
ci/check.sh. Which credential, and who owns it, is the user's decision.

Before Viglet Cloud's tree is useful there it needs its own adoption, filed in its own
roadmap: a jcloud.yml, manifests for turing, shio and dumont, its check script writing
.deploy-state/nginx-T.txt and .deploy-state/compose-config.json (platform_test.go names
them), and JCT18 and JCT20 shipped here. Until it carries a jcloud.yml it is read as the
Japode layout and fails the Japode-script tests, which is the right answer for a tree
that has not said what it is.

### §JCT18 A route states its auth gate

Since JCT3, agree reads an nginx route and reports an `auth_request` on it as a
directive the manifest has no field for (route_nginx.go, proxy.Unstated). That report
can never be cleared. On Viglet Cloud every per-client host in clients.nginx.conf runs
`auth_request` to client-gate, and admin.viglet.cloud gates through oauth2-proxy, so
those routes stay red whatever their manifest says.

A route gains `gate:`, the URI of the authorisation subrequest (`/oauth2/auth`). Under
nginx it is compared with the `auth_request` in force on the route's `location /`,
inherited from the server like every other value there. Under Caddy it is compared with
`forward_auth`, whose adapted form is a reverse_proxy handler with a rewrite to that
URI. An absent `gate:` with a gate on the route stays the report it is today, and a
declared gate the route does not run is a new one: the route is open to anyone.

Which upstream answers the subrequest is not in the field. It is another route's
business, and one that route's own manifest states.

### §JCT19 One compose path: the model

Since JCT4, `agree --compose` reads the model `docker compose --profile '*' config
--no-interpolate --format json` prints, and without it falls back to agreeCompose and
agreeNeeds over services/<name>/docker-compose.yml as text (compose_model.go). The
Japode platform's scripts/check-jcloud.sh passes only `--adapted`, so its services still
go through the substring path, which passes on a commented-out line.

Measured on 2026-11-10: the model built from the top-level docker-compose.yml plus every
services/*/docker-compose.yml, joined with `-f` the way scripts/deploy.sh joins them,
agrees for all three Japode services and both their databases. So the change is in the
platform's script: build that model beside caddy-adapt.json and pass it with
`--compose`. jcloud then reports an absent model the way it reports absent routes ("not
supplied, so unchecked"), and the textual compose and needs checks are deleted, leaving
one code path.

The two repositories land in order: the platform script first, then the jcloud release
that drops the fallback, or the platform's CI turns red on a model it never built.

### §JCT20 How a container is reached and probed

Two of agree's compose assertions encode how the Japode platform reaches a container,
and Viglet Cloud reaches its own another way. Both were read off its generated
docker/docker-compose.clients.yml while building JCT9.

The port. agreeComposeModel (and agreeInstances, and the textual agreeCompose) require
the manifest's port in `expose:` or a published port. Viglet services join the `viglet`
network and declare neither, because expose is documentation there and nginx dials the
container name. The port is still stated twice: in the healthcheck and in the edge's
upstream. So the evidence should be any of the three (expose, a published target, the
healthcheck naming it), and the route check already holds the upstream to it.

The probe. Turing's healthcheck is `bash -c '</dev/tcp/localhost/2700'`: a TCP connect,
no HTTP path. The manifest requires health.path, and agree requires the healthcheck to
mention it. A `probe: tcp` with no path is the honest statement: agree then asserts only
that the healthcheck names the port, and the edge, which has no active check under nginx
(JCT3), needs no path either. Under Caddy a tcp probe still needs a health_uri for the
edge, so Validate refuses `probe: tcp` there unless a path is given too.

Both widen what agrees; neither loosens Japode, whose services keep expose and HTTP
probes and still pass the stricter reading.

## Block C — The product page: jcloud on japode.com/jcloud/

## Block D — What jcloud prints: a report a reader can act on

### §JCT17 JCT17 — when the ::error annotations are printed

README.md says "inside GitHub Actions each one is also printed as an `::error file=…::`
annotation". jcloud prints them everywhere: main.go (check and compare), up.go and
survey.go call `fmt.Printf("::error…")` with no condition, and nothing reads
`GITHUB_ACTIONS`. Seen on 2026-11-10 running `jcloud check` in a Windows terminal, and
in every transcript `site/scripts/capture.sh` recorded in a plain container.

Either the README describes what happens, or jcloud prints the annotations only when
`GITHUB_ACTIONS=true`. The second halves what a person reads locally, where each problem
appears twice. It changes the output a caller may parse, so it is also a change to the
product page's transcripts, which are captured again with the release that ships it.

Decide which, then make the README and the binary say the same thing; a test pins the
behaviour on both sides of the variable if the binary changes.
