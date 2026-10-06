import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Editor } from "@tiptap/core";
import { EditorContext } from "@tiptap/react";
import { FindAndReplace } from "@tiptap/extension-find-and-replace";
import { CellSelection } from "@tiptap/pm/tables";
import { articleContentExtensions } from "../../../components/content/schema";
import { ArticleSheet } from "../../../components/content/ArticleSheet";
import { NavigationProvider } from "../../../app/navigation";
import { MentionProvider } from "../../../app/mentions";
import { ColorActionAdapterContext } from "@notion/hooks/use-color-action-adapter";
import { TextStyleColorPanel } from "@notion/components/ui/color-text-popover";
import { ArticleReadingContent } from "./ArticleReadingContent";
import type { ArticleDocument } from "../../../api/public";
import {
  ReadingAnnotations,
  activeReadingColor,
  applyReadingColor,
  clearReadingAnnotations,
  readingSelection,
  readingSelectionText,
} from "./readingAnnotations";

let editor: Editor;
beforeEach(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
});
afterEach(() => {
  cleanup();
  editor?.destroy();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function create(
  content = '<p><mark data-color="#ffee00" style="background-color:#ffee00"><span style="color:#cc0000">abcdefghij</span></mark></p><p>klmnop</p>',
) {
  editor = new Editor({
    editable: false,
    extensions: [
      ...articleContentExtensions(),
      ReadingAnnotations,
      FindAndReplace.configure({ injectCSS: false }),
    ],
    content,
  });
  return editor;
}

describe("temporary reading decorations", () => {
  it("overlays independent colors, splits overlaps and clears only intersecting ranges without touching author marks", () => {
    create();
    const before = editor.getJSON();
    const html = editor.getHTML();
    const update = vi.fn();
    editor.on("update", update);
    const write = vi.spyOn(Storage.prototype, "setItem");
    applyReadingColor(
      editor,
      { from: 1, to: 9 },
      "highlight",
      "var(--tt-color-highlight-blue)",
    );
    applyReadingColor(
      editor,
      { from: 4, to: 7 },
      "highlight",
      "var(--tt-color-highlight-green)",
    );
    applyReadingColor(
      editor,
      { from: 3, to: 10 },
      "text",
      "var(--tt-color-text-blue)",
    );
    expect(activeReadingColor(editor, { from: 4, to: 7 }, "highlight")).toBe(
      "var(--tt-color-highlight-green)",
    );
    expect(
      activeReadingColor(editor, { from: 1, to: 8 }, "highlight"),
    ).toBeUndefined();
    expect(
      editor.view.dom.querySelector('[data-reading-annotation="text"]'),
    ).not.toBeNull();
    clearReadingAnnotations(editor, { from: 5, to: 8 });
    expect(editor.storage.readingAnnotations.ranges).toEqual(
      expect.arrayContaining([
        {
          from: 1,
          to: 4,
          type: "highlight",
          color: "var(--tt-color-highlight-blue)",
        },
        {
          from: 4,
          to: 5,
          type: "highlight",
          color: "var(--tt-color-highlight-green)",
        },
        {
          from: 8,
          to: 9,
          type: "highlight",
          color: "var(--tt-color-highlight-blue)",
        },
        { from: 3, to: 5, type: "text", color: "var(--tt-color-text-blue)" },
        { from: 8, to: 10, type: "text", color: "var(--tt-color-text-blue)" },
      ]),
    );
    clearReadingAnnotations(editor);
    expect(
      editor.view.dom.querySelector("[data-reading-annotation]"),
    ).toBeNull();
    expect(editor.view.dom.querySelector("mark")).toHaveStyle({
      backgroundColor: "#ffee00",
    });
    expect(editor.view.dom.querySelector("span[style*=color]")).toHaveStyle({
      color: "#cc0000",
    });
    expect(editor.getJSON()).toEqual(before);
    expect(editor.getHTML()).toBe(html);
    expect(editor.isEditable).toBe(false);
    expect(update).not.toHaveBeenCalled();
    expect(write).not.toHaveBeenCalled();
  });

  it("supports cross-paragraph colors, default colors and toggling without removing the other channel", () => {
    create();
    const range = { from: 5, to: 16 };
    applyReadingColor(editor, range, "highlight", "blue");
    applyReadingColor(editor, range, "text", "red");
    expect(
      editor.view.dom.querySelectorAll("[data-reading-annotation]"),
    ).toHaveLength(2);
    applyReadingColor(editor, range, "text", "var(--tt-color-text)");
    expect(activeReadingColor(editor, range, "highlight")).toBe("blue");
    expect(activeReadingColor(editor, range, "text")).toBeUndefined();
    applyReadingColor(editor, range, "highlight", "blue");
    expect(editor.storage.readingAnnotations.ranges).toHaveLength(0);
    applyReadingColor(editor, range, "highlight", "green");
    applyReadingColor(editor, range, "highlight", "var(--tt-bg-color)");
    expect(editor.storage.readingAnnotations.ranges).toHaveLength(0);
  });

  it("copies only the selected slice with paragraph and hard-break newlines", () => {
    create("<p>prefix abc<br>def suffix</p><p>next text</p>");
    editor.commands.setTextSelection({ from: 8, to: 25 });
    expect(readingSelectionText(editor, readingSelection(editor)!)).toBe(
      "abc\ndef suffix\nn",
    );
  });

  it("excludes images, code blocks and whole-table selections", () => {
    create(
      '<p>text</p><pre><code>const x = 1;</code></pre><img src="/image.png"><table><tbody><tr><td><p>cell</p></td></tr></tbody></table>',
    );
    editor.commands.setTextSelection(2);
    expect(readingSelection(editor)).toBeNull();
    const positions: Record<string, number> = {};
    editor.state.doc.descendants((node, pos) => {
      if (node.type.name !== "text") positions[node.type.name] = pos;
    });
    editor.commands.setTextSelection({
      from: positions.codeBlock + 1,
      to: positions.codeBlock + 6,
    });
    expect(readingSelection(editor)).toBeNull();
    editor.commands.setNodeSelection(positions.image);
    expect(readingSelection(editor)).toBeNull();
    editor.view.dispatch(
      editor.state.tr.setSelection(
        CellSelection.create(editor.state.doc, positions.tableCell),
      ),
    );
    expect(readingSelection(editor)).toBeNull();
  });

  it("reuses the native palette once per click, with memory-only recent colors", async () => {
    create();
    const write = vi.spyOn(Storage.prototype, "setItem");
    const added = vi.fn();
    const applied = vi.fn();
    render(
      <EditorContext.Provider value={{ editor }}>
        <ColorActionAdapterContext.Provider
          value={{
            editor,
            canApply: true,
            activeColor: (type) =>
              activeReadingColor(editor, { from: 2, to: 6 }, type),
            applyColor: (type, value) => {
              applyReadingColor(editor, { from: 2, to: 6 }, type, value);
              return true;
            },
            recentColors: [],
            addRecentColor: added,
          }}
        >
          <TextStyleColorPanel onColorChanged={applied} />
        </ColorActionAdapterContext.Provider>
      </EditorContext.Provider>,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Blue background highlight color" }),
    );
    expect(applied).toHaveBeenCalledTimes(1);
    expect(added).toHaveBeenCalledTimes(1);
    expect(write).not.toHaveBeenCalled();
    expect(activeReadingColor(editor, { from: 2, to: 6 }, "highlight")).toBe(
      "var(--tt-color-highlight-blue)",
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Default background highlight color",
      }),
    );
    expect(editor.storage.readingAnnotations.ranges).toHaveLength(0);
    expect(editor.isEditable).toBe(false);
  });

  it("keeps native editing color commands when no reading adapter is supplied", async () => {
    create();
    editor.setEditable(true);
    editor.commands.setTextSelection({ from: 2, to: 6 });
    render(
      <EditorContext.Provider value={{ editor }}>
        <TextStyleColorPanel />
      </EditorContext.Provider>,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Blue background highlight color" }),
    );
    await waitFor(() =>
      expect(
        editor.isActive("highlight", {
          color: "var(--tt-color-highlight-blue)",
        }),
      ).toBe(true),
    );
    expect(editor.storage.readingAnnotations.ranges).toHaveLength(0);
  });
});

