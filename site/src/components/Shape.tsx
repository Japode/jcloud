import { useWords } from "../i18n";

// Where each verb runs: the service's repository, the platform's, and the server. Drawn as
// three cards joined by arrows in HTML rather than an SVG, so it reflows on a phone and takes
// both themes from the same tokens as the rest of the page.
export function Shape() {
  const s = useWords().shape;
  return (
    <section id="shape" className="shape">
      <div className="wrap">
        <div className="sec-head">
          <p className="eyebrow">{s.eyebrow}</p>
          <h2>{s.title}</h2>
          <p>{s.lead}</p>
        </div>
        <ol className="places">
          {s.places.map((p) => (
            <li key={p.name} className="place">
              <h3>{p.name}</h3>
              <p className="place-where">{p.where}</p>
              <ul className="files">
                {p.files.map((f) => (
                  <li key={f}>{f}</li>
                ))}
              </ul>
              <p className="place-verbs">
                {p.verbs.map((verb) => (
                  <code key={verb}>jcloud {verb}</code>
                ))}
              </p>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}
