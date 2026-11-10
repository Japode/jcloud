# Roadmap (active backlog)

## Block A

## Block B — A second platform: jcloud checks a tree that is not the Japode one

## Block C — The product page: jcloud on japode.com/jcloud/

## Block D — What jcloud prints: a report a reader can act on

## Block E — Watching the server: jcloud reads the running machine on its own

- ⏳ **JCT23** (deps: cloud C190) **survey holds a server only to its services; the timers, modes and firewall it needs are asserted in hand-written bash** — a manifest's declared backup implying its own timer, once a manifest can say it runs one. → §JCT23
- 📋 **JCT26** (deps: —) **Two surveys in a row cannot say what changed between them, so a fresh failure reads the same as standing drift** — A watch run every few minutes must tell new from known, or it alerts on every run or on none. → §JCT26
- 📋 **JCT27** (deps: —) **survey prints prose only, so a scheduler cannot tell one finding from another without parsing English sentences** — A stable code and subject per finding is what lets one alert open, update and close per fact, whatever the wording. → §JCT27
- 📋 **JCT24** (deps: JCT22 ✅, JCT23 ⏳) **survey cannot say a backup is stale: it reads that a timer is active, never whether its last run succeeded or when** — A timer whose unit fails every night still reads active, which is the 26 days without a copy that JC2 found. → §JCT24
- 📋 **JCT25** (deps: JCT22 ✅, JCT23 ⏳) **Nothing reports a disk filling or a TLS certificate near expiry until the service it holds up has already failed** — Both are answered locally by the host, so observe can read them and survey can hold them to a floor jcloud.yml declares. → §JCT25
- 📋 **JCT28** (deps: JCT22 ✅, JCT26, JCT27) **Nothing runs survey on a schedule, so drift and failed jobs are found only when somebody thinks to look** — jcloud ships a timer and a self-hosted workflow template that observe, compare with the last run and keep one issue per finding. → §JCT28
- 📋 **JCT29** (deps: JCT21 ✅, JCT24, JCT25, JCT28, cloud C190) **Viglet Cloud's server is watched by nothing that reads it against what its own tree declares** — Adopting it with only jcloud.yml and manifests, no Viglet bash, is the proof the watch is transparent to a platform. → §JCT29

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
