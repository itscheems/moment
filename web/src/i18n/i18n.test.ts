import { describe, expect, it } from "vitest";
import i18n, { browserLocale, getLocale, t } from "./index";
import { formatNumber } from "./format";
import en from "./locales/en.json";
import zh from "./locales/zh-CN.json";
import { formatMomentDate } from "../features/posts/dates";
import { articleSavedAgo } from "../features/article/articleDates";
import { siteDate } from "../features/timeline/timelineDays";

describe("application languages", () => {
  it("matches browser languages in preference order and handles unsupported input", () => {
    expect(browserLocale(["fr-FR", "en-GB", "zh-CN"])).toBe("en");
    expect(browserLocale(["zh-Hans-CN", "en-US"])).toBe("zh-CN");
    expect(browserLocale([])).toBe("zh-CN");
    expect(browserLocale(["fr-FR"])).toBe("zh-CN");
  });

  it("ships matching complete resources with preserved interpolation variables", () => {
    expect(Object.keys(en).sort()).toEqual(Object.keys(zh).sort());
    const chinese: Record<string, string> = zh;
    for (const [key, value] of Object.entries(en)) {
      expect(value, key).not.toMatch(/[\u3400-\u9fff]/);
      const variables = (text: string) =>
        [...text.matchAll(/{{\s*([^}]+?)\s*}}/g)]
          .map((match) => match[1])
          .sort();
      expect(variables(value), key).toEqual(variables(chinese[key]));
    }
  });

  it("changes display formatting while preserving the site's date partition", async () => {
    const date = "2025-12-31T23:30:00Z";
    const now = new Date("2025-12-31T23:32:00Z");
    const day = siteDate(date, "Asia/Shanghai");
    await i18n.changeLanguage("en");
    expect(getLocale()).toBe("en");
    expect(t("主页")).toBe("Home");
    expect(formatMomentDate(date, "Asia/Shanghai", now).relative).toBe(
      "2 minutes ago",
    );
    expect(articleSavedAgo(date, now.valueOf())).toBe("2 minutes ago");
    expect(formatNumber(12345)).toBe("12,345");
    expect(t("回复 {{value1}}", { value1: "@reader" })).toBe(
      "Reply to @reader",
    );
    await i18n.changeLanguage("zh-CN");
    expect(formatMomentDate(date, "Asia/Shanghai", now).relative).toBe(
      "2分钟前",
    );
    expect(articleSavedAgo(date, now.valueOf())).toBe("2分钟前");
    expect(siteDate(date, "Asia/Shanghai")).toBe(day);
    expect(day).toBe("2026-01-01");
  });
});
