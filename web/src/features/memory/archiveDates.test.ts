import { describe, expect, it } from "vitest";

import {
  clampMonth,
  daysInMonth,
  mondayOffset,
  monthRange,
  newestMonthIn,
  pickerYear,
  positionAnchor,
  requestedMonth,
  shiftMonth,
  timezoneNote,
  yearsOf,
} from "./archiveDates";

const months = [
  { year: 2026, month: 8, count: 2 },
  { year: 2026, month: 2, count: 1 },
  { year: 2024, month: 2, count: 3 },
  { year: 2021, month: 10, count: 4 },
];

describe("archive dates", () => {
  it("does month arithmetic on labels, across year ends and leap years", () => {
    expect(shiftMonth("2026-01", -1)).toBe("2025-12");
    expect(shiftMonth("2025-12", 1)).toBe("2026-01");
    expect(daysInMonth("2024-02")).toBe(29);
    expect(daysInMonth("2026-02")).toBe(28);
    // 2026-09-01 is a Tuesday: one blank before it in a Monday-first grid.
    expect(mondayOffset("2026-09")).toBe(1);
    // 2026-11-01 is a Sunday: six blanks.
    expect(mondayOffset("2026-11")).toBe(6);
  });

  it("anchors the list at the last day of the position", () => {
    const base = {
      month: null,
      year: null,
      loadedThrough: null,
      loadedNewer: null,
      error: null,
    };
    expect(positionAnchor({ ...base, day: "2026-02-28" })).toBe("2026-02-28");
    expect(positionAnchor({ ...base, day: null, month: "2024-02" })).toBe(
      "2024-02-29",
    );
    expect(positionAnchor({ ...base, day: null, month: "2025-12" })).toBe(
      "2025-12-31",
    );
    expect(positionAnchor({ ...base, day: null, year: 2024 })).toBe(
      "2024-12-31",
    );
    expect(positionAnchor({ ...base, day: null })).toBeNull();
  });

  it("bounds the calendar by the archive and the site's current month", () => {
    expect(monthRange(months, "2026-09")).toEqual({
      oldest: "2021-10",
      newest: "2026-09",
    });
    // A clock behind the newest post must not put that post out of reach.
    expect(monthRange(months, "2026-07")).toEqual({
      oldest: "2021-10",
      newest: "2026-08",
    });
    expect(monthRange([], "2026-09")).toEqual({
      oldest: "2026-09",
      newest: "2026-09",
    });
    const range = { oldest: "2021-10", newest: "2026-09" };
    expect(clampMonth("2019-03", range)).toBe("2021-10");
    expect(clampMonth("2030-01", range)).toBe("2026-09");
    expect(clampMonth("2024-02", range)).toBe("2024-02");
  });

  it("seats the calendar on what the position names, else the newest month with posts", () => {
    const range = { oldest: "2021-10", newest: "2026-09" };
    const base = {
      month: null,
      year: null,
      loadedThrough: null,
      loadedNewer: null,
      error: null,
    };
    expect(requestedMonth({ ...base, day: null }, months, range)).toBe(
      "2026-08",
    );
    expect(requestedMonth({ ...base, day: "2024-02-29" }, months, range)).toBe(
      "2024-02",
    );
    expect(
      requestedMonth({ ...base, day: null, month: "2023-05" }, months, range),
    ).toBe("2023-05");
    // A year names its newest month with posts, and December when it has none.
    expect(
      requestedMonth({ ...base, day: null, year: 2026 }, months, range),
    ).toBe("2026-08");
    expect(
      requestedMonth({ ...base, day: null, year: 2023 }, months, range),
    ).toBe("2023-12");
    // Out of range positions land on the nearest end.
    expect(
      requestedMonth({ ...base, day: null, year: 2018 }, months, range),
    ).toBe("2021-10");
  });

  it("lists years newest first and picks the nearest listed year for the wheel", () => {
    expect(yearsOf(months)).toEqual([2026, 2024, 2021]);
    expect(newestMonthIn(2026, months)).toBe("2026-08");
    expect(newestMonthIn(2023, months)).toBeNull();
    expect(pickerYear("2024-06", [2026, 2024, 2021])).toBe(2024);
    // 2023 is not listed: 2024 and 2021 are one and two years away.
    expect(pickerYear("2023-01", [2026, 2024, 2021])).toBe(2024);
    // A tie goes to the later year, which is how the archive reads elsewhere.
    expect(pickerYear("2025-06", [2026, 2024])).toBe(2026);
    expect(pickerYear("2025-06", [])).toBeNull();
  });

  it("names the site's zone only when the reader's clock disagrees", () => {
    const now = new Date("2026-09-18T04:00:00Z");
    const local = now.getTimezoneOffset();
    // Whatever zone the test runs in, a zone eight hours from UTC and one
    // fourteen hours from it cannot both match it.
    const notes = [
      timezoneNote("Asia/Shanghai", now),
      timezoneNote("Pacific/Kiritimati", now),
    ];
    expect(notes.filter((note) => note !== null).length).toBeGreaterThan(0);
    if (local === -480) {
      expect(notes[0]).toBeNull();
    } else {
      expect(notes[0]).toBe("日期按站点时区 Asia/Shanghai");
    }
    expect(timezoneNote("Not/AZone", now)).toBeNull();
  });
});
