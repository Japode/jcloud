import { useState, type KeyboardEvent } from "react";
import { transcripts, verbIds, type VerbId } from "../content";
import { useWords } from "../i18n";
import { Terminal } from "./Terminal";

// One tab per verb, in the order a service meets them. The first is selected in the
// prerendered page, so a reader without JavaScript still sees one whole run; the arrows move
// between tabs as the ARIA tabs pattern expects.
export function Verbs() {
  const v = useWords().verbs;
  const [active, setActive] = useState<VerbId>("check");
  const item = v.items[active];
  const run = transcripts.runs[active];

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    if (!step) return;
    e.preventDefault();
    const next = verbIds[(verbIds.indexOf(active) + step + verbIds.length) % verbIds.length];
    setActive(next);
    document.getElementById(`tab-${next}`)?.focus();
  };

  return (
    <section id="verbs" className="verbs">
      <div className="wrap">
        <div className="sec-head">
          <p className="eyebrow">{v.eyebrow}</p>
          <h2>{v.title}</h2>
          <p>{v.lead}</p>
        </div>
        <div className="tabs" role="tablist" aria-label={v.tabsLabel} onKeyDown={onKey}>
          {verbIds.map((id) => (
            <button
              key={id}
              id={`tab-${id}`}
              type="button"
              role="tab"
              aria-selected={id === active}
              aria-controls="verb-panel"
              tabIndex={id === active ? 0 : -1}
              className="tab"
              onClick={() => setActive(id)}
            >
              {v.items[id].tab}
            </button>
          ))}
        </div>
        <div id="verb-panel" role="tabpanel" aria-labelledby={`tab-${active}`} className="verb-panel">
          <div className="verb-copy">
            <h3>{item.title}</h3>
            <p>{item.body}</p>
            <ul className="files">
              {item.files.map((f) => (
                <li key={f}>{f}</li>
              ))}
            </ul>
            {run.lines.some((l) => l.kind === "annotation") ? <p className="note">{v.annotation}</p> : null}
          </div>
          <div className="window verb-window">
            <Terminal run={run} label={run.cwd} exitLabel={v.exit(run.exit)} />
          </div>
        </div>
        <p className="provenance">{v.provenance(transcripts.version)}</p>
      </div>
    </section>
  );
}
