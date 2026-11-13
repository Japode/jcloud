// Every word on the page, in English and Portuguese, in one module: the components only lay it
// out. The two dictionaries have one shape (Words), so a section cannot exist in one language
// and not the other, and what does not change with the language (the links, the manifest, the
// terminal output jcloud prints in English) is written once above them.

import captured from "./transcripts.json";
import heroYml from "../demo/hero/japode.yml?raw";

export const THEME_KEY = "jcloud-theme";
export const BASE = "/jcloud/";
export const repoUrl = "https://github.com/Japode/jcloud";

export const asset = (path: string) => `${BASE}${path}`;

// The Japode storefront, at the root of the origin this page is served from (japode.com), so
// the link works on the custom domain without naming it twice. As on Revelei's site (PB49).
export const familyHref = "/";
export const familyName = "Japode";

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

/** The newest release, from git at build time (vite.config.ts), with and without its "v". */
export const tag = __JCLOUD_TAG__;
const version = tag.replace(/^v/, "");

/**
 * The install commands, all naming the same release. Each was run as written before it was
 * put here: the archive name is GoReleaser's (`jcloud_<version>_<os>_<arch>`), the archive
 * holds `jcloud` at its root, and `sha256sum -c` passes against the release's checksums.txt.
 */
export const install = {
  go: `go install github.com/Japode/jcloud@${tag}`,
  ci: `name: manifest
on: [push, pull_request]
jobs:
  japode-yml:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: jcloud check --repo
        env:
          V: ${version}
        run: |
          A=jcloud_\${V}_linux_amd64.tar.gz
          curl -fsSLO "https://github.com/Japode/jcloud/releases/download/v$V/$A"
          curl -fsSL "https://github.com/Japode/jcloud/releases/download/v$V/checksums.txt" | grep " $A\\$" | sha256sum -c
          tar -xzf "$A" jcloud
          ./jcloud check --repo . --repository \${{ github.repository }} japode.yml`,
  platforms: ["Linux", "macOS", "Windows"].map((os) => `${os} · amd64, arm64`),
};

/** One printed line, and how jcloud coloured it: see site/scripts/capture.mjs. */
export type TermLine = { kind: "ok" | "err" | "out" | "annotation" | "blank"; text: string };
export type Run = { cwd: string; cmd: string; exit: number; lines: TermLine[] };

/**
 * Every terminal on the page, as site/scripts/capture.sh recorded it from a released
 * jcloud against the scenarios under site/demo/. Never edited by hand: change a scenario
 * and capture again.
 */
export const transcripts = captured as { version: string; runs: Record<VerbId | "hero", Run> };

/**
 * The hero's manifest: site/demo/hero/japode.yml as it is on disk, without its comments,
 * blank lines and the env list, which the terminal under it already counts.
 */
export const heroManifest = heroYml
  .split("\n")
  .filter((l) => l.trim() !== "" && !l.trimStart().startsWith("#"))
  .join("\n")
  .split("\nenv:")[0]
  .split("\n");

/** The verbs the page walks through, in the order a service meets them. */
export const verbIds = ["check", "repo", "agree", "compare", "up", "survey", "env"] as const;
export type VerbId = (typeof verbIds)[number];

// --- the words ------------------------------------------------------------------------------

