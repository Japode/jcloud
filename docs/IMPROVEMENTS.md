# Improvements

## Block A

## Block B — A second platform: jcloud checks a tree that is not the Japode one

### §JCT7 Built images and tag pins

The manifest refuses a tag or digest in `image:` because the deploy pins it into
`.deploy-state/images.env` (pinnedDigest in up.go). Viglet Cloud has three kinds at
once: products pulled from GHCR by a moving tag (`ghcr.io/openviglet/turing:latest`,
shio, dumont), images built on the server from a Dockerfile in the platform tree
(console, keycloak, the sites), and third-party images with a fixed tag.

The schema gains `build: <dockerfile path>` as the alternative to `image:` for the
second kind, checked against the compose model's `build.dockerfile` (JCT4), and `up`
reports it as built on the host rather than unpinned. A moving tag stays a finding: `up`
names it and prints the digest the `--published` record says it resolves to now, which
is the step Viglet Cloud needs to move to digests.

The pin file and the variable spelling come from the profile (JCT2): Viglet Cloud spells
it `VIGLET_TURING_IMAGE`.

Nothing here softens the rule for Japode. Its profile keeps `build:` refused, since
nothing is built on that server.

### §JCT8 Declared env prefix and shared keys

The `.env.example` key for a manifest variable is `<SCREAM>_<NAME>` (agree.go), and
orphanEnvKeys reports a key under that prefix the manifest does not declare. Viglet
Cloud has keys several services read (OPENAI_API_KEY, the Keycloak client secrets) and
per-product keys under `VIGLET_<PRODUCT>_`. The manifest gains `env_prefix:`, defaulting
to today's derivation, and a variable may say `shared: true`: its `.env.example` key is
the bare name, other manifests may declare it too, and agree checks that every manifest
declaring a shared key agrees on its default and on `secret`.

`jcloud env` already takes `--file`, so `.env.age` works on any tree. Viglet Cloud has
no `.env.example` beside its compose file, only console/.env.example, and keeps a
tracked docker/.env with live secrets. The profile names where the example lives;
adopting jcloud there starts by writing one and moving docker/.env into `.env.age`.

### §JCT9 Manifests instanced by a registry

Viglet Cloud's scripts/gen-clients renders docker-compose.clients.yml, an nginx fragment
and a Keycloak import from docker/clients.yaml: per client key, an app container for
each product the client has, with its own redis and its own host. That is a renderer,
which the Japode platform refused (JC34) and Viglet Cloud chose, and `gen-clients
--check` already gates its drift in CI.

The open question is what a manifest is for such a service. Two shapes. A manifest with
placeholders (`{client}` in name, host and env prefix) plus the registry as input, so
agree checks each instance against the generated files. Or jcloud leaves generated files
to their generator's own check and validates only the product's template manifest. The
second is cheaper and may be enough.

Decide after JCT3 and JCT4 exist, against the real generated files rather than in the
abstract.

### §JCT10 A second platform in the contract tests

The TestCommitted tests read the tree JCLOUD_PLATFORM names and assert facts about
Japode's own manifests and addons and the wording of two of its scripts. With the
profile from JCT2 they read `jcloud.yml` from that tree and assert only what the profile
says that platform has. ci/check.sh accepts a list,
`JCLOUD_PLATFORM=../cloud:../../viglet/cloud`, and runs the suite once per tree,
skipping an absent tree as it does today.

The block is finished when Viglet Cloud carries a jcloud.yml and manifests for turing,
shio and dumont, and `jcloud agree` passes there. That adoption is the Viglet Cloud
repository's work, filed in its own roadmap; this line is the half that keeps it passing
after every jcloud change.

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

## Block C — The product page: jcloud on japode.com/jcloud/

## Block D — What jcloud prints: a report a reader can act on

### §JCT16 JCT16 — compare reports only what differs

`Manifest.CompareTo` (compare.go) trims the common head and tail of the two canonical
renderings and reports everything left, from both sides. With one difference that is one
line each. With two far apart it is every line between them: found on 2026-11-10 while
building the product page's compare tab, where the platform copy at port 8080 and a
service copy at port 3000 that had also gained one variable (`MAX_UPLOAD`) printed some
60 lines, `health:`, `volumes:` and `routes:` included, none of which differ. The page
shows the one-difference case for that reason.

The doc comment argues a general diff is not needed because the field order is the
struct's, so two manifests that disagree about a field disagree at the same place. That
holds for one field, and is exactly why a line-level LCS over the two canonical
renderings would stay small and exact: shared lines resync between differences. Keep the
"both sides shown" form per hunk, and say which field each hunk sits under (`env[2]`,
`port`) rather than quoting indentation.

Reproduce: `site/demo/platform/services/notes/japode.yml` against a copy with `port:
3000` and one more `env` entry. A test with two separated differences asserts the report
names only those.

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
