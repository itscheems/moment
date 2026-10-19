import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useEscapeFocus } from "./useEscapeFocus";

function Harness() {
  useEscapeFocus();
  return (
    <>
      <button>打开</button>
      <button>返回焦点</button>
    </>
  );
}
afterEach(cleanup);

it("dismisses focus rings through Escape dismissal and restores them on the next interaction", () => {
  render(<Harness />);
  const first = screen.getByRole("button", { name: "打开" });
  const restored = screen.getByRole("button", { name: "返回焦点" });
  const escapeHandler = vi.fn();
  first.addEventListener("keydown", escapeHandler, { once: true });
  first.focus();
  fireEvent.keyDown(first, { key: "Escape" });
  expect(escapeHandler).toHaveBeenCalledOnce();
  restored.focus();
  expect(document.activeElement).toBe(restored);
  expect(document.documentElement.hasAttribute("data-focus-dismissed")).toBe(
    true,
  );
  fireEvent.keyDown(restored, { key: "Tab" });
  expect(document.documentElement.hasAttribute("data-focus-dismissed")).toBe(
    false,
  );
  fireEvent.keyDown(restored, { key: "Escape" });
  fireEvent.pointerDown(first);
  expect(document.documentElement.hasAttribute("data-focus-dismissed")).toBe(
    false,
  );
});
