import type { ReactNode } from "react";

// A YAML highlighter for the two snippets the page shows, a manifest and a workflow, and no
// more: keys, list dashes, comments, quoted strings, numbers, GitHub expressions and the
// shell variables inside a `run: |` block. A full grammar library would be most of the page's
// JavaScript for two code blocks. It colours and never rewrites, so the text a reader copies
// is the text that was tested.

const KEY = /^(\s*)(- )?([A-Za-z_][\w-]*)(:)(\s|$)(.*)$/;
const DASH = /^(\s*)(- )(.*)$/;
// One pass over a value: a GitHub expression, a quoted string, a shell variable, a number.
const VALUE = /(\$\{\{[^}]*\}\})|("(?:[^"\\]|\\.)*"|'[^']*')|(\$\{?[A-Za-z_][A-Za-z0-9_]*\}?)|(\b\d+(?:\.\d+)*\b)/g;

function value(text: string, key: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(VALUE)) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const cls = m[1] ? "y-expr" : m[2] ? "y-str" : m[3] ? "y-var" : "y-num";
    out.push(
      <span key={`${key}-${m.index}`} className={cls}>
        {m[0]}
      </span>,
    );
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

function line(text: string, i: number, inBlock: boolean): ReactNode {
  const k = `l${i}`;
  if (/^\s*#/.test(text)) return <span className="y-comment">{text}</span>;
  // Inside `run: |` every line is shell, so only values are coloured, never a "key".
  if (!inBlock) {
    const m = KEY.exec(text);
    if (m) {
      const [, indent, dash, name, colon, space, rest] = m;
      return (
        <>
          {indent}
          {dash ? <span className="y-dash">{dash}</span> : null}
          <span className="y-key">{name}</span>
          <span className="y-punct">{colon}</span>
          {space}
          {rest === "|" ? <span className="y-punct">|</span> : value(rest, k)}
        </>
      );
    }
    const d = DASH.exec(text);
    if (d) {
      return (
        <>
          {d[1]}
          <span className="y-dash">{d[2]}</span>
          {value(d[3], k)}
        </>
      );
    }
  }
  return <>{value(text, k)}</>;
}

/** The snippet, highlighted, as the children of a <pre>. */
export function Yaml({ code }: Readonly<{ code: string }>) {
  const lines = code.split("\n");
  let block = -1; // the indent a `run: |` block's own key sits at, or -1 outside one
  return (
    <>
      {lines.map((text, i) => {
        const indent = text.length - text.trimStart().length;
        if (block >= 0 && text.trim() !== "" && indent <= block) block = -1;
        const node = line(text, i, block >= 0);
        if (block < 0 && /:\s*\|\s*$/.test(text)) block = indent;
        return (
          <span key={i}>
            {node}
            {i < lines.length - 1 ? "\n" : null}
          </span>
        );
      })}
    </>
  );
}