it("preserves the reading instance across equal-body rerenders and search, resetting only on actual body change", async () => {
  const body: ArticleDocument = {
    schema_version: 2,
    type: "doc",
    content: [
      {
        type: "paragraph",
        content: [{ type: "text", text: "the river flows" }],
      },
    ],
  };
  const show = (document: ArticleDocument) => (
    <NavigationProvider>
      <MentionProvider known={[]}>
        <ArticleSheet title="title" byline="author">
          <ArticleReadingContent document={document} enableAnnotations />
        </ArticleSheet>
      </MentionProvider>
    </NavigationProvider>
  );
  const mounted = render(show(body));
  const current = () =>
    (
      document.querySelector("[data-content-renderer]") as HTMLElement & {
        editor: Editor;
      }
    ).editor;
  await waitFor(() => expect(current()).toBeDefined());
  const original = current();
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal(
    "navigator",
    new Proxy(navigator, {
      get: (target, key) =>
        key === "clipboard" ? { writeText } : Reflect.get(target, key, target),
    }),
  );
  act(() => original.commands.setTextSelection({ from: 5, to: 8 }));
  const copy = await screen.findByRole("button", { name: /^Copy$/ });
  await act(async () => fireEvent.click(copy));
  expect(writeText).toHaveBeenCalledExactlyOnceWith("riv");
  expect(original.getText()).toBe("the river flows");
  act(() =>
    applyReadingColor(original, { from: 1, to: 5 }, "highlight", "blue"),
  );
  mounted.rerender(show(structuredClone(body)));
  expect(current()).toBe(original);
  act(() => {
    original.commands.setSearchTerm("river");
    original.commands.updateDecorations();
  });
  expect(original.storage.readingAnnotations.ranges).toHaveLength(1);
  expect(original.getText()).toBe("the river flows");
  const changed: ArticleDocument = {
    ...body,
    content: [
      { type: "paragraph", content: [{ type: "text", text: "new body" }] },
    ],
  };
  mounted.rerender(show(changed));
  await waitFor(() => expect(current()).not.toBe(original));
  expect(current().storage.readingAnnotations.ranges).toHaveLength(0);
  mounted.unmount();
  render(show(body));
  await waitFor(() =>
    expect(current().storage.readingAnnotations.ranges).toHaveLength(0),
  );
});
