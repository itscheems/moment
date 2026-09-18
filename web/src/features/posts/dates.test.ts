import { describe, expect, it } from "vitest";

import { formatMomentDate } from "./dates";

describe("Moment dates", () => {
  it("pairs relative time with the configured site's absolute date", () => {
    const date = formatMomentDate(
      "2026-08-10T01:00:00.000Z",
      "Asia/Shanghai",
      new Date("2026-08-10T01:30:00.000Z"),
    );
    expect(date.relative).toContain("30");
    expect(date.absolute).toContain("2026");
    expect(date.absolute).toContain("9:00");
    expect(date.dateMark).toBe("08.10");
  });

  it("writes the year only once the date is not in this one", () => {
    const now = new Date("2026-09-18T04:00:00.000Z");
    expect(
      formatMomentDate("2026-06-21T09:20:00.000Z", "Asia/Shanghai", now)
        .relative,
    ).toBe("6月21日");
    expect(
      formatMomentDate("2024-11-11T15:59:00.000Z", "Asia/Shanghai", now)
        .relative,
    ).toBe("2024年11月11日");
    // The site's year, not UTC's: 16:20 UTC on the 31st is already next year
    // in Shanghai.
    expect(
      formatMomentDate("2025-12-31T16:20:00.000Z", "Asia/Shanghai", now)
        .relative,
    ).toBe("1月1日");
  });
});
