# Shipped Ledger

## Block A

- ✅ **JCT1** **jcloud runs only where Go or Docker is present: a tag publishes source and no executable** — Every v* tag publishes checksummed jcloud binaries for six platforms through GoReleaser, built with the compiler go.mod pins; the version comes from the tag.

## Block B — A second platform: jcloud checks a tree that is not the Japode one

- ✅ **JCT2** **jcloud reads only the Japode tree: manifest glob, route path, container prefix and pin file are constants** — jcloud.yml at a platform root names its manifest, route, container, image variable, pin file, deploy and addon paths; absent, jcloud reads the Japode layout (design recorded in `profile.go`).
- ✅ **JCT3** **Routes agree only with Caddy, so an nginx edge such as the Viglet Cloud cloud-frontend cannot be checked at all** — agree --routes reads nginx -T on a route.kind nginx platform: server_name, proxy_pass via set or upstream, body size, headers; hosts keys are Caddy's (design recorded in `route_nginx.go`).
- ✅ **JCT4** **agree greps one compose file per service, so a single shared compose file split by profiles reads as missing** — agree --compose finds each service by name in the resolved compose model, so a shared file split by profiles agrees (design superseded: uninterpolated) (design recorded in `compose_model.go`).
- ✅ **JCT5** **needs states only a sidecar database the service owns, never a postgres, redis or Keycloak several services share** — A need may say shared: <service>; agree holds that platform service to the addon and the client to waiting on and reaching it (design recorded in `addon.go`).
- ✅ **JCT6** **agree refuses a service without a Japode-format backup.conf, so a platform with its own backup scripts cannot pass** — jcloud.yml may say backup: none; agree then compares no backup.conf, says so, and names each sqlite file or database the platform claims nothing for (design recorded in `agree.go`).
- ✅ **JCT7** **up and survey read every image built on the server as unpinned, since a digest in images.env is the only pin they know** — build: names a host-built image, checked against the model and ready unpinned where builds: true; up names a tag pin and the digest to pin (design recorded in `up.go`).
- ✅ **JCT8** **agree puts every variable under a service-name prefix, which a shared key like OPENAI_API_KEY can never carry** — env_prefix: names a service's key prefix and shared: true a bare key many services read, held to one default and secret; jcloud.yml names .env.example (design recorded in `agree.go`).
- ✅ **JCT9** **One product instanced per client silo needs one manifest per client, though only the host and the key differ** — instances: names per-client copies by template; agree finds each in the compose model and edge, holding it to the product's port, image, health and route (design recorded in `instances.go`).
- ✅ **JCT10 (multi-platform contract tests)** **Contract tests read only a Japode checkout, so nothing proves a second platform still agrees after a jcloud change** — The contract tests run once per tree in a JCLOUD_PLATFORM list, each read through its own jcloud.yml; ci/check.sh mounts every tree.
- ✅ **JCT18** **A route gated by auth_request is reported forever, since no manifest field can state the gate** — A route states gate: <uri>; agree compares it with nginx auth_request or Caddy forward_auth both ways, and a declared gate the route lacks is reported as open (design recorded in `route_json.go`).
- ✅ **JCT20** **agree demands an exposed port and an HTTP health path, so a service on a user network with a TCP probe cannot agree** — probe: tcp needs no path, and a service with no expose or ports shows its port in its healthcheck; a declared expose still has to list it (design recorded in `compose_model.go`).
- ✅ **JCT21** **agree refuses a compose model whose environment holds a number, such as PORT: 2700, which compose accepts** — agree decodes a compose model whose environment holds numbers, booleans or null, renders each as compose does and compares every manifest it carries.

## Block C — The product page: jcloud on japode.com/jcloud/

- ✅ **JCT11** **jcloud has no product page, so it can be found only by someone already inside the repository** — site/ holds a prerendered EN and PT product page with canonical and hreflang, whose hero shows a real jcloud run on a generic demo manifest (design recorded in `site/README.md`).
- ✅ **JCT12** **The page cannot show what jcloud catches, since no real output of a check exists outside a terminal** — Each verb has a tab on the page with real output that site/scripts/capture.sh records from a released jcloud, plus a view of where each verb runs (design recorded in `site/README.md`).
- ✅ **JCT13** **The page gives no way to install or run jcloud, so a reader convinced by it has nowhere to go** — The page has an install section naming the newest tag, read from git at build time: the release download, go install, and a tested GitHub Actions step (design recorded in `site/README.md`).
- ✅ **JCT14** **Nothing builds or publishes site/, so the page exists only on the machine it was written on** — site.yml builds site/ as a gate on every push touching it and deploys to Pages on dispatch only; ci.yml now ignores site/** (design recorded in `.github/workflows/site.yml`).
- ✅ **JCT15** **The page declares no canonical, social card or link to Japode, so search and shares lose it** — The page declares its canonical, og tags, social card, touch icon and JSON-LD, links back to the Japode storefront, and the README links to it (design recorded in `site/README.md`).

## Block D — What jcloud prints: a report a reader can act on

- ✅ **JCT16** **compare reports every line between two distant differences, so a port and one new variable print some 60 lines** — compare diffs the two canonical renderings by longest common subsequence, so a port and one new variable print 8 lines, each named by its field (design recorded in `compare.go`).
- ✅ **JCT17** **The README says ::error annotations are printed inside GitHub Actions, and jcloud prints them everywhere** — jcloud prints ::error and ::warning annotations only when GITHUB_ACTIONS is true, so a terminal shows each problem once; the README says so (design recorded in `annotate.go`).

## Block E — Watching the server: jcloud reads the running machine on its own

- ✅ **JCT22** **Only Japode's survey-server.sh gathers observations, so another platform has no collector and must write its own** — jcloud observe runs on the host and prints the container, running, pinned and profile lines survey reads, so a platform needs no collector of its own (design recorded in `observe.go`).
