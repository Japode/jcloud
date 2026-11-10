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

`npm run build` is the gate. The prerender writes each language's page with its own title,
description, canonical, `<html lang>` and hreflang links, by replace-or-throw, so a template
that lost an anchor fails the build.

## Where things live

| Path | What |
|---|---|
| `src/content.ts` | **Every word, in both languages.** The two dictionaries share one type, so a section cannot exist in only one; the links and the terminal output are written once beside them |
| `src/i18n.tsx` | The page's language, as a React context the sections read |
| `src/routes.ts` | The two routes and their canonical URLs, read by the prerender |
| `src/components/` | The hero and the chrome |
| `demo/hero/japode.yml` | The generic demo manifest the hero shows, and runs jcloud against |
| `public/icon.svg` | The mark: two sheets that have to agree, and the check |
| `public/favicon.svg` | The mark at tab size, the check alone |
| `scripts/prerender.mjs` | The prerender, and the per-language `<html lang>` and hreflang |

## Terminal output is never typed

Every line of jcloud output on the page was printed by jcloud. The hero's run is
`jcloud check japode.yml` in `demo/hero`, and its provenance is in the comment on `heroRun`
in `src/content.ts`. Change the manifest and run it again, rather than editing the output.
