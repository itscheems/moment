import { describe, expect, it } from "vitest";

import {
  activeSearchYear,
  archiveHref,
  nextDate,
  parseArchiveLocation,
  parseSearchLocation,
  previousDate,
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

  it("rejects malformed percent escapes and invalid UTF-8 before decoding", () => {
    for (const search of ["?q=%", "?q=%GG", "?q=%E0%A4%A"]) {
      const result = parseSearchLocation(search);
      expect(result.error, search).not.toBeNull();
      expect(result.query, search).toBeNull();
    }
    for (const search of ["?year=2026%", "?month=%ED%A0%80"]) {
      const result = parseArchiveLocation(search);
      expect(result.error, search).not.toBeNull();
      expect(result.month, search).toBeNull();
    }
  });

  it("recognizes only an exact annual search range", () => {
    expect(activeSearchYear("2026-01-01", "2027-01-01")).toBe(2026);
    expect(activeSearchYear("2026-01-02", "2027-01-01")).toBeNull();
  });

  it("names an archive position by its most specific part", () => {
    const settled = { loadedThrough: null, error: null };
    expect(archiveHref({ day: "2024-02-29" })).toBe("/archive?day=2024-02-29");
    expect(parseArchiveLocation("?day=2024-02-29")).toEqual({
      day: "2024-02-29",
      month: null,
      year: null,
      ...settled,
    });
    expect(archiveHref({ month: "2024-02" })).toBe("/archive?month=2024-02");
    expect(parseArchiveLocation("?month=2024-02")).toEqual({
      day: null,
      month: "2024-02",
      year: null,
      ...settled,
    });
    expect(archiveHref({ year: 2024 })).toBe("/archive?year=2024");
    expect(parseArchiveLocation("?year=2024&loaded_through=cursor")).toEqual({
      day: null,
      month: null,
      year: 2024,
      loadedThrough: "cursor",
      error: null,
    });
    expect(archiveHref({})).toBe("/archive");
    expect(parseArchiveLocation("")).toEqual({
      day: null,
      month: null,
      year: null,
      ...settled,
    });
    expect(nextDate("2026-12-31")).toBe("2027-01-01");
    expect(previousDate("2027-01-01")).toBe("2026-12-31");
  });

  it("still opens the links the archive used to write, on their day", () => {
    // `year=&month=&from=D&to=D+1` named one day as a window; it is the day.
    expect(
      parseArchiveLocation(
        "?year=2024&month=2024-02&from=2024-02-29&to=2024-03-01",
      ),
    ).toEqual({
      day: "2024-02-29",
      month: null,
      year: null,
      loadedThrough: null,
      error: null,
    });
    // A year and its month agree, so both survive as the month.
    expect(parseArchiveLocation("?year=2026&month=2026-08").month).toBe(
      "2026-08",
    );
  });

  it("rejects positions whose parts disagree, and windows that are not one day", () => {
    for (const search of [
      "?year=2026&month=2025-12",
      "?month=0000-01",
      "?year=2026&month=2026-13",
      "?day=2026-02-30",
      "?month=2026-01&day=2026-02-01",
      "?year=2025&day=2026-02-01",
      "?year=2026&month=2026-02&from=2026-02-28&to=2026-03-02",
      "?from=2026-01-01",
      "?day=2026-01-02&from=2026-01-01&to=2026-01-02",
      "?loaded_through=",
      "?year=2026&year=2025",
      "?state=error",
    ]) {
      expect(parseArchiveLocation(search).error, search).not.toBeNull();
    }
  });
});
