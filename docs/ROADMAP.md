# Roadmap (active backlog)

## Block A

## Block B — A second platform: jcloud checks a tree that is not the Japode one

- 📋 **JCT5** (deps: JCT2 ✅, JCT4 ✅) **needs states only a sidecar database the service owns, never a postgres, redis or Keycloak several services share** — Turing, Shio and Dumont in Viglet Cloud share one postgres, one redis and one Keycloak, and a need jcloud cannot state is drift it cannot see. → §JCT5
- 📋 **JCT6** (deps: JCT2 ✅) **agree refuses a service without a Japode-format backup.conf, so a platform with its own backup scripts cannot pass** — Viglet Cloud backs up through backup-postgres.sh and backup-volumes.sh, and forcing restic directives on it would check a file nothing reads. → §JCT6
- 📋 **JCT7** (deps: JCT2 ✅, JCT4 ✅) **up and survey read every image built on the server as unpinned, since a digest in images.env is the only pin they know** — Viglet Cloud still builds console, keycloak and the sites on the host and pulls its products by latest tag, and the report should name each case. → §JCT7
- 📋 **JCT8** (deps: JCT2 ✅) **agree puts every variable under a service-name prefix, which a shared key like OPENAI_API_KEY can never carry** — Viglet Cloud reads several keys in many containers and prefixes others with VIGLET_, so the prefix has to be declared, not derived from the name. → §JCT8
- 💭 **JCT9** (deps: JCT3 ✅, JCT4 ✅) **One product instanced per client silo needs one manifest per client, though only the host and the key differ** — Viglet Cloud renders per-client Turing, Shio and Dumont from clients.yaml, and a manifest per copy would be the drift jcloud exists to stop. → §JCT9
- 📋 **JCT10** (deps: JCT3 ✅, JCT4 ✅, JCT5, JCT6, JCT7, JCT8) **Contract tests read only a Japode checkout, so nothing proves a second platform still agrees after a jcloud change** — Applying jcloud to Viglet Cloud is the acceptance test for this block, and it has to run in jcloud CI rather than once by hand. → §JCT10
- 📋 **JCT18** (deps: —) **A route gated by auth_request is reported forever, since no manifest field can state the gate** — Every Viglet Cloud per-client host runs auth_request to client-gate, so those routes cannot agree whatever their manifest says. → §JCT18
- 📋 **JCT19** (deps: —) **Japode's check script passes no compose model, so its services still go through the substring compose check** — That check passes on a commented-out line, and keeping it beside the model leaves two rules for one fact. → §JCT19

## Block C — The product page: jcloud on japode.com/jcloud/

## Block D — What jcloud prints: a report a reader can act on

- 📋 **JCT16** (deps: —) **compare reports every line between two distant differences, so a port and one new variable print some 60 lines** — compare is the check between the platform's copy and the service's own, and a report nobody can read is one whoever runs it stops reading. → §JCT16
- 💭 **JCT17** (deps: —) **The README says ::error annotations are printed inside GitHub Actions, and jcloud prints them everywhere** — Local output repeats every problem twice, and a reader of the README expects a terminal to show each once. → §JCT17

## Done when — Block B

- **jcloud agree passes on Viglet Cloud** Viglet Cloud carries a jcloud.yml and
  manifests for Turing, Shio and Dumont, and the check runs green there in jcloud CI
  while the Japode platform stays green unchanged.

## Done when — Block C

- **japode.com/jcloud/ is live in both languages** The Pages deploy from site.yml serves
  /jcloud/ and /jcloud/pt/, every transcript on it was captured from a released jcloud,
  and the README links to it.

## Non-goals
