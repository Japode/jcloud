import { SiteButton, SiteCopyButton } from "@viglet/viglet-design-system/site";
import { install, releasesUrl, tag } from "../content";
import { useWords } from "../i18n";
import { Yaml } from "./Yaml";

// The call to action: the three ways to install the release the build found in git, and the
// GitHub Actions step that is jcloud's main home. Every command names that one release, so
// the page cannot point at a version that does not exist (JCT13).
export function Install() {
  const w = useWords().install;
  return (
    <section id="install" className="install">
      <div className="wrap">
        <div className="sec-head">
          <p className="eyebrow">{w.eyebrow}</p>
          <h2>{w.title}</h2>
          <p>{w.lead(tag)}</p>
        </div>
        <div className="ways">
          <article className="way">
            <h3>{w.binary.title}</h3>
            <p>{w.binary.body}</p>
            <ul className="files">
              {install.platforms.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
            <p className="way-note">{w.binary.checksums}</p>
            <div className="way-action">
              <SiteButton href={releasesUrl} rel="noopener">
                {w.binary.cta}
              </SiteButton>
            </div>
          </article>
          <article className="way">
            <h3>{w.go.title}</h3>
            <p>{w.go.body}</p>
            <div className="snippet">
              <pre className="code">{install.go}</pre>
              <SiteCopyButton text={install.go} label={w.copy.label("go install")} copyLabel={w.copy.copy} copiedLabel={w.copy.copied} />
            </div>
          </article>
          <article className="way way-wide">
            <h3>{w.ci.title}</h3>
            <p>{w.ci.body}</p>
            <div className="window">
              <div className="pane">
                <p className="pane-label">
                  <span>{w.ci.file}</span>
                  <SiteCopyButton text={install.ci} label={w.copy.label(w.ci.file)} copyLabel={w.copy.copy} copiedLabel={w.copy.copied} />
                </p>
                <pre className="code">
                  <Yaml code={install.ci} />
                </pre>
              </div>
            </div>
          </article>
        </div>
      </div>
    </section>
  );
}
