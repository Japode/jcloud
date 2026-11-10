# Shipped Ledger

## Block A

- ✅ **JCT1** **jcloud runs only where Go or Docker is present: a tag publishes source and no executable** — Every v* tag publishes checksummed jcloud binaries for six platforms through GoReleaser, built with the compiler go.mod pins; the version comes from the tag.

## Block B — A second platform: jcloud checks a tree that is not the Japode one

- ✅ **JCT2** **jcloud reads only the Japode tree: manifest glob, route path, container prefix and pin file are constants** — jcloud.yml at a platform root names its manifest, route, container, image variable, pin file, deploy and addon paths; absent, jcloud reads the Japode layout (design recorded in `profile.go`).

## Block C — The product page: jcloud on japode.com/jcloud/

- ✅ **JCT11** **jcloud has no product page, so it can be found only by someone already inside the repository** — site/ holds a prerendered EN and PT product page with canonical and hreflang, whose hero shows a real jcloud run on a generic demo manifest (design recorded in `site/README.md`).
- ✅ **JCT12** **The page cannot show what jcloud catches, since no real output of a check exists outside a terminal** — Each verb has a tab on the page with real output that site/scripts/capture.sh records from a released jcloud, plus a view of where each verb runs (design recorded in `site/README.md`).
- ✅ **JCT13** **The page gives no way to install or run jcloud, so a reader convinced by it has nowhere to go** — The page has an install section naming the newest tag, read from git at build time: the release download, go install, and a tested GitHub Actions step (design recorded in `site/README.md`).
- ✅ **JCT14** **Nothing builds or publishes site/, so the page exists only on the machine it was written on** — site.yml builds site/ as a gate on every push touching it and deploys to Pages on dispatch only; ci.yml now ignores site/** (design recorded in `.github/workflows/site.yml`).
- ✅ **JCT15** **The page declares no canonical, social card or link to Japode, so search and shares lose it** — The page declares its canonical, og tags, social card, touch icon and JSON-LD, links back to the Japode storefront, and the README links to it (design recorded in `site/README.md`).

## Block D — What jcloud prints: a report a reader can act on

