// The prerender, after the client and SSR builds.
//
// prerenderSite writes each language's page into dist/, patching the <head> (title,
// description, canonical) by replace-or-throw, and writes sitemap.xml and robots.txt from the
// same route table. The social card, the touch icon and the JSON-LD are JCT15's.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { prerenderSite } from "@viglet/viglet-design-system/vite";

import { render, ROUTES, canonicalUrl, outputDir } from "../dist-server/entry-server.js";

const siteDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const distDir = join(siteDir, "dist");

const written = prerenderSite({
  distDir,
  routes: ROUTES,
  render,
  canonicalUrl,
  outputDir,
  git: { cwd: siteDir },
  log: console.log,
});

// The language half the shared prerender does not know about: each page's <html lang> and the
// hreflang links that tell a search engine the two pages are one page in two languages.
// Replace-or-throw, like the prerender: a template that lost an anchor must fail the build.
const alternates = [
  ...ROUTES.map((r) => `<link rel="alternate" hreflang="${r.htmlLang}" href="${canonicalUrl(r.path)}" />`),
  `<link rel="alternate" hreflang="x-default" href="${canonicalUrl("/")}" />`,
].join("\n    ");
for (const route of ROUTES) {
  const file = join(distDir, outputDir(route.path), "index.html");
  let html = readFileSync(file, "utf8");
  for (const [from, to] of [
    ['<html lang="en" data-tone="cool">', `<html lang="${route.htmlLang}" data-tone="cool">`],
    ["<!--hreflang-->", alternates],
  ]) {
    if (!html.includes(from)) throw new Error(`${file} lost its ${from} anchor`);
    html = html.replace(from, () => to);
  }
  writeFileSync(file, html);
}

console.log(`prerender: ${written.length} route(s) in ${ROUTES.map((r) => r.htmlLang).join(" and ")}, sitemap.xml and robots.txt written to dist/`);
