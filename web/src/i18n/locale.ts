export type UILocale = "zh-CN" | "en";

export function isUILocale(value: unknown): value is UILocale {
  return value === "zh-CN" || value === "en";
}
