import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ConfirmDialog } from "./ConfirmDialog";

describe("owner confirmation dialog", () => {
  beforeEach(() => {
    vi.spyOn(window, "scrollY", "get").mockReturnValue(330);
    vi.stubGlobal("scrollTo", vi.fn());
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("locks the document without selecting an action and returns focus on close", async () => {
    render(
      <ConfirmDialog
        confirmLabel="确认操作"
        description="这是一项需要确认的操作。"
        onConfirm={vi.fn()}
        title="继续操作？"
        trigger={<button type="button">打开确认</button>}
      />,
    );

    const trigger = screen.getByRole("button", { name: "打开确认" });
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = await screen.findByRole("dialog", { name: "继续操作？" });

    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
    await waitFor(() => expect(dialog).toHaveFocus());
    expect(screen.getByRole("button", { name: "保留" })).not.toHaveFocus();

    fireEvent.click(screen.getByRole("button", { name: "保留" }));
    await waitFor(() => expect(dialog).not.toBeInTheDocument());
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.body.style.position).toBe("");
    expect(document.documentElement.style.overflow).toBe("");
  });
});
