import { execFileSync } from "node:child_process";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { siteThemeScript } from "@viglet/viglet-design-system/site";

// GitHub Pages serves a project site under the repository's name, so the page lives at
// https://japode.com/jcloud/ and every asset path carries the prefix. Written here and in
// src/content.ts, and nowhere else.
export const BASE = "/jcloud/";

// The key the theme choice is stored under; src/content.ts names the same one for the toggle.
const THEME_KEY = "jcloud-theme";

// The pre-paint theme script comes from the design system rather than being typed into
// index.html, so the page and the toggle cannot disagree about the rule. Replace-or-throw: a
// template that lost its anchor would ship a flash of the wrong theme.
function themeScript(): Plugin {
  const anchor = "<!--theme-script-->";
  return {
    name: "theme-script",
    transformIndexHtml(html) {
      if (!html.includes(anchor)) throw new Error(`index.html lost its ${anchor} anchor`);
      return html.replace(anchor, () => `<script>${siteThemeScript(THEME_KEY)}</script>`);
    },
  };
}

// The release the install commands name, read from git at build time (JCT13): a version
// written into src/content.ts goes stale on the next `v*` tag. A build with no tags in reach
// fails rather than publishing an install command for a version that does not exist, which
// is why the Pages workflow checks out with full history.
function latestTag(): string {
  try {
    return execFileSync("git", ["describe", "--tags", "--abbrev=0", "--match", "v*"], { encoding: "utf8" }).trim();
  } catch {
    throw new Error("no v* tag is reachable from HEAD: fetch the tags (fetch-depth: 0) before building the site");
  }
}

export default defineConfig({
  base: BASE,
  define: { __JCLOUD_TAG__: JSON.stringify(latestTag()) },
  plugins: [react(), tailwindcss(), themeScript()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
