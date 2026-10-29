import { getLocale, t } from "./index";

export function formatNumber(
  value: number,
  options?: Intl.NumberFormatOptions,
): string {
  return new Intl.NumberFormat(getLocale(), options).format(value);
}

export function formatDate(
  value: string | number | Date,
  options: Intl.DateTimeFormatOptions,
): string {
  return new Intl.DateTimeFormat(getLocale(), options).format(new Date(value));
}

export function formatRelative(
  value: string,
  now: number,
  numeric: "auto" | "always" = "auto",
): string {
  const seconds = Math.round((Date.parse(value) - now) / 1000);
  const magnitude = Math.abs(seconds);
  if (magnitude < 5) return t("刚刚");
  const formatter = new Intl.RelativeTimeFormat(getLocale(), { numeric });
  if (magnitude < 60) return formatter.format(seconds, "second");
  if (magnitude < 3600)
    return formatter.format(Math.round(seconds / 60), "minute");
  if (magnitude < 86400)
    return formatter.format(Math.round(seconds / 3600), "hour");
  return formatter.format(Math.round(seconds / 86400), "day");
}
