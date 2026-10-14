import { render } from "preact";
import { isMyGo, runtime } from "mygo-runtime";
import { App } from "./app";
import { matchLang, setLang, type Lang } from "./lib/i18n";
import { Preferences } from "./mygo";

if (isMyGo()) document.documentElement.dataset.platform = runtime().platform;

// The first paint is in the language of the app: the settings', else the
// system's.
async function start() {
  try {
    setLang(isMyGo() ? ((await Preferences.language()) as Lang) : matchLang(navigator.language));
  } catch {
    setLang(matchLang(navigator.language));
  }
  render(<App />, document.getElementById("app")!);
}
void start();
