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
});
