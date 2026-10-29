import i18n from "../../i18n";
import { beforeEach } from "vitest";
beforeEach(() => {
  void i18n.changeLanguage("en");
});
import { describe, expect, it } from "vitest";
import { articlePublishedTime, articleSavedAgo } from "./articleDates";

const now = Date.parse("2026-10-08T12:00:00Z");
describe("article workspace English timestamps", () => {
  it.each([
    [0, "just now"],
    [6, "6 seconds ago"],
    [90, "1 minute ago"],
    [14 * 3600, "14 hours ago"],
    [2 * 86400, "2 days ago"],
  ])("formats a draft saved %s seconds ago", (seconds, expected) => {
    expect(
      articleSavedAgo(new Date(now - seconds * 1000).toISOString(), now),
    ).toBe(expected);
  });
  it.each([
    [34, "34s"],
    [43 * 60, "43m"],
    [14 * 3600, "14h"],
  ])("formats a recent publication %s seconds ago", (seconds, expected) => {
    expect(
      articlePublishedTime(
        new Date(now - seconds * 1000).toISOString(),
        "Asia/Shanghai",
        now,
      ),
    ).toBe(expected);
  });
  it("uses English dates and only includes a different calendar year", () => {
    expect(
      articlePublishedTime("2026-09-24T10:00:00Z", "Asia/Shanghai", now),
    ).toBe("Sep 24");
    expect(
      articlePublishedTime("2025-09-24T10:00:00Z", "Asia/Shanghai", now),
    ).toBe("Sep 24, 2025");
    expect(
      articlePublishedTime(
        "2025-12-31T23:30:00Z",
        "Asia/Shanghai",
        Date.parse("2026-01-03T00:00:00Z"),
      ),
    ).toBe("Jan 1");
  });
});
