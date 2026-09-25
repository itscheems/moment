import { describe, expect, it } from "vitest";

import {
  AUTOSAVE_IDLE_MS,
  AUTOSAVE_MAX_MS,
  autosaveDelay,
  autosaveDueAt,
  saveStatusLabel,
} from "./autosave";

describe("when a draft is written back", () => {
  it("waits for the typing to stop rather than saving per keystroke", () => {
    const start = 1_000;
    expect(autosaveDueAt(start, start)).toBe(start + AUTOSAVE_IDLE_MS);
    // Another keystroke a second later pushes the save out, not forward.
    expect(autosaveDueAt(start, start + 1_000)).toBe(
      start + 1_000 + AUTOSAVE_IDLE_MS,
    );
  });

  it("saves anyway once the ceiling is reached, however fast the typing", () => {
    const start = 1_000;
    // Still typing at fourteen seconds: idle would say 16s, the ceiling says 15.
    expect(autosaveDueAt(start, start + 14_000)).toBe(start + AUTOSAVE_MAX_MS);
  });

  it("asks for no wait at all once the save is due", () => {
    const start = 1_000;
    expect(autosaveDelay(start, start, start + AUTOSAVE_MAX_MS)).toBe(0);
    expect(autosaveDelay(start, start, start)).toBe(AUTOSAVE_IDLE_MS);
  });
});

describe("the line in the status bar", () => {
  const timezone = "Asia/Shanghai";
  const now = new Date("2026-09-25T12:00:00Z");

  it("says how long ago the draft reached the server", () => {
    expect(
      saveStatusLabel(
        { kind: "saved", at: "2026-09-25T11:57:00Z" },
        timezone,
        now,
      ),
    ).toBe("草稿 · 3分钟前保存");
  });

  it("says nothing about a time it has not got", () => {
    expect(saveStatusLabel({ kind: "idle" }, timezone, now)).toBe("草稿");
    expect(saveStatusLabel({ kind: "dirty" }, timezone, now)).toBe(
      "草稿 · 有改动未保存",
    );
    expect(saveStatusLabel({ kind: "saving" }, timezone, now)).toBe(
      "草稿 · 正在保存…",
    );
  });

  it("carries a refusal and a conflict in the same place", () => {
    expect(
      saveStatusLabel(
        { kind: "failed", message: "保存没有成功。" },
        timezone,
        now,
      ),
    ).toBe("草稿 · 保存没有成功。");
    expect(
      saveStatusLabel(
        { kind: "conflict", serverUpdatedAt: null },
        timezone,
        now,
      ),
    ).toBe("草稿 · 别处改过了");
  });
});
