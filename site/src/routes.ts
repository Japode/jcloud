import { BASE, languages, releasesUrl, repoUrl, tag, words, type Lang } from "./content";

// One page per language. The table is what the design system's prerender reads, and what
// scripts/prerender.mjs reads to set each page's <html lang> and its hreflang links.
// The Japode organization's custom domain, which GitHub Pages serves this repository under.
export const SITE_ORIGIN = "https://japode.com";

// Absolute, because a platform fetching the social card is not on this origin.
export const OG_IMAGE = `${SITE_ORIGIN}${BASE}og.png`;

const order: Lang[] = ["en", "pt"];

export const ROUTES = order.map((lang) => ({
  path: languages[lang].path,
  lang,
  htmlLang: languages[lang].htmlLang,
  title: words[lang].meta.title,
  description: words[lang].meta.description,
}));

/** The language a route path is in; English for anything else. */
export function langFor(path: string): Lang {
  return path.replace(/\/+$/, "") === "/pt" ? "pt" : "en";
}

/** The canonical URL for a route, carrying the base. */
export function canonicalUrl(path: string): string {
  const rel = path === "/" ? "" : `${path.replace(/^\//, "")}/`;
  return `${SITE_ORIGIN}${BASE}${rel}`;
}

/** The page's schema.org description of jcloud, in the route's language (JCT15). The release
 *  is the one the build read from git, so the version and the download agree with the page. */
export function jsonLd(path: string) {
  const route = ROUTES.find((r) => r.path === path) ?? ROUTES[0];
  return {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: "jcloud",
    description: route.description,
    url: canonicalUrl(route.path),
    inLanguage: route.htmlLang,
    image: OG_IMAGE,
    applicationCategory: "DeveloperApplication",
    operatingSystem: "Linux, macOS, Windows",
    softwareVersion: tag.replace(/^v/, ""),
    isAccessibleForFree: true,
    offers: { "@type": "Offer", price: "0", priceCurrency: "USD" },
    downloadUrl: releasesUrl,
    sameAs: [repoUrl],
    publisher: { "@type": "Organization", name: "Japode", url: `${SITE_ORIGIN}/` },
  };
}

/** Where a route's HTML is written, relative to dist/: "" for the root. */
export function outputDir(path: string): string {
  return path === "/" ? "" : path.replace(/^\//, "");
}
