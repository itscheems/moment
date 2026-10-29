import { getLocale } from "../../i18n";
import { formatRelative } from "../../i18n/format";
const dateFormats = new Map<string, Intl.DateTimeFormat>();
const yearFormats = new Map<string, Intl.DateTimeFormat>();

export function articleSavedAgo(value: string, now = Date.now()): string {
  return formatRelative(value, now, "always");
}

export function articlePublishedTime(
  value: string,
  timezone: string,
  now = Date.now(),
): string {
  const seconds = Math.max(1, Math.floor((now - Date.parse(value)) / 1000));
  if (seconds < 60)
    return new Intl.NumberFormat(getLocale(), {
      style: "unit",
      unit: "second",
      unitDisplay: "narrow",
    }).format(seconds);
  if (seconds < 3600)
    return new Intl.NumberFormat(getLocale(), {
      style: "unit",
      unit: "minute",
      unitDisplay: "narrow",
    }).format(Math.floor(seconds / 60));
  if (seconds < 86400)
    return new Intl.NumberFormat(getLocale(), {
      style: "unit",
      unit: "hour",
      unitDisplay: "narrow",
    }).format(Math.floor(seconds / 3600));
  let yearFormatter = yearFormats.get(timezone);
  if (!yearFormatter) {
    yearFormatter = new Intl.DateTimeFormat("en-US", {
      timeZone: timezone,
      year: "numeric",
    });
    yearFormats.set(timezone, yearFormatter);
  }
  const year = (instant: number) => yearFormatter.format(instant);
  const includeYear = year(Date.parse(value)) !== year(now);
  const key = `${getLocale()}:${timezone}:${includeYear}`;
  let formatter = dateFormats.get(key);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(getLocale(), {
      timeZone: timezone,
      month: "short",
      day: "numeric",
      ...(includeYear ? { year: "numeric" } : {}),
    });
    dateFormats.set(key, formatter);
  }
  return formatter.format(new Date(value));
}
