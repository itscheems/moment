import type { PublicPost } from "../../api/public";
import { timelineInstant } from "./timelineItems";

/**
 * Which site-calendar day a row belongs to, and how that day is written over
 * it. The zone is the site's, never the reader's: the rows sit under a heading
 * that names a site day, and a row stamped by the reader's clock under a
 * heading written by the site's would contradict itself on one screen.
 */

const WEEKDAYS = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

const dateFormatters = new Map<string, Intl.DateTimeFormat>();

function dateFormatter(timezone: string): Intl.DateTimeFormat {
  let formatter = dateFormatters.get(timezone);
  if (!formatter) {
    const options: Intl.DateTimeFormatOptions = {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    };
    try {
      formatter = new Intl.DateTimeFormat("en-US", {
        ...options,
        timeZone: timezone,
      });
    } catch {
      // A zone the browser cannot resolve falls back to UTC rather than taking
      // the page down over a formatting detail; the server validated it.
      formatter = new Intl.DateTimeFormat("en-US", {
        ...options,
        timeZone: "UTC",
      });
    }
    dateFormatters.set(timezone, formatter);
  }
  return formatter;
}

/** The site-calendar day an instant falls on, as `YYYY-MM-DD`. */
export function siteDate(instant: string | Date, timezone: string): string {
  const parts = dateFormatter(timezone).formatToParts(
    instant instanceof Date ? instant : new Date(instant),
  );
  const value = (type: Intl.DateTimeFormatPartTypes) =>
    parts.find((part) => part.type === type)?.value ?? "";
  return `${value("year")}-${value("month")}-${value("day")}`;
}

export function siteToday(timezone: string, now = new Date()): string {
  return siteDate(now, timezone);
}

/** The day a timeline row appeared on, in the site's calendar. */
export function siteDayOf(post: PublicPost, timezone: string): string {
  return siteDate(timelineInstant(post), timezone);
}

/**
 * The heading over one day of a list: `9月8日 周二`, with the year in front
 * once the day is not in the current one.
 */
export function dayHeading(date: string, todayYear: number): string {
  const [year, month, day] = date.split("-").map(Number);
  const weekday = WEEKDAYS[new Date(Date.UTC(year, month - 1, day)).getUTCDay()];
  const dayLabel = `${month}月${day}日 ${weekday}`;
  return year === todayYear ? dayLabel : `${year}年${dayLabel}`;
}
