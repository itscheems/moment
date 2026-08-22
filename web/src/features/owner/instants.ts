/**
 * Render a server instant in the site's own timezone.
 *
 * The site timezone, not the reader's: these are facts about the instance, and
 * an owner reading them from a hotel abroad should still see the times their
 * own records use. A timezone the browser cannot resolve falls back to UTC
 * rather than throwing a settings page away over a formatting detail.
 */
export function formatInstant(value: string | null, timezone: string): string {
  if (!value) return "尚无记录";
  try {
    return new Intl.DateTimeFormat("zh-CN", {
      dateStyle: "medium",
      timeStyle: "short",
      timeZone: timezone,
    }).format(new Date(value));
  } catch {
    return new Intl.DateTimeFormat("zh-CN", {
      dateStyle: "medium",
      timeStyle: "short",
      timeZone: "UTC",
    }).format(new Date(value));
  }
}
