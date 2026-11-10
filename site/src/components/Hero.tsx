import { SiteButton } from "@viglet/viglet-design-system/site";
import { heroManifest, releasesUrl, repoUrl, transcripts } from "../content";
import { useWords } from "../i18n";
import { Terminal } from "./Terminal";

// The opening says what jcloud is in one sentence and then shows it: a manifest, the command,
// and what jcloud printed for it, captured from a release (src/transcripts.json).
export function Hero() {
  const hero = useWords().hero;
  return (
    <header className="hero">
      <div className="hero-glow" aria-hidden="true" />
      <div className="wrap hero-grid">
        <div className="hero-copy">
          <p className="eyebrow">{hero.eyebrow}</p>
          <h1>
            {hero.title} <span className="accent">{hero.titleAccent}</span>
          </h1>
          <p className="hero-sub">{hero.sub}</p>
          <div className="hero-actions">
            <SiteButton href={releasesUrl} rel="noopener">
              {hero.primary}
            </SiteButton>
            <SiteButton variant="ghost" href={repoUrl} rel="noopener">
              {hero.secondary}
            </SiteButton>
          </div>
          <ul className="hero-pills">
            {hero.pills.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        </div>
        <figure className="hero-stage">
          <div className="window">
            <div className="window-bar" aria-hidden="true">
              <span />
              <span />
              <span />
            </div>
            <div className="pane">
              <p className="pane-label">
                <span>{hero.manifestLabel}</span>
              </p>
              <pre className="code">{heroManifest.join("\n")}</pre>
            </div>
            <Terminal run={transcripts.runs.hero} label={hero.terminalLabel} />
          </div>
          <figcaption>{hero.caption}</figcaption>
        </figure>
      </div>
    </header>
  );
}
