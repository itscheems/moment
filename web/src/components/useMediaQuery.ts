import { useEffect, useState } from "react";

export const MOBILE_VIEWPORT_QUERY = "(max-width: 719px)";

// Where composing is a dialog over the page rather than a screen of its own.
// The same width the publish action asks about before it opens one, so the
// two can never disagree about which surface the reader is on.
export const COMPOSE_DIALOG_VIEWPORT_QUERY = "(min-width: 720px)";

// The width at which the shell draws its right rail (AppShell.module.css
// hides `.rightRail` below it).
export const RAIL_VIEWPORT_QUERY = "(min-width: 988px)";

export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() =>
    typeof window === "undefined" ? false : window.matchMedia(query).matches,
  );

  useEffect(() => {
    const media = window.matchMedia(query);
    const update = () => setMatches(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, [query]);

  return matches;
}
