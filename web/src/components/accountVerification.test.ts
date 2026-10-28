import { afterEach, expect, it, vi } from "vitest";

afterEach(() => {
  vi.unstubAllEnvs();
  vi.resetModules();
});

it("uses configured stable account identifiers in place of the default selection", async () => {
  vi.stubEnv("VITE_VERIFIED_ACCOUNT_IDS", ` ${"d".repeat(32)} , `);
  vi.resetModules();
  const { accountVerification } = await import("./accountVerification");
  expect(accountVerification({ userId: "d".repeat(32) })).toBe("blue");
  expect(
    accountVerification({ userId: "11d003f31a23262ea0f6cbe9857981a1" }),
  ).toBeNull();
});

it("allows a deployment to omit all verification marks", async () => {
  vi.stubEnv("VITE_VERIFIED_ACCOUNT_IDS", "");
  vi.resetModules();
  const { accountVerification } = await import("./accountVerification");
  expect(
    accountVerification({ userId: "11d003f31a23262ea0f6cbe9857981a1" }),
  ).toBeNull();
});
