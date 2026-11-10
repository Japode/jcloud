import { SiteButton } from "@viglet/viglet-design-system/site";
import { heroRun, releasesUrl, repoUrl } from "../content";
import { useWords } from "../i18n";

// The opening says what jcloud is in one sentence and then shows it: a manifest, the command,
// and what jcloud printed for it. The run is real (see heroRun), so the page cannot promise
// output the binary does not produce.
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
              <p className="pane-label">{hero.manifestLabel}</p>
              <pre className="code" aria-label={hero.manifestLabel}>
                {heroRun.manifest.map((line, i) => (
                  <span key={i} className="code-line">
                    {line}
                    {"\n"}
                  </span>
                ))}
              </pre>
            </div>
            <div className="pane pane-term">
              <p className="pane-label">{hero.terminalLabel}</p>
              <pre className="code term">
                {heroRun.terminal.map((line, i) => (
                  <span key={i} className={`t-${line.kind}`}>
                    {line.kind === "prompt" ? <span className="t-dollar">$ </span> : null}
                    {line.text}
                    {"\n"}
                  </span>
                ))}
              </pre>
            </div>
          </div>
          <figcaption>{hero.caption}</figcaption>
        </figure>
      </div>
    </header>
  );
}
