import { render } from "preact";
import { isMyGo, runtime } from "mygo-runtime";
import { App } from "./app";

if (isMyGo()) document.documentElement.dataset.platform = runtime().platform;

render(<App />, document.getElementById("app")!);
