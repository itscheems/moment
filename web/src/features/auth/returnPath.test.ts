import { describe, expect, it } from "vitest";
import { loginHref } from "./returnPath";

describe("shared sign-in destination", () => {
  it.each([
    "/settings/account",
    "/compose?quote=post#reply",
    "/m/post/comments/reply?view=thread",
  ])("preserves the complete local return path %s", (path) => {
    expect(
      new URLSearchParams(loginHref(path).split("?")[1]).get("return"),
    ).toBe(path);
  });
  it.each([
    "https://outside.invalid/path",
    "//outside.invalid/path",
    "/\\outside.invalid",
    "/ok\nno",
  ])("refuses unsafe return input %s", (path) => {
    expect(loginHref(path)).toBe("/login?return=%2F");
  });
  it("avoids a login/register return loop", () => {
    expect(loginHref("/login?return=%2Fsettings%2Faccount")).toBe(
      "/login?return=%2Fsettings%2Faccount",
    );
    expect(loginHref("/register?return=%2Flogin")).toBe("/login?return=%2F");
  });
});
