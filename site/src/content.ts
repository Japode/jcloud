// Every word on the page, in English and Portuguese, in one module: the components only lay it
// out. The two dictionaries have one shape (Words), so a section cannot exist in one language
// and not the other, and what does not change with the language (the links, the manifest, the
// terminal output jcloud prints in English) is written once above them.

export const THEME_KEY = "jcloud-theme";
export const BASE = "/jcloud/";
export const repoUrl = "https://github.com/Japode/jcloud";

export const asset = (path: string) => `${BASE}${path}`;

export type Lang = "en" | "pt";

/** Each language's route, its <html lang>, and how the other language names it. */
export const languages: Record<Lang, { path: string; htmlLang: string; switchTo: string; switchLabel: string }> = {
  en: { path: "/", htmlLang: "en", switchTo: "PT", switchLabel: "Ver em português" },
  pt: { path: "/pt", htmlLang: "pt-BR", switchTo: "EN", switchLabel: "View in English" },
};

/** The page of a language, with the base. */
export const pageHref = (lang: Lang) => (lang === "en" ? BASE : `${BASE}pt/`);

// --- what does not change with the language ---------------------------------------------

// `releases/latest` is the one release link that cannot go stale on the next `v*` tag.
export const releasesUrl = `${repoUrl}/releases/latest`;

/** One line of a terminal: what was typed, what jcloud printed, and how it coloured it. */
export type TermLine = { kind: "prompt" | "ok" | "err" | "out" | "blank"; text: string };

/**
 * The hero's run, copied from the terminal and not composed: jcloud's development build of
 * commit 73a1405 (2026-11-10, go1.27.1), run in site/demo/hero against the generic demo
 * manifest there. The manifest lines are that file's, with comments and the env list taken
 * out. JCT12 replaces hand copies like this one with transcripts a script captures from a
 * released binary.
 */
export const heroRun = {
  file: "japode.yml",
  manifest: [
    "version: 1",
    "name: notes",
    "image: ghcr.io/example/notes",
    "port: 8080",
    "health:",
    "  path: /healthz",
    "  probe: curl",
    "volumes:",
    "  - name: data",
    "    path: /data",
    "routes:",
    "  - hosts: [notes.example.com]",
  ],
  terminal: [
    { kind: "prompt", text: "jcloud check japode.yml" },
    { kind: "ok", text: "  ✓ japode.yml: notes states what it declares (1 volume(s), 1 route(s), 2 variable(s))" },
    { kind: "blank", text: "" },
    { kind: "ok", text: "1 manifest(s) state what they declare" },
  ] satisfies TermLine[],
};

// --- the words ------------------------------------------------------------------------------

type Words = {
  meta: { title: string; description: string };
  nav: { cta: string; repo: string; themeLabel: (next: string) => string };
  hero: {
    eyebrow: string;
    title: string;
    titleAccent: string;
    sub: string;
    primary: string;
    secondary: string;
    pills: string[];
    manifestLabel: string;
    terminalLabel: string;
    caption: string;
  };
  footer: { links: { href: string; label: string }[]; disclaimer: string };
};

export const words: Record<Lang, Words> = {
  en: {
    meta: {
      title: "jcloud: the manifest and the files that have to agree with it",
      description:
        "jcloud checks a service's japode.yml against its Dockerfile, Caddyfile and workflows, and against the compose file, route, backup.conf and .env.example the platform deploys beside it.",
    },
    nav: {
      cta: "Download",
      repo: "GitHub",
      themeLabel: (next) => `Switch to ${next} theme`,
    },
    hero: {
      eyebrow: "jcloud · for Japode Cloud services",
      title: "One manifest.",
      titleAccent: "Every file that has to agree with it.",
      sub: "jcloud reads a service's japode.yml and checks it against the Dockerfile, Caddyfile and workflows in its repository, and against the compose file, route, backup.conf and .env.example the platform deploys beside it. A port moved in one place and not the other fails on that commit, not on the deploy.",
      primary: "Download jcloud",
      secondary: "View the source",
      pills: ["Linux · macOS · Windows", "One Go binary", "Runs in GitHub Actions"],
      manifestLabel: "japode.yml, excerpt",
      terminalLabel: "terminal",
      caption: "A real run on a demo manifest, copied from the terminal.",
    },
    footer: {
      links: [
        { href: repoUrl, label: "Source" },
        { href: `${repoUrl}/releases`, label: "Releases" },
      ],
      disclaimer: "jcloud is open source. It reads files and reports; only the env verb writes one.",
    },
  },
  pt: {
    meta: {
      title: "jcloud: o manifesto e os arquivos que precisam concordar com ele",
      description:
        "O jcloud confere o japode.yml de um serviço com o Dockerfile, o Caddyfile e os workflows dele, e com o compose, a rota, o backup.conf e o .env.example que a plataforma publica ao lado.",
    },
    nav: {
      cta: "Baixar",
      repo: "GitHub",
      themeLabel: (next) => `Mudar para o tema ${next === "dark" ? "escuro" : "claro"}`,
    },
    hero: {
      eyebrow: "jcloud · para os serviços da Japode Cloud",
      title: "Um manifesto.",
      titleAccent: "Todo arquivo que precisa concordar com ele.",
      sub: "O jcloud lê o japode.yml de um serviço e o confere com o Dockerfile, o Caddyfile e os workflows do repositório, e com o compose, a rota, o backup.conf e o .env.example que a plataforma publica ao lado. Uma porta trocada num lugar e não no outro falha naquele commit, e não no deploy.",
      primary: "Baixar o jcloud",
      secondary: "Ver o código",
      pills: ["Linux · macOS · Windows", "Um binário Go", "Roda no GitHub Actions"],
      manifestLabel: "japode.yml, trecho",
      terminalLabel: "terminal",
      caption: "Uma execução real num manifesto de demonstração, copiada do terminal.",
    },
    footer: {
      links: [
        { href: repoUrl, label: "Código" },
        { href: `${repoUrl}/releases`, label: "Versões" },
      ],
      disclaimer: "O jcloud é open source. Ele lê arquivos e informa; só o comando env grava um.",
    },
  },
};
