import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { Editor } from "@tiptap/core";
import { NodeSelection } from "@tiptap/pm/state";
import { contentExtensions } from "../../../components/content/schema";
import {
  TocProvider,
  useToc,
} from "@notion/components/nodes/toc-node/context/toc-context";

beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

it("keeps the editor's real node selection after two seconds", () => {
  const editor = new Editor({
    extensions: contentExtensions.article,
    content: "<h1>第一章</h1><h2>第二章</h2>",
  });
  const heading = editor.view.dom.querySelector("h1")!;
  const Navigate = () => {
    const { navigateToHeading } = useToc();
    return (
      <button
        onClick={() =>
          navigateToHeading({
            id: "first",
            textContent: "第一章",
            level: 1,
            originalLevel: 1,
            itemIndex: 1,
            dom: heading,
            editor,
            pos: 0,
            node: editor.state.doc.firstChild!,
            isActive: false,
            isScrolledOver: false,
          })
        }
      >
        跳转
      </button>
    );
  };
  const view = render(
    <TocProvider>
      <Navigate />
    </TocProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "跳转" }));
  expect(editor.state.selection).toBeInstanceOf(NodeSelection);
  expect(heading).toHaveClass("ProseMirror-selectednode");
  act(() => vi.advanceTimersByTime(2_100));
  expect(heading).toHaveClass("ProseMirror-selectednode");
  view.unmount();
  expect(heading).toHaveClass("ProseMirror-selectednode");
  editor.destroy();
});
