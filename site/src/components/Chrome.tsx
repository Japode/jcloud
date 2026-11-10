import { SiteButton, SiteFooter, SiteNav, SiteThemeToggle } from "@viglet/viglet-design-system/site";
import { asset, languages, pageHref, releasesUrl, repoUrl, THEME_KEY, words, type Lang } from "../content";
import { useLang, useWords } from "../i18n";

// The nav and the footer, drawn by the design system's site layer so jcloud sits in the same
// family as the other product sites. Everything between them is this product's own.

const other = (lang: Lang): Lang => (lang === "en" ? "pt" : "en");

export function Nav() {
  const lang = useLang();
  const w = useWords();
  const target = other(lang);
  return (
    <SiteNav
      brand={{ href: pageHref(lang), name: "jcloud", logo: asset("icon.svg") }}
      actions={
        <>
          <SiteButton href={releasesUrl} rel="noopener">
            {w.nav.cta}
          </SiteButton>
          {/* The other language, named in that language's own words for screen readers. */}
          <SiteButton
            variant="ghost"
            href={pageHref(target)}
            hrefLang={languages[target].htmlLang}
            lang={languages[target].htmlLang}
            aria-label={languages[lang].switchLabel}
            title={languages[lang].switchLabel}
          >
            {languages[lang].switchTo}
          </SiteButton>
          <SiteThemeToggle storageKey={THEME_KEY} label={w.nav.themeLabel} />
        </>
      }
    />
  );
}

export function Footer() {
  const lang = useLang();
  const w = words[lang];
  return (
    <SiteFooter
      brand={{ href: pageHref(lang), name: "jcloud", logo: asset("icon.svg") }}
      links={w.footer.links}
      disclaimer={
        <>
          {w.footer.disclaimer}{" "}
          <a href={repoUrl} rel="noopener">
            github.com/Japode/jcloud
          </a>
        </>
      }
    />
  );
}
