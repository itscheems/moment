import { AppShell } from "../components/AppShell";
import { ThemeProvider } from "../theme/ThemeProvider";
import { resolveRoute } from "./routes";

export function App() {
  const route = resolveRoute(window.location.pathname);
  const state = new URLSearchParams(window.location.search).get("state");

  return (
    <ThemeProvider>
      <AppShell route={route} state={state} />
    </ThemeProvider>
  );
}
