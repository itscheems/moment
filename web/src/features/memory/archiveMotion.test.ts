import { afterEach, describe, expect, it } from "vitest";

import {
  archiveJumpDirection,
  archiveWindowSearch,
  rememberArchiveWindow,
} from "./archiveMotion";

afterEach(() => {
  window.history.replaceState({}, "", "/");
});

describe("archive jump direction", () => {
  it.each([
    ["2026-06-14", "2026-06-02", "older"],
    ["2026-06-02", "2026-06-14", "newer"],
    ["2026-01-01", "2025-12-31", "older"],
    ["2025-12-31", "2026-01-01", "newer"],
    ["2026-06-02", null, "newer"],
    ["2026-06-02", "2026-06-02", null],
  ] as const)("compares %s with %s as %s", (from, to, expected) => {
    expect(archiveJumpDirection(from, to)).toBe(expected);
  });
});

it("restores the origin and pagination of a reused archive window", () => {
  rememberArchiveWindow("?anchor=2025-03-31&loaded_through=saved");
  expect(archiveWindowSearch("?anchor=2026-06-14&loaded_through=saved")).toBe(
    "?anchor=2025-03-31&loaded_through=saved",
  );
  rememberArchiveWindow("?loaded_through=saved");
  expect(archiveWindowSearch("?anchor=2026-06-14")).toBe(
    "?loaded_through=saved",
  );
});
