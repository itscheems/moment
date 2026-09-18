import { useEffect, useState } from "react";

export const MOBILE_VIEWPORT_QUERY = "(max-width: 719px)";

// The width at which the shell draws its right rail (AppShell.module.css
// hides `.rightRail` below it).
export const RAIL_VIEWPORT_QUERY = "(min-width: 1280px)";

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
