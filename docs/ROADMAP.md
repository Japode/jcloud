# Roadmap (active backlog)

## Block A

## Block B — A second platform: jcloud checks a tree that is not the Japode one

## Block C — The product page: jcloud on japode.com/jcloud/

## Block D — What jcloud prints: a report a reader can act on

## Block E — Watching the server: jcloud reads the running machine on its own

- ⏳ **JCT23** (deps: cloud C190) **survey holds a server only to its services; the timers, modes and firewall it needs are asserted in hand-written bash** — a manifest's declared backup implying its own timer, once a manifest can say it runs one. → §JCT23
- 📋 **JCT29** (deps: JCT21 ✅, JCT24 ✅, JCT25 ✅, JCT28 ✅, cloud C190) **Viglet Cloud's server is watched by nothing that reads it against what its own tree declares** — Adopting it with only jcloud.yml and manifests, no Viglet bash, is the proof the watch is transparent to a platform. → §JCT29

## Block F — Moving a service: one archive carries it to an empty server

- 📋 **JCT47** (deps: —) **A host.timers entry cannot run a jcloud command: run: is always a bash script under the root** — JCT46 made the jobs jcloud verbs, yet a timer still needs a one-line script and a carry: path to reach them, which FI194's jcloud/ folder should not carry. → §JCT47
- 📋 **JCT48** (deps: —) **A timer cannot run a jcloud verb: run: always becomes bash <root>/<first word>, so run: jcloud backup fails every firing** — The verbs exist so a timer runs a jcloud command; until the unit runs this binary, a site's own server still has no backup. → §JCT48

## Done when — Block B

- **jcloud agree passes on Viglet Cloud** Viglet Cloud carries a jcloud.yml and
  manifests for Turing, Shio and Dumont, and the check runs green there in jcloud CI
  while the Japode platform stays green unchanged.

## Done when — Block C

- **japode.com/jcloud/ is live in both languages** The Pages deploy from site.yml serves
  /jcloud/ and /jcloud/pt/, every transcript on it was captured from a released jcloud,
  and the README links to it.

## Done when — JCT10

- **jcloud CI runs the contract tests against both platform trees** ci.yml checks out
  Japode/cloud and openviglet/cloud with a read token and hands both to ci/check.sh.

## Done when — Block E

- **Both platforms are watched by jcloud alone** Japode and Viglet Cloud each run the
  shipped timer on the server, every finding reaches an issue, and neither carries bash
  that collects or asserts host facts.

## Done when — JCT23

- **A manifest that opts in implies its backup timer under host.backup_timer** A test
  reads a jcloud.yml with backup_timer and an opted-in manifest and survey reports that
  timer when inactive.

## Non-goals

- **A repair the watch runs on its own** A repair that writes to the server has to argue
  why it is not a deploy (JC96); a timer firing it unattended would skip that argument.
- **An alert channel beyond the one webhook the platform configures** The owner decided
  jcloud healthwatch posts its DOWN and UP lines to ALERT_WEBHOOK_URL (JCT46), so a
  platform with no bash still gets told. Routing, escalation, other channels and a
  second sender stay out; the survey watch templates still alert with the platform's own
  token (JCT28).
