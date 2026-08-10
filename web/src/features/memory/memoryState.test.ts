import { describe, expect, it } from "vitest";

import {
  activeSearchYear,
  archiveHref,
  nextDate,
  parseArchiveLocation,
  parseSearchLocation,
  searchHref,
} from "./memoryState";

describe("memory-layer URL state", () => {
  it("round-trips Unicode search terms and exclusive date boundaries", () => {
    const href = searchHref({
      query: "旧城 雨 + 100%",
      from: "2024-02-29",
      to: "2025-01-01",
      loadedThrough: "opaque+/=cursor",
    });
    expect(href).toContain("q=%E6%97%A7%E5%9F%8E+%E9%9B%A8+%2B+100%25");
    expect(parseSearchLocation(href.slice(href.indexOf("?")))).toEqual({
      query: "旧城 雨 + 100%",
      from: "2024-02-29",
      to: "2025-01-01",
      loadedThrough: "opaque+/=cursor",
      error: null,
    });
  });

  it("rejects duplicate, blank, impossible, reversed, and orphan cursor state", () => {
    for (const search of [
      "?q=a&q=b",
      "?q=%20%20",
      "?q=a&from=2026-02-30",
      "?q=a&from=2026-02-02&to=2026-02-01",
      "?loaded_through=cursor",
      "?q=a&unknown=1",
    ]) {
      expect(parseSearchLocation(search).error, search).not.toBeNull();
    }
  });

  it("recognizes only an exact annual search range", () => {
    expect(activeSearchYear("2026-01-01", "2027-01-01")).toBe(2026);
    expect(activeSearchYear("2026-01-02", "2027-01-01")).toBeNull();
  });

  it("keeps archive hierarchy and day range reconstructible", () => {
    const href = archiveHref({
      year: 2024,
      month: "2024-02",
      from: "2024-02-29",
      to: "2024-03-01",
    });
    expect(parseArchiveLocation(href.slice(href.indexOf("?")))).toEqual({
      year: 2024,
      month: "2024-02",
      from: "2024-02-29",
      to: "2024-03-01",
      loadedThrough: null,
      error: null,
    });
    expect(nextDate("2026-12-31")).toBe("2027-01-01");
  });

  it("rejects mismatched months, non-day ranges, and invalid archive pages", () => {
    for (const search of [
      "?year=2026&month=2025-12",
      "?month=0000-01",
      "?year=2026&month=2026-13",
      "?year=2026&month=2026-02&from=2026-02-28&to=2026-03-02",
      "?year=2026&from=2026-01-01&to=2026-01-02",
      "?loaded_through=cursor",
      "?year=2026&year=2025",
    ]) {
      expect(parseArchiveLocation(search).error, search).not.toBeNull();
    }
  });
});
