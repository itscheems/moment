import { getLocale } from "../../i18n";
import { t } from "../../i18n";
export function formatInstant(value: string | null, timezone: string): string {
  if (!value) return t("尚无记录");
  try {
    return new Intl.DateTimeFormat(getLocale(), {
      dateStyle: "medium",
      timeStyle: "short",
      timeZone: timezone,
    }).format(new Date(value));
  } catch {
    return new Intl.DateTimeFormat(getLocale(), {
      dateStyle: "medium",
      timeStyle: "short",
      timeZone: "UTC",
    }).format(new Date(value));
  }
}
