# Roadmap (active backlog)

## Block A

## Block B — A second platform: jcloud checks a tree that is not the Japode one

- 📋 **JCT10** (deps: JCT3 ✅, JCT4 ✅, JCT5 ✅, JCT6 ✅, JCT7 ✅, JCT8 ✅) **Contract tests read only a Japode checkout, so nothing proves a second platform still agrees after a jcloud change** — Applying jcloud to Viglet Cloud is this block's acceptance test, and it must run in jcloud CI, not once by hand. → §JCT10
- 📋 **JCT18** (deps: —) **A route gated by auth_request is reported forever, since no manifest field can state the gate** — Every Viglet Cloud per-client host runs auth_request to client-gate, so those routes cannot agree whatever their manifest says. → §JCT18
- 📋 **JCT19** (deps: —) **Japode's check script passes no compose model, so its services still go through the substring compose check** — That check passes on a commented-out line, and keeping it beside the model leaves two rules for one fact. → §JCT19
- 📋 **JCT20** (deps: —) **agree demands an exposed port and an HTTP health path, so a service on a user network with a TCP probe cannot agree** — Every Viglet Cloud product is reached over its network and probed by TCP connect, so each would be refused on how it is reached. → §JCT20

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
