import "@testing-library/jest-dom/vitest";
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

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
  afterEach(cleanup);

  it("holds the lock until the last stacked overlay closes", () => {
    const { rerender } = render(<LockHarness first second />);
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.documentElement.style.overscrollBehavior).toBe("none");

    rerender(<LockHarness first={false} second />);
    expect(document.documentElement.style.overflow).toBe("hidden");

    rerender(<LockHarness first={false} second={false} />);
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.documentElement.style.overscrollBehavior).toBe("");
  });

  it("never takes the body out of flow", () => {
    // Pinning the body with position: fixed is the common recipe and it
    // silently destroys every sticky element on the page: with no scrollport
    // left, the navigation rails and the timeline header fall back to their
    // document position and jump off-screen by the scroll offset.
    render(<LockHarness first second />);
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
    expect(document.body.style.overflow).toBe("");
  });

  it("leaves the scroll offset alone, so nothing has to be restored", () => {
    // The offset never moves while locked, which is why release does not call
    // scrollTo and cannot fight with a route's own scroll restoration.
    render(<LockHarness first second />);
    expect(document.documentElement.style.top).toBe("");
  });

  it("restores whatever inline styles the page already had", () => {
    document.documentElement.style.overflow = "clip";
    document.documentElement.style.paddingInlineEnd = "3px";
    const { rerender } = render(<LockHarness first second={false} />);
    expect(document.documentElement.style.overflow).toBe("hidden");

    rerender(<LockHarness first={false} second={false} />);
    expect(document.documentElement.style.overflow).toBe("clip");
    expect(document.documentElement.style.paddingInlineEnd).toBe("3px");
    document.documentElement.removeAttribute("style");
  });
});
