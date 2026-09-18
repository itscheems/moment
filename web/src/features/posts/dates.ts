const relativeFormatter = new Intl.RelativeTimeFormat("zh-CN", {
  numeric: "auto",
});

export interface MomentDate {
  absolute: string;
  dateMark: string;
  relative: string;
}

export function formatMomentDate(
  value: string,
  timezone: string,
  now = new Date(),
): MomentDate {
  const instant = new Date(value);
  const deltaSeconds = Math.round((instant.valueOf() - now.valueOf()) / 1000);
  const magnitude = Math.abs(deltaSeconds);
  let relative: string;
  if (magnitude < 45) {
    relative = "刚刚";
  } else if (magnitude < 60 * 60) {
    relative = relativeFormatter.format(
      Math.round(deltaSeconds / 60),
      "minute",
    );
  } else if (magnitude < 24 * 60 * 60) {
    relative = relativeFormatter.format(
      Math.round(deltaSeconds / 3600),
      "hour",
    );
  } else if (magnitude < 30 * 24 * 60 * 60) {
    relative = relativeFormatter.format(
      Math.round(deltaSeconds / (24 * 3600)),
      "day",
    );
  } else {
    // Include the year for earlier years so archived dates remain unambiguous.
    const yearOf = (value: Date) =>
      new Intl.DateTimeFormat("en-US", {
        year: "numeric",
        timeZone: timezone,
      }).format(value);
    relative = new Intl.DateTimeFormat("zh-CN", {
      ...(yearOf(instant) === yearOf(now) ? {} : { year: "numeric" }),
      month: "short",
      day: "numeric",
      timeZone: timezone,
    }).format(instant);
  }

  const absolute = new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "long",
    timeStyle: "short",
    timeZone: timezone,
  }).format(instant);
  const parts = new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    timeZone: timezone,
  }).formatToParts(instant);
  const month = parts.find((part) => part.type === "month")?.value ?? "--";
  const day = parts.find((part) => part.type === "day")?.value ?? "--";
  return { absolute, dateMark: `${month}.${day}`, relative };
}
