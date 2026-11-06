import "@testing-library/jest-dom/vitest";
import { Editor } from "@tiptap/core";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { articleEditorExtensions } from "./schema";
import {
  TocProvider,
  useToc,
} from "@notion/components/nodes/toc-node/context/toc-context";

it("resolves the current heading and scrolls its panel when the cached DOM is detached", async () => {
  const editor = new Editor({
    extensions: articleEditorExtensions(),
    content: "<h2>Current heading</h2>",
  });
  await new Promise<void>((resolve) => editor.on("create", () => resolve()));
  const before = editor.getJSON();
  const heading = editor.view.nodeDOM(0) as HTMLElement;
  const panel = editor.view.dom;
  document.body.append(panel);
  panel.style.overflowY = "auto";
  Object.defineProperty(panel, "scrollHeight", {
    configurable: true,
    value: 2000,
  });
  Object.defineProperty(panel, "clientHeight", {
    configurable: true,
    value: 500,
  });
  const scroll = vi.fn();
  panel.scrollTo = scroll;
  vi.spyOn(heading, "getBoundingClientRect").mockReturnValue(
    new DOMRect(0, 1200, 600, 40),
  );
  vi.spyOn(panel, "getBoundingClientRect").mockReturnValue(
    new DOMRect(0, 0, 600, 500),
  );
  vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
    cb(0);
    return 1;
  });
  const windowScroll = vi
    .spyOn(window, "scrollTo")
    .mockImplementation(() => {});
  function Jump() {
    const { navigateToHeading } = useToc();
    return (
      <button
        onClick={() =>
          navigateToHeading(
            {
              id: "heading",
              textContent: "Current heading",
              level: 1,
              originalLevel: 2,
              itemIndex: 1,
              isActive: false,
              isScrolledOver: false,
              dom: document.createElement("h2"),
              editor,
              pos: 0,
            },
            { topOffset: 92, behavior: "auto" },
          )
        }
      >
        Jump
      </button>
    );
  }
  try {
    render(
      <TocProvider>
        <Jump />
      </TocProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Jump" }));
    expect(scroll).toHaveBeenCalled();
    expect(windowScroll).not.toHaveBeenCalled();
    expect(editor.getJSON()).toEqual(before);
    expect(editor.commands.undo()).toBe(false);
  } finally {
    cleanup();
    editor.destroy();
    panel.remove();
    vi.restoreAllMocks();
  }
});
