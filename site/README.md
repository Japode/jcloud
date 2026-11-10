# jcloud site

The public page, in English (`/jcloud/`) and Portuguese (`/jcloud/pt/`), served by GitHub
Pages at https://japode.com/jcloud/. A self-contained Vite + React 19 + TypeScript +
Tailwind v4 workspace: `ci/check.sh` and the Go build neither build nor need it.

The chrome is the family's: the nav, footer, buttons and theme toggle come from
`@viglet/viglet-design-system/site`, the colours from its cool tone (`data-tone="cool"`, the
`--vg-*` tokens), and the prerender from its `/vite` helpers, as on the Revelei site. The page
between the nav and the footer is jcloud's own: a command-line tool shows itself in a
terminal, so the hero's stage is a window with a manifest and the run.

## Commands

```
npm install        # once
npm run dev        # dev server at /jcloud/ (and /jcloud/pt/)
npm run build      # tsc, client, SSR, then the prerender of both pages
npm run preview    # serve the built dist/
```

The build reads the newest `v*` tag from git (`vite.config.ts`, `__JCLOUD_TAG__`), and
every install command on the page names that release, so no version is written in the
source. A checkout with no tags fails the build rather than publishing a command for a
version that does not exist. The CI step on the page was run as written, against the
release, before it was put there: if GoReleaser's archive names change, run it again.

`npm run build` is the gate. The prerender writes each language's page with its own title,
description, canonical, og tags, `<html lang>`, hreflang links and JSON-LD (a
`SoftwareApplication` naming the release the build found), by replace-or-throw, so a template
that lost an anchor fails the build. It also rasterises `og.png`, the 1200 x 630 social card
in the cool tone, and `apple-touch-icon.png` from `public/icon.svg`; resvg needs a font on
disk, which is why `site.yml` installs `fonts-dejavu-core`.

The nav carries a "part of Japode" chip and the footer a "More from Japode" link, both to
`/`, the storefront at the root of japode.com, as on Revelei's site.

## Where things live

| Path | What |
|---|---|
| `src/content.ts` | **Every word, in both languages.** The two dictionaries share one type, so a section cannot exist in only one; the links and the terminal output are written once beside them |
| `src/i18n.tsx` | The page's language, as a React context the sections read |
| `src/routes.ts` | The two routes and their canonical URLs, read by the prerender |
| `src/transcripts.json` | Every terminal on the page, written by `scripts/capture.sh`; never edited by hand |
| `src/components/` | The hero, one tab per verb (`Verbs`), where each verb runs (`Shape`), the shared `Terminal`, and the chrome |
| `demo/` | The scenarios, one per verb, all about one generic service called `notes`: `hero/` and `check/` (a manifest), `repo/` (a service repository whose Dockerfile moved the port), `platform/` (the files a platform deploys beside it), `compare/`, `survey/` and `env/` |
| `public/icon.svg` | The mark: two sheets that have to agree, and the check |
| `public/favicon.svg` | The mark at tab size, the check alone |
| `scripts/prerender.mjs` | The prerender, `og.png`, `apple-touch-icon.png`, and the per-language `<html lang>`, hreflang and JSON-LD |

## Terminal output is never typed

Every line of jcloud output on the page was printed by a released jcloud, and the page names
which one under the tabs. To capture again, after a release or after changing a scenario:

```
bash site/scripts/capture.sh v0.2.0
```

It builds the tag the way GoReleaser does, in the Go image `ci/check.sh` pins and for Linux, so
paths print with `/` on every machine. Then it adapts `demo/platform/caddy/sites/notes.caddy`
with `caddy:2-alpine` into `demo/platform/caddy-adapt.json`, which `agree` reads, and runs
`scripts/capture.mjs` in a Node image. That script runs each scenario and writes
`src/transcripts.json`. Docker is the only requirement. Commit what it writes.

The colours come from jcloud's own ANSI codes, read before they are stripped: green is a pass,
red a refusal, and an `::error` line is the annotation GitHub Actions shows on the file.

`demo/env/.env.age` is encrypted to a recipient whose private key was thrown away when the
file was made. `jcloud env list` needs no key, which is the point that tab makes, and nobody
can read the demo values.
