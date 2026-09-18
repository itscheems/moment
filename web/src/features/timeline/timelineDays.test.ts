import { describe, expect, it } from "vitest";

import type { PublicPost } from "../../api/public";
import { dayHeading, siteDate, siteDayOf } from "./timelineDays";

function row(
  publishedAt: string,
  extra: Partial<PublicPost> = {},
): PublicPost {
  return {
    publishedAt,
    repostedAt: null,
    timelineItemKind: "original",
    ...extra,
  } as PublicPost;
}

describe("timeline days", () => {
  it("files an instant under the site's day, not UTC's", () => {
    // 16:20 UTC on the 31st is 00:20 on the 1st in Shanghai.
    expect(siteDate("2025-12-31T16:20:00Z", "Asia/Shanghai")).toBe(
      "2026-01-01",
    );
    expect(siteDate("2025-12-31T16:20:00Z", "UTC")).toBe("2025-12-31");
    // A zone the browser cannot resolve reads as UTC rather than throwing.
    expect(siteDate("2025-12-31T16:20:00Z", "Not/AZone")).toBe("2025-12-31");
  });

  it("files a repost under the day it was reposted", () => {
    const reposted = row("2019-03-03T02:00:00Z", {
      repostedAt: "2026-09-09T02:00:00Z",
      timelineItemKind: "repost",
    });
    expect(siteDayOf(reposted, "Asia/Shanghai")).toBe("2026-09-09");
    expect(
      siteDayOf(row("2019-03-03T02:00:00Z"), "Asia/Shanghai"),
    ).toBe("2019-03-03");
  });

  it("writes the weekday, and the year once it is not this one", () => {
    expect(dayHeading("2026-09-08", 2026)).toBe("9月8日 周二");
    expect(dayHeading("2025-12-31", 2026)).toBe("2025年12月31日 周三");
    expect(dayHeading("2024-02-29", 2026)).toBe("2024年2月29日 周四");
  });
});
