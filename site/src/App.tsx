import type { Lang } from "./content";
import { LangContext } from "./i18n";
import { Footer, Nav } from "./components/Chrome";
import { Hero } from "./components/Hero";

// One page per language. The hero says what jcloud is and shows it run; the sections after
// it (what each verb catches, how to install it) are JCT12 and JCT13.
export function App({ lang }: Readonly<{ lang: Lang }>) {
  return (
    <LangContext.Provider value={lang}>
      <Nav />
      <main>
        <Hero />
      </main>
      <Footer />
    </LangContext.Provider>
  );
}
