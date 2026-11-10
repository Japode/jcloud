# Improvements

## Block A

## Block B — A second platform: jcloud checks a tree that is not the Japode one

### §JCT2 A platform profile names the layout

Every path jcloud opens is a literal in its code: `services/*/japode.yml` in up.go and
survey.go, `caddy/sites/<name>.caddy` and `container_name: japode-<name>` in agree.go,
`.deploy-state/images.env` and the `scripts/deploy.sh` argv in up.go,
`addons/<type>/addon.yml` in needs.go. A platform root gains one file, `jcloud.yml`,
that states them: the manifest glob and file name, the route source and its kind, the
container-name prefix, the image variable spelling, the pin file and the deploy command
`up` prints. Absent, every value is the one Japode uses today, so the Japode platform
changes no byte and its contract tests pass unchanged.

The profile is data the platform repository carries, for the reason addon.yml is: what a
check holds a tree to sits in the same git log as the tree. jcloud reads it and never
fetches one.

Every other line in this block depends on this one.

Not in scope: rendering. JC34 refused generating compose files or routes from a
manifest, and a second platform is no argument against that decision.

### §JCT3 Reading nginx routes from nginx -T

agree reads the route from `caddy adapt` JSON (route_json.go) rather than from the
Caddyfile text, so a commented-out block cannot pass. The nginx equivalent is `nginx
-T`, run by the platform script in a pinned nginx image: the whole configuration with
every include resolved. jcloud parses that file and never runs nginx, the division
`--adapted` already makes. From each `server` block it reads `server_name` (hosts),
`proxy_pass` (upstream name and port), `client_max_body_size` (max_body) and the
`proxy_set_header` lines (proxy). The profile from JCT2 says which kind a platform uses,
and `--adapted` becomes `--routes <file>`.

Viglet Cloud is the case that motivates it: the edge is nginx inside cloud-frontend,
behind Cloudflare, with a generated client fragment (clients.nginx.conf) included into
it and an `auth_request` to client-gate per client host. auth_request is not a manifest
field in this line; a route carrying one is reported as a directive the manifest does
not state, not refused.

`check --repo` compares a service's own Caddyfile. A service behind an nginx platform
usually ships no proxy configuration at all, so an absent `--caddyfile` has to mean not
checked rather than refused.

### §JCT4 Comparing against the resolved compose model

agreeCompose reads `services/<name>/docker-compose.yml` as text and asserts substrings:
`${<NAME>_IMAGE`, `container_name: japode-<name>`, the healthcheck words. Against a file
holding every service, a substring matches a neighbour's block. The resolved model is
what to compare: `docker compose config --format json` with every profile enabled,
written by the platform script the way caddy adapt is today, gives each service its
image, build, environment, volumes, healthcheck, depends_on and profiles after
interpolation. jcloud reads that JSON and looks the service up by name. The profile from
JCT2 says which compose files make up the model and whether a service directory is
required.

This also closes a weakness Japode has now: a substring assertion passes on a
commented-out line. Japode keeps its per-directory layout and passes the same model, so
one code path serves both.

A manifest may declare `profile:`, so a service outside the default profile is checked
as one, and `survey` does not report it missing where that profile is off on the server.

### §JCT5 Shared needs

A need today resolves to `<service>-<need>`, a container the service owns with its own
volume and the image addons/<type>/addon.yml pins (needs.go). Viglet Cloud has the other
shape: platform services (postgres, redis, keycloak) that several products reach by
name, each product with its own database and user inside the shared server. The schema
gains `shared: <platform service>` on a need. The addon still states image, port,
healthcheck and the variables the client must set, but the container is the named
platform service, depends_on is asserted against it, and its volume is backed up once by
the platform, not once per client.

The addon files for postgres, redis and keycloak are the platform repository's data, not
jcloud's; this line makes them expressible. What jcloud owns is the validation: a shared
need names a service the model from JCT4 declares, and the variables the addon lists
reach the client container.

Keycloak also brings the realm import and the client registration, which no manifest
field covers. That stays outside the schema.

### §JCT6 Backup agreement is the platform to declare

agreeBackupConf reads `services/<name>/backup.conf` in the directive grammar of Japode's
scripts/backup-lib.sh (`sqlite`, `mysql`, skip lines). That grammar is a fact about one
platform's backup tool. The profile from JCT2 states the backup kind: `backup-conf`,
today's behaviour and the default, or `none`, which skips the comparison and says so in
the report instead of passing in silence.

A third kind is not designed here. Viglet Cloud's backup-postgres.sh and
backup-volumes.sh read volume names from the compose model, and whether a manifest's
`sqlite:` volume maps onto them is a question to ask once that platform has manifests to
ask it of.

What holds under every kind: a volume declared `sqlite:`, or a need whose addon says
`backup: mysql`, is a file with a live writer, and under `none` jcloud still warns that
the platform claims no handling for it.

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
