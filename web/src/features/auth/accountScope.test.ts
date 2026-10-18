import { describe, it, expect, afterEach } from "vitest";
import { accountCacheScope } from "./accountScope";
import {
  createComposeDraft,
  saveComposeDraft,
  loadComposeDraft,
  clearComposeDraft,
} from "../compose/draft";
afterEach(() => localStorage.clear());
describe("account browser data", () => {
  it("separates accounts on the same instance and the same account on different instances", () => {
    const instance = "a".repeat(32),
      first = "b".repeat(32),
      second = "c".repeat(32);
    const one = accountCacheScope(instance, first),
      two = accountCacheScope(instance, second),
      other = accountCacheScope("d".repeat(32), first);
    expect(new Set([one, two, other]).size).toBe(3);
    const draft = createComposeDraft("only first account");
    saveComposeDraft(one, draft);
    expect(loadComposeDraft(two)).toBeNull();
    expect(loadComposeDraft(other)).toBeNull();
    expect(loadComposeDraft(one)?.contentMd).toBe("only first account");
    clearComposeDraft(two, { reason: "logout" });
    expect(loadComposeDraft(one)?.contentMd).toBe("only first account");
  });
  it("does not create a sensitive namespace before both identities are known", () => {
    expect(accountCacheScope("a".repeat(32), null)).toBeNull();
    expect(accountCacheScope(null, "b".repeat(32))).toBeNull();
  });
});
