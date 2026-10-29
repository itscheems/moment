import { describe, expect, it, vi } from "vitest";
import { parsePreferences, PreferencesApi } from "./preferences";

describe("private account preferences", () => {
  it("accepts only shipped languages or an unset choice", () => {
    for (const value of [null, "zh-CN", "en"]) {
      expect(parsePreferences({ ui_locale: value })).toEqual({
        uiLocale: value,
      });
    }
    for (const value of [
      {},
      { ui_locale: "fr" },
      { ui_locale: "" },
      { ui_locale: "en", user_id: "other" },
    ]) {
      expect(() => parsePreferences(value)).toThrow();
    }
  });

  it("sends the own-account update with CSRF and surfaces failure", async () => {
    const fetcher = vi.fn<typeof fetch>(
      async () =>
        new Response(JSON.stringify({ data: { ui_locale: "en" }, meta: {} }), {
          status: 200,
        }),
    );
    expect(
      await new PreferencesApi(fetcher).update("csrf-current", "en"),
    ).toEqual({ uiLocale: "en" });
    const [url, options] = fetcher.mock.calls[0];
    expect(url).toBe("/api/v1/account/preferences");
    expect(options?.method).toBe("PATCH");
    expect(new Headers(options?.headers).get("X-CSRF-Token")).toBe(
      "csrf-current",
    );
    expect(JSON.parse(String(options?.body))).toEqual({ ui_locale: "en" });
    const failure = new PreferencesApi(
      async () =>
        new Response(
          JSON.stringify({
            error: { code: "UNAUTHORIZED", message: "expired" },
          }),
          { status: 401 },
        ),
    );
    await expect(failure.update("ended", "en")).rejects.toMatchObject({
      code: "UNAUTHORIZED",
    });
  });
});
