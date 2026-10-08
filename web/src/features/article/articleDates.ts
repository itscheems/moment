const relative = new Intl.RelativeTimeFormat("en-US", { numeric: "always" });
const dateFormats = new Map<string, Intl.DateTimeFormat>();
const yearFormats = new Map<string, Intl.DateTimeFormat>();

export function articleSavedAgo(value: string, now = Date.now()): string {
  const seconds = Math.max(0, Math.floor((now - Date.parse(value)) / 1000));
  if (seconds < 5) return "just now";
  if (seconds < 60) return relative.format(-seconds, "second");
  if (seconds < 3600)
    return relative.format(-Math.floor(seconds / 60), "minute");
  if (seconds < 86400)
    return relative.format(-Math.floor(seconds / 3600), "hour");
  return relative.format(-Math.floor(seconds / 86400), "day");
}

export function articlePublishedTime(
  value: string,
  timezone: string,
  now = Date.now(),
): string {
  const seconds = Math.max(1, Math.floor((now - Date.parse(value)) / 1000));
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
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
  const key = `${timezone}:${includeYear}`;
  let formatter = dateFormats.get(key);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat("en-US", {
      timeZone: timezone,
      month: "short",
      day: "numeric",
      ...(includeYear ? { year: "numeric" } : {}),
    });
    dateFormats.set(key, formatter);
  }
  return formatter.format(new Date(value));
}
