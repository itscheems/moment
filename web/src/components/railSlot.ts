import { createContext, useContext } from "react";

/**
 * Where a page may put something in the right rail.
 *
 * The rail belongs to the shell and a page renders inside `<main>`, so a page
 * that wants the rail -- the archive, for its calendar -- reaches it through a
 * portal into this element. Null when the shell draws no rail: below 1280 px,
 * and on the search page, whose rail has nothing to say.
 */
export const RailSlotContext = createContext<HTMLElement | null>(null);

export function useRailSlot(): HTMLElement | null {
  return useContext(RailSlotContext);
}
