// The prerender, the social card and the touch icon, after the client and SSR builds.
//
// prerenderSite writes each language's page into dist/, patching the <head> (title,
// description, canonical, og tags) by replace-or-throw, and writes sitemap.xml and robots.txt
// from the same route table. ogCardSvg draws the family's social card in the cool tone with
// the jcloud mark, and renderOgCard rasterises it to dist/og.png at 1200 x 630. The 180 px
// touch icon is rasterised from public/icon.svg, since an Apple device will not take an SVG.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { Resvg } from "@resvg/resvg-js";
import { ogCardSvg, prerenderSite, renderOgCard } from "@viglet/viglet-design-system/vite";

import { render, ROUTES, canonicalUrl, jsonLd, outputDir, OG_IMAGE } from "../dist-server/entry-server.js";

const siteDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const distDir = join(siteDir, "dist");
const icon = readFileSync(join(siteDir, "public", "icon.svg"), "utf8");

const card = await renderOgCard({
  // English, the default page's language; one card serves both, as og:image is one URL.
  svg: ogCardSvg({
    eyebrow: "jcloud · for Japode Cloud services",
    title: ["One manifest."],
    titleAccent: "Every file that agrees with it.",
    line: ["Checks japode.yml against the Dockerfile, the route, the compose file,", "backup.conf and .env.example. A moved port fails on the commit."],
    mark: icon,
    tone: "cool",
  }),
  out: join(distDir, "og.png"),
});
console.log(`og-image: dist/og.png  ${card.width}x${card.height}  (${(card.bytes / 1024).toFixed(0)} kB)`);

const touch = new Resvg(icon, { fitTo: { mode: "width", value: 180 } }).render().asPng();
writeFileSync(join(distDir, "apple-touch-icon.png"), touch);
console.log(`touch icon: dist/apple-touch-icon.png  180x180  (${(touch.length / 1024).toFixed(0)} kB)`);

const written = prerenderSite({
  distDir,
  routes: ROUTES,
  render,
  canonicalUrl,
  outputDir,
  ogImage: OG_IMAGE,
  git: { cwd: siteDir },
  log: console.log,
});

// The language half the shared prerender does not know about: each page's <html lang>, the
// hreflang links that tell a search engine the two pages are one page in two languages, and
// the JSON-LD that says the page is an application's, in that page's language.
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
    // "<" escaped so no text in it can close the script early
    ["</head>", `<script type="application/ld+json">${JSON.stringify(jsonLd(route.path)).replace(/</g, "\\u003c")}</script>\n  </head>`],
  ]) {
    if (!html.includes(from)) throw new Error(`${file} lost its ${from} anchor`);
    html = html.replace(from, () => to);
  }
  writeFileSync(file, html);

  // Google Analytics loads only after consent (JCT31): the tag must be in no built page, and
  // the footer must carry the way back to the banner. Either one missing fails the build.
  if (html.includes("googletagmanager")) throw new Error(`${file} loads the analytics tag before consent`);
  if (!html.includes('class="consent-settings"')) throw new Error(`${file} lost the footer's analytics settings`);
}

console.log(`prerender: ${written.length} route(s) in ${ROUTES.map((r) => r.htmlLang).join(" and ")}, sitemap.xml and robots.txt written to dist/`);
