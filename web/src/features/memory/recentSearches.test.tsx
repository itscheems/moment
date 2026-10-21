import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  clearRecentSearches,
  recentSearchScope,
  rememberSearch,
  removeRecentSearch,
  useRecentSearches,
} from "./recentSearches";

const scope = recentSearchScope("instance", "account")!;
function History({ storageKey = scope }: { storageKey?: string }) {
  return <output>{JSON.stringify(useRecentSearches(storageKey))}</output>;
}

describe("recent searches", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.dispatchEvent(new StorageEvent("storage", { key: null }));
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("persists submitted queries, deduplicates them, and moves reuse to the front", () => {
    rememberSearch(scope, " 上海 ");
    rememberSearch(scope, "东京");
    rememberSearch(scope, "上海");
    render(<History />);
    expect(screen.getByRole("status").textContent).toBe('["上海","东京"]');
    expect(JSON.parse(window.localStorage.getItem(scope)!)).toEqual([
      "上海",
      "东京",
    ]);
    act(() => removeRecentSearch(scope, "上海"));
    expect(screen.getByRole("status").textContent).toBe('["东京"]');
    act(() => clearRecentSearches(scope));
    expect(screen.getByRole("status").textContent).toBe("[]");
    expect(window.localStorage.getItem(scope)).toBeNull();
  });

  it("keeps accounts, visitors, and separate instances apart", () => {
    rememberSearch(scope, "当前账号");
    for (const key of [
      recentSearchScope("instance", "other"),
      recentSearchScope("instance", null),
      recentSearchScope("other", "account"),
    ]) {
      expect(window.localStorage.getItem(key!)).toBeNull();
    }
    rememberSearch(null, "资料尚未加载");
    expect(window.localStorage.length).toBe(1);
  });

  it("bounds history and ignores invalid or empty input and malformed stored data", () => {
    window.localStorage.setItem(scope, '{"invalid":true}');
    rememberSearch(scope, "");
    rememberSearch(scope, "x".repeat(1_001));
    for (let i = 0; i < 25; i++) rememberSearch(scope, `关键词${i}`);
    const stored = JSON.parse(window.localStorage.getItem(scope)!);
    expect(stored).toHaveLength(20);
    expect(stored[0]).toBe("关键词24");
    expect(stored[19]).toBe("关键词5");
  });

  it("reacts to another tab clearing history", () => {
    rememberSearch(scope, "上海");
    render(<History />);
    act(() => {
      window.localStorage.removeItem(scope);
      window.dispatchEvent(new StorageEvent("storage", { key: scope }));
    });
    expect(screen.getByRole("status").textContent).toBe("[]");
  });

  it("keeps the current session usable when storage is denied", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("Blocked", "SecurityError");
    });
    rememberSearch(scope, "临时搜索");
    render(<History />);
    expect(screen.getByRole("status").textContent).toBe('["临时搜索"]');
  });
});
