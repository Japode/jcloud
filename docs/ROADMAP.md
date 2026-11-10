# Roadmap (active backlog)

## Block A

## Block B — A second platform: jcloud checks a tree that is not the Japode one

- 📋 **JCT2** (deps: —) **jcloud reads only the Japode tree: manifest glob, route path, container prefix and pin file are constants** — Viglet Cloud keeps the same facts under other names, so every check refuses it before asking a single real question. → §JCT2
- 📋 **JCT3** (deps: JCT2) **Routes agree only with Caddy, so an nginx edge such as the Viglet Cloud cloud-frontend cannot be checked at all** — The route is where a wrong port or a missing header fails silently, and it is the check docker compose config cannot make. → §JCT3
- 📋 **JCT4** (deps: JCT2) **agree greps one compose file per service, so a single shared compose file split by profiles reads as missing** — Viglet Cloud declares some thirty services in one docker-compose.yml, separated by profiles rather than by directory. → §JCT4
- 📋 **JCT5** (deps: JCT2, JCT4) **needs states only a sidecar database the service owns, never a postgres, redis or Keycloak several services share** — Turing, Shio and Dumont in Viglet Cloud share one postgres, one redis and one Keycloak, and a need jcloud cannot state is drift it cannot see. → §JCT5
- 📋 **JCT6** (deps: JCT2) **agree refuses a service without a Japode-format backup.conf, so a platform with its own backup scripts cannot pass** — Viglet Cloud backs up through backup-postgres.sh and backup-volumes.sh, and forcing restic directives on it would check a file nothing reads. → §JCT6
- 📋 **JCT7** (deps: JCT2, JCT4) **up and survey read every image built on the server as unpinned, since a digest in images.env is the only pin they know** — Viglet Cloud still builds console, keycloak and the sites on the host and pulls its products by latest tag, and the report should name each case. → §JCT7
- 📋 **JCT8** (deps: JCT2) **agree puts every variable under a service-name prefix, which a shared key like OPENAI_API_KEY can never carry** — Viglet Cloud reads several keys in many containers and prefixes others with VIGLET_, so the prefix has to be declared, not derived from the name. → §JCT8
- 💭 **JCT9** (deps: JCT3, JCT4) **One product instanced per client silo needs one manifest per client, though only the host and the key differ** — Viglet Cloud renders per-client Turing, Shio and Dumont from clients.yaml, and a manifest per copy would be the drift jcloud exists to stop. → §JCT9
- 📋 **JCT10** (deps: JCT3, JCT4, JCT5, JCT6, JCT7, JCT8) **Contract tests read only a Japode checkout, so nothing proves a second platform still agrees after a jcloud change** — Applying jcloud to Viglet Cloud is the acceptance test for this block, and it has to run in jcloud CI rather than once by hand. → §JCT10

## Block C — The product page: jcloud on japode.com/jcloud/

- 📋 **JCT13** (deps: JCT11 ✅) **The page gives no way to install or run jcloud, so a reader convinced by it has nowhere to go** — The release archives, go install and the CI step are the action the page exists to lead to. → §JCT13
- 📋 **JCT14** (deps: JCT11 ✅) **Nothing builds or publishes site/, so the page exists only on the machine it was written on** — GitHub Pages serves the repository at japode.com/jcloud/ once a workflow deploys it, and the build must be gated before anyone publishes. → §JCT14
- 📋 **JCT15** (deps: JCT11 ✅, JCT14) **The page declares no canonical, social card or link to Japode, so search and shares lose it** — A page that search engines cannot attribute and a link that unfurls blank are the two ways a product page goes unread. → §JCT15

## Done when — Block B

- **jcloud agree passes on Viglet Cloud** Viglet Cloud carries a jcloud.yml and
  manifests for Turing, Shio and Dumont, and the check runs green there in jcloud CI
  while the Japode platform stays green unchanged.

## Done when — Block C

- **japode.com/jcloud/ is live in both languages** The Pages deploy from site.yml serves
  /jcloud/ and /jcloud/pt/, every transcript on it was captured from a released jcloud,
  and the README links to it.

## Non-goals
