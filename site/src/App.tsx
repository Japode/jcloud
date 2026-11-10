import type { Lang } from "./content";
import { LangContext } from "./i18n";
import { Footer, Nav } from "./components/Chrome";
import { Hero } from "./components/Hero";
import { Install } from "./components/Install";
import { Shape } from "./components/Shape";
import { Verbs } from "./components/Verbs";

// One page per language, in the order a reader's questions arrive: what is it and does it
// work (hero), what does it catch (one real run per verb), and where does each run.
export function App({ lang }: Readonly<{ lang: Lang }>) {
  return (
    <LangContext.Provider value={lang}>
      <Nav />
      <main>
        <Hero />
        <Verbs />
        <Shape />
        <Install />
      </main>
      <Footer />
    </LangContext.Provider>
  );
}
