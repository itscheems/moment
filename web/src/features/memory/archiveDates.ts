import type { ArchiveMonthCount } from "../../api/public";
import { nextDate, type ArchiveLocationState } from "./memoryState";

/**
 * Label arithmetic for the archive, in the site's calendar.
 *
 * Every function here works on the `YYYY-MM` and `YYYY-MM-DD` strings the
 * server aggregates by, which are site-timezone labels rather than instants. A
 * `Date` only ever appears at GMT midnight, where a label has no zone to shift
 * under it. Instants meet the site's zone in `timelineDays.ts`, which asks
 * `Intl` rather than doing the arithmetic itself.
 */

export function monthKeyOf(item: Pick<ArchiveMonthCount, "year" | "month">) {
  return `${String(item.year).padStart(4, "0")}-${String(item.month).padStart(2, "0")}`;
}

export function splitMonth(month: string): { year: number; month: number } {
  return { year: Number(month.slice(0, 4)), month: Number(month.slice(5, 7)) };
}

export function shiftMonth(month: string, delta: number): string {
  const { year, month: index } = splitMonth(month);
  const shifted = new Date(Date.UTC(year, index - 1 + delta, 1));
  return monthKeyOf({
    year: shifted.getUTCFullYear(),
    month: shifted.getUTCMonth() + 1,
  });
}

export function daysInMonth(month: string): number {
  const { year, month: index } = splitMonth(month);
  return new Date(Date.UTC(year, index, 0)).getUTCDate();
}

/** Leading blanks before the 1st in a Monday-first grid. */
export function mondayOffset(month: string): number {
  const { year, month: index } = splitMonth(month);
  return (new Date(Date.UTC(year, index - 1, 1)).getUTCDay() + 6) % 7;
}

/**
 * The bound the list reads back from: the first instant *after* the position.
 *
 * A day, a month or a year is where the list starts, not what it is filtered
 * to, so the window is open below -- `/posts?to=` and nothing else. Null when
 * the archive is opened without a position, which starts at the newest post.
 */
export function positionEnd(state: ArchiveLocationState): string | null {
  if (state.day) return nextDate(state.day);
  if (state.month) return `${shiftMonth(state.month, 1)}-01`;
  if (state.year !== null) {
    return `${String(state.year + 1).padStart(4, "0")}-01-01`;
  }
  return null;
}

/**
 * How far the calendar may page: from the oldest month that holds a post to
 * the later of the newest such month and the site's current month. Months in
 * between stay reachable even when empty.
 */
export function monthRange(
  months: readonly ArchiveMonthCount[],
  todayMonth: string,
): { oldest: string; newest: string } {
  const keys = months.map(monthKeyOf);
  const newest = keys.reduce(
    (best, key) => (key > best ? key : best),
    todayMonth,
  );
  const oldest = keys.reduce((best, key) => (key < best ? key : best), newest);
  return { oldest, newest };
}

export function clampMonth(
  month: string,
  range: { oldest: string; newest: string },
): string {
  if (month < range.oldest) return range.oldest;
  if (month > range.newest) return range.newest;
  return month;
}

/** The newest month of one year that holds posts, or null. */
export function newestMonthIn(
  year: number,
  months: readonly ArchiveMonthCount[],
): string | null {
  const keys = months
    .filter((item) => item.year === year)
    .map(monthKeyOf)
    .sort();
  return keys.length > 0 ? keys[keys.length - 1] : null;
}

/**
 * The month the calendar opens on for a position: the day's own month, the
 * month named, a year's newest month with posts, or -- with no position -- the
 * newest month that holds anything. Never an empty current month when the
 * site's newest post is years old: the archive's job is to show what is there.
 */
export function requestedMonth(
  state: ArchiveLocationState,
  months: readonly ArchiveMonthCount[],
  range: { oldest: string; newest: string },
): string {
  let wanted: string;
  if (state.day) {
    wanted = state.day.slice(0, 7);
  } else if (state.month) {
    wanted = state.month;
  } else if (state.year !== null) {
    wanted =
      newestMonthIn(state.year, months) ??
      monthKeyOf({ year: state.year, month: 12 });
  } else {
    wanted = months.length > 0 ? monthKeyOf(months[0]) : range.newest;
  }
  return clampMonth(wanted, range);
}

/** Years that hold posts, newest first. */
export function yearsOf(months: readonly ArchiveMonthCount[]): number[] {
  return Array.from(new Set(months.map((item) => item.year))).sort(
    (left, right) => right - left,
  );
}

/**
 * Which year the picker opens on: the visible month's own when it holds posts,
 * else the nearest year that does, the later one on a tie. The chevrons page
 * through empty months on purpose, so the month on screen can belong to a year
 * the picker does not list.
 */
export function pickerYear(
  visibleMonth: string,
  years: readonly number[],
): number | null {
  if (years.length === 0) return null;
  const wanted = splitMonth(visibleMonth).year;
  if (years.includes(wanted)) return wanted;
  return years.reduce((best, year) => {
    const distance = Math.abs(year - wanted);
    const bestDistance = Math.abs(best - wanted);
    return distance < bestDistance || (distance === bestDistance && year > best)
      ? year
      : best;
  });
}

function offsetMinutes(timezone: string, now: Date): number | null {
  try {
    const parts = new Intl.DateTimeFormat("en-US", {
      timeZone: timezone,
      hourCycle: "h23",
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    }).formatToParts(now);
    const value = (type: Intl.DateTimeFormatPartTypes) =>
      Number(parts.find((part) => part.type === type)?.value);
    const local = Date.UTC(
      value("year"),
      value("month") - 1,
      value("day"),
      value("hour"),
      value("minute"),
    );
    const truncated =
      now.getTime() - now.getSeconds() * 1000 - now.getMilliseconds();
    return Math.round((local - truncated) / 60_000);
  } catch {
    return null;
  }
}

/**
 * One line saying whose calendar this is, or null when the reader's clock and
 * the site's agree right now. Most readers of a one-person site are its owner,
 * and a line that always says the same thing is noise they learn not to see.
 */
export function timezoneNote(timezone: string, now = new Date()): string | null {
  const site = offsetMinutes(timezone, now);
  if (site === null || site === -now.getTimezoneOffset()) return null;
  return `日期按站点时区 ${timezone}`;
}
