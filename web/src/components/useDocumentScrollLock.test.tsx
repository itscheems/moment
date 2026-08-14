import "@testing-library/jest-dom/vitest";
import { cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useDocumentScrollLock } from "./useDocumentScrollLock";

function Lock({ active }: { active: boolean }) {
  useDocumentScrollLock(active);
  return null;
}

function LockHarness({ first, second }: { first: boolean; second: boolean }) {
  return (
    <>
      <Lock active={first} />
      <Lock active={second} />
    </>
  );
}

describe("document scroll lock", () => {
  beforeEach(() => {
    vi.spyOn(window, "scrollX", "get").mockReturnValue(12);
    vi.spyOn(window, "scrollY", "get").mockReturnValue(480);
    vi.stubGlobal("scrollTo", vi.fn());
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("keeps the document fixed until the last stacked modal closes", () => {
    const { rerender } = render(<LockHarness first second />);

    expect(document.documentElement.style.overflowY).toBe("hidden");
    expect(document.body.style.position).toBe("fixed");
    expect(document.body.style.top).toBe("-480px");
    expect(document.body.style.left).toBe("-12px");

    rerender(<LockHarness first={false} second />);
    expect(document.documentElement.style.overflowY).toBe("hidden");
    expect(document.body.style.position).toBe("fixed");
    expect(window.scrollTo).not.toHaveBeenCalled();

    rerender(<LockHarness first={false} second={false} />);
    expect(document.documentElement.style.overflowY).toBe("");
    expect(document.body.style.position).toBe("");
    expect(window.scrollTo).toHaveBeenCalledWith(12, 480);
  });
});