type Words = {
  meta: { title: string; description: string };
  nav: { cta: string; repo: string; themeLabel: (next: string) => string };
  family: { pre: string; title: string };
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
  verbs: {
    eyebrow: string;
    title: string;
    lead: string;
    tabsLabel: string;
    exit: (code: number) => string;
    annotation: string;
    provenance: (version: string) => string;
    items: Record<VerbId, { tab: string; title: string; body: string; files: string[] }>;
  };
  shape: {
    eyebrow: string;
    title: string;
    lead: string;
    places: { name: string; where: string; files: string[]; verbs: string[] }[];
  };
  install: {
    eyebrow: string;
    title: string;
    lead: (tag: string) => string;
    binary: { title: string; body: string; cta: string; checksums: string };
    go: { title: string; body: string };
    ci: { title: string; body: string; file: string };
    copy: { label: (what: string) => string; copy: string; copied: string };
  };
  footer: { links: { href: string; label: string }[]; disclaimer: string };
  consent: { label: string; body: string; decline: string; allow: string; settings: string };
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
    family: { pre: "part of", title: "Japode: small software that does one job" },
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
    verbs: {
      eyebrow: "What it catches",
      title: "The mistakes nothing else compares",
      lead: "docker compose config and caddy validate each read one file. A port written in three files passes both while the route aims at nothing. Each tab is one jcloud verb on a small demo service, with exactly what it printed.",
      tabsLabel: "jcloud verbs",
      exit: (code) => `exit ${code}`,
      annotation: "In GitHub Actions, each ::error line becomes a mark on the manifest in the pull request.",
      provenance: (version) => `Every line above was printed by ${version}, run against the files in site/demo.`,
      items: {
        check: {
          tab: "check",
          title: "A first draft",
          body: "check reads one manifest and refuses what a service has to declare and does not. Here: no health path, a route with no address, and a variable that is both fixed and defaulted. Every problem is reported at once, so one run is enough.",
          files: ["japode.yml"],
        },
        repo: {
          tab: "check --repo",
          title: "In the service's own CI",
          body: "Somebody moved the port to 3000 in the Dockerfile and left the manifest at 8080. check --repo compares the manifest with the Dockerfile, the Caddyfile and the workflows beside it, and fails on that commit, in front of whoever made it.",
          files: ["japode.yml", "Dockerfile", ".github/workflows/publish.yml"],
        },
        agree: {
          tab: "agree",
          title: "In the platform",
          body: "The route still proxies to port 3000, and .env.example keeps a key the service stopped reading. agree compares the manifest with the compose file, the route as Caddy reads it, backup.conf and .env.example.",
          files: ["services/notes/docker-compose.yml", "caddy/sites/notes.caddy", "services/notes/backup.conf", ".env.example"],
        },
        compare: {
          tab: "compare",
          title: "Two copies of one manifest",
          body: "The platform's copy and the service's own, after the service moved its port. Comments and key order may differ between them. A fact may not.",
          files: ["platform/services/notes/japode.yml", "compare/theirs.yml"],
        },
        up: {
          tab: "up",
          title: "Before a deploy",
          body: "What stands between a service and a deploy: a variable the manifest declares and the configuration lacks, and no image pinned. up only reports; it brings nothing up.",
          files: ["services/notes/japode.yml", ".env"],
        },
        survey: {
          tab: "survey",
          title: "Against the server",
          body: "What a collector saw on the host, against what the platform declares: a container running another digest than the one pinned, and a container nothing declares. survey reads a file, not a host.",
          files: ["survey/observations.txt", "docker-compose.yml", "services/*/japode.yml"],
        },
        env: {
          tab: "env",
          title: "The configuration",
          body: "Keys are readable and each value is encrypted with age. list needs no private key, so CI can check which keys exist without holding a secret.",
          files: [".env.age"],
        },
      },
    },
    shape: {
      eyebrow: "Where it runs",
      title: "One manifest, three places that have to agree with it",
      lead: "The manifest sits in the service's repository and a copy sits in the platform's. jcloud runs in both, and against what the server reports.",
      places: [
        {
          name: "Service repository",
          where: "its CI, on every pull request",
          files: ["japode.yml", "Dockerfile", "Caddyfile", ".github/workflows"],
          verbs: ["check", "check --repo"],
        },
        {
          name: "Platform repository",
          where: "its CI, and before a deploy",
          files: ["services/<name>/japode.yml", "docker-compose.yml", "caddy/sites/<name>.caddy", "backup.conf", ".env.example", ".env.age"],
          verbs: ["agree", "compare", "up", "env"],
        },
        {
          name: "Server",
          where: "read by a collector, never by jcloud",
          files: ["running containers", "pinned digests"],
          verbs: ["survey"],
        },
      ],
    },
    install: {
      eyebrow: "Install",
      title: "One binary, three ways to get it",
      lead: (t) => `The current release is ${t}. Every way below installs that one.`,
      binary: {
        title: "Download a binary",
        body: "Take the archive for your system from the release, check it against checksums.txt, and put jcloud on your PATH.",
        cta: "Open the release",
        checksums: "Each archive is listed with its SHA-256 in checksums.txt.",
      },
      go: {
        title: "Install with Go",
        body: "Any Go toolchain new enough for go.mod builds it from source.",
      },
      ci: {
        title: "Run it in GitHub Actions",
        body: "Where jcloud earns its keep: in the service's own repository, so the commit that moves a port without the manifest fails in front of whoever made it. The step downloads the release, checks it, and runs check --repo.",
        file: ".github/workflows/manifest.yml",
      },
      copy: { label: (what) => `Copy ${what}`, copy: "Copy", copied: "Copied" },
    },
    footer: {
      links: [
        { href: repoUrl, label: "Source" },
        { href: `${repoUrl}/releases`, label: "Releases" },
        { href: familyHref, label: "More from Japode" },
      ],
      disclaimer: "jcloud is a Japode product and open source. It reads files and reports; only the env verb writes one.",
    },
    // The banner's words are japode.com's own, since the two pages share one consent.
    consent: {
      label: "Analytics consent",
      body: "This site counts visits with Google Analytics only if you allow it. Nothing is loaded until you choose.",
      decline: "Decline",
      allow: "Allow analytics",
      settings: "Analytics settings",
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
    family: { pre: "parte da", title: "Japode: softwares pequenos que fazem uma coisa bem" },
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
    verbs: {
      eyebrow: "O que ele detecta",
      title: "Os erros que nenhuma outra ferramenta compara",
      lead: "O docker compose config e o caddy validate leem um arquivo cada. Uma porta escrita em três arquivos passa nos dois enquanto a rota aponta para o nada. Cada aba é um comando do jcloud num pequeno serviço de demonstração, com exatamente o que ele imprimiu.",
      tabsLabel: "comandos do jcloud",
      exit: (code) => `saída ${code}`,
      annotation: "No GitHub Actions, cada linha ::error vira uma marca no manifesto, dentro do pull request.",
      provenance: (version) => `Cada linha acima foi impressa pelo ${version}, rodando sobre os arquivos de site/demo.`,
      items: {
        check: {
          tab: "check",
          title: "Um primeiro rascunho",
          body: "O check lê um manifesto e recusa o que um serviço precisa declarar e não declara. Aqui: falta o caminho de health, uma rota não tem endereço e uma variável é fixa e tem padrão ao mesmo tempo. Todos os problemas saem de uma vez, então basta rodar uma vez.",
          files: ["japode.yml"],
        },
        repo: {
          tab: "check --repo",
          title: "No CI do próprio serviço",
          body: "Alguém mudou a porta para 3000 no Dockerfile e deixou o manifesto em 8080. O check --repo compara o manifesto com o Dockerfile, o Caddyfile e os workflows ao lado e falha naquele commit, na frente de quem o fez.",
          files: ["japode.yml", "Dockerfile", ".github/workflows/publish.yml"],
        },
        agree: {
          tab: "agree",
          title: "Na plataforma",
          body: "A rota ainda aponta para a porta 3000, e o .env.example guarda uma chave que o serviço deixou de ler. O agree compara o manifesto com o compose, com a rota como o Caddy a lê, com o backup.conf e com o .env.example.",
          files: ["services/notes/docker-compose.yml", "caddy/sites/notes.caddy", "services/notes/backup.conf", ".env.example"],
        },
        compare: {
          tab: "compare",
          title: "Duas cópias do mesmo manifesto",
          body: "A cópia da plataforma e a do próprio serviço, depois que o serviço mudou a porta. Comentários e ordem das chaves podem diferir entre elas. Um fato, não.",
          files: ["platform/services/notes/japode.yml", "compare/theirs.yml"],
        },
        up: {
          tab: "up",
          title: "Antes de um deploy",
          body: "O que separa um serviço de um deploy: uma variável que o manifesto declara e a configuração não tem, e nenhuma imagem fixada. O up só informa; ele não sobe nada.",
          files: ["services/notes/japode.yml", ".env"],
        },
        survey: {
          tab: "survey",
          title: "Contra o servidor",
          body: "O que um coletor viu no host, contra o que a plataforma declara: um contêiner rodando um digest diferente do fixado, e um contêiner que ninguém declara. O survey lê um arquivo, não um host.",
          files: ["survey/observations.txt", "docker-compose.yml", "services/*/japode.yml"],
        },
        env: {
          tab: "env",
          title: "A configuração",
          body: "As chaves ficam legíveis e cada valor é criptografado com age. O list não precisa da chave privada, então o CI confere quais chaves existem sem guardar segredo nenhum.",
          files: [".env.age"],
        },
      },
    },
    shape: {
      eyebrow: "Onde ele roda",
      title: "Um manifesto, três lugares que precisam concordar com ele",
      lead: "O manifesto fica no repositório do serviço e uma cópia fica no da plataforma. O jcloud roda nos dois, e contra o que o servidor informa.",
      places: [
        {
          name: "Repositório do serviço",
          where: "no CI dele, a cada pull request",
          files: ["japode.yml", "Dockerfile", "Caddyfile", ".github/workflows"],
          verbs: ["check", "check --repo"],
        },
        {
          name: "Repositório da plataforma",
          where: "no CI dela, e antes de um deploy",
          files: ["services/<nome>/japode.yml", "docker-compose.yml", "caddy/sites/<nome>.caddy", "backup.conf", ".env.example", ".env.age"],
          verbs: ["agree", "compare", "up", "env"],
        },
        {
          name: "Servidor",
          where: "lido por um coletor, nunca pelo jcloud",
          files: ["contêineres rodando", "digests fixados"],
          verbs: ["survey"],
        },
      ],
    },
    install: {
      eyebrow: "Instalar",
      title: "Um binário, três jeitos de obtê-lo",
      lead: (t) => `A versão atual é a ${t}. Todos os jeitos abaixo instalam essa versão.`,
      binary: {
        title: "Baixar um binário",
        body: "Pegue no release o arquivo do seu sistema, confira com o checksums.txt e coloque o jcloud no PATH.",
        cta: "Abrir o release",
        checksums: "Cada arquivo aparece com seu SHA-256 no checksums.txt.",
      },
      go: {
        title: "Instalar com Go",
        body: "Qualquer toolchain Go nova o bastante para o go.mod compila a partir do código.",
      },
      ci: {
        title: "Rodar no GitHub Actions",
        body: "É onde o jcloud mais vale: no repositório do próprio serviço, para que o commit que muda uma porta sem mudar o manifesto falhe na frente de quem o fez. O passo baixa o release, confere o checksum e roda o check --repo.",
        file: ".github/workflows/manifest.yml",
      },
      copy: { label: (what) => `Copiar ${what}`, copy: "Copiar", copied: "Copiado" },
    },
    footer: {
      links: [
        { href: repoUrl, label: "Código" },
        { href: `${repoUrl}/releases`, label: "Versões" },
        { href: familyHref, label: "Mais da Japode" },
      ],
      disclaimer: "O jcloud é um produto Japode e open source. Ele lê arquivos e informa; só o comando env grava um.",
    },
    consent: {
      label: "Consentimento de análise",
      body: "Este site conta as visitas com o Google Analytics só se você permitir. Nada é carregado antes da sua escolha.",
      decline: "Recusar",
      allow: "Permitir análise",
      settings: "Configurações de análise",
    },
  },
};
