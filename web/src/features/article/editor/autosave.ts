import { t } from "../../../i18n";
import { formatMomentDate } from "../../posts/dates";

/**
 * When a draft is written back.
 *
 * Two clocks, and the earlier one wins. The idle clock is what makes saving
 * feel like it is not happening: it restarts on every keystroke, so a paragraph
 * typed straight through is one save at the end of it rather than one per
 * letter. The ceiling is what makes it trustworthy: a writer who never stops
 * long enough to go idle -- and, writing prose, that is most of them -- still
 * has their work on the server within fifteen seconds of touching it.
 */
export const AUTOSAVE_IDLE_MS = 2_000;
export const AUTOSAVE_MAX_MS = 15_000;

/**
 * The instant a draft with unsaved work is due to be written.
 *
 * `firstChangeAt` is the first change since the last successful save, not the
 * first change ever: the ceiling is a promise about how long work can sit
 * unsaved, so it restarts every time the draft reaches the server.
 */
export function autosaveDueAt(
  firstChangeAt: number,
  lastChangeAt: number,
): number {
  return Math.min(
    lastChangeAt + AUTOSAVE_IDLE_MS,
    firstChangeAt + AUTOSAVE_MAX_MS,
  );
}

/** How long to wait from `now`, never negative -- a due save is due now. */
export function autosaveDelay(
  firstChangeAt: number,
  lastChangeAt: number,
  now: number,
): number {
  return Math.max(0, autosaveDueAt(firstChangeAt, lastChangeAt) - now);
}

export type SaveState =
  /** Nothing has been written since this draft was opened. */
  | { kind: "idle" }
  | { kind: "dirty" }
  | { kind: "saving" }
  | { kind: "saved"; at: string }
  | { kind: "failed"; message: string }
  | { kind: "conflict"; serverUpdatedAt: string | null };

/**
 * The line in the status bar.
 *
 * `savedAt` is worded the way every other instant on this site is -- "刚刚",
 * "3分钟前" -- so that a reader who has learned what a timestamp means here
 * does not have to learn a second dialect in the editor.
 */
export function saveStatusLabel(
  state: SaveState,
  timezone: string,
  now = new Date(),
): string {
  switch (state.kind) {
    case "idle":
      return t("草稿");
    case "dirty":
      return t("草稿 · 有改动未保存");
    case "saving":
      return t("草稿 · 正在保存…");
    case "saved":
      return t("草稿 · {{value1}}保存", {
        value1: formatMomentDate(state.at, timezone, now).relative,
      });
    case "failed":
      return t("草稿 · {{value1}}", { value1: state.message });
    case "conflict":
      return t("草稿 · 别处改过了");
  }
}
