import type { Run } from "../content";

// A captured run as a terminal: the command after a prompt, then each line jcloud printed in
// the colour it printed it in. The lines come from src/transcripts.json and are laid out, never
// edited, so the page cannot show output the binary does not produce.
export function Terminal({ run, label, exitLabel }: Readonly<{ run: Run; label: string; exitLabel?: string }>) {
  return (
    <div className="pane pane-term">
      <p className="pane-label">
        <span>{label}</span>
        {exitLabel ? <span className={`exit ${run.exit === 0 ? "exit-ok" : "exit-err"}`}>{exitLabel}</span> : null}
      </p>
      <pre className="code term">
        <span className="t-prompt">
          <span className="t-dollar">$ </span>
          {run.cmd}
          {"\n"}
        </span>
        {run.lines.map((line, i) => (
          <span key={i} className={`t-${line.kind}`}>
            {line.text}
            {"\n"}
          </span>
        ))}
      </pre>
    </div>
  );
}
