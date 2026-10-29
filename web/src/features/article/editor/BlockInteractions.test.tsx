import "@testing-library/jest-dom/vitest";
import { Editor } from "@tiptap/core";
import { EditorContent } from "@tiptap/react";
import { NodeRangeSelection } from "@tiptap/extension-node-range";
import { DOMParser } from "@tiptap/pm/model";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { articleEditorExtensions } from "./schema";
import { toArticleDocument } from "./document";

let editor: Editor;
afterEach(() => {
  cleanup();
  editor?.destroy();
});

const mediaId = "01K00000000000000000000000";
function setup() {
  editor = new Editor({
    extensions: articleEditorExtensions(),
    content: {
      type: "doc",
      content: [
        { type: "paragraph", content: [{ type: "text", text: "Before" }] },
        {
          type: "image",
          attrs: { media_id: mediaId, src: `/api/v1/media/${mediaId}/preview` },
        },
        { type: "paragraph", content: [{ type: "text", text: "After" }] },
      ],
    },
  });
}

it("registers upstream block range selection for mouse and keyboard selection", () => {
  setup();
  expect(
    editor.extensionManager.extensions.find(
      (extension) => extension.name === "nodeRange",
    )?.options,
  ).toMatchObject({ key: null });
  editor.commands.setTextSelection(1);
  editor.view.dom.dispatchEvent(
    new KeyboardEvent("keydown", {
      key: "ArrowDown",
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    }),
  );
  expect(editor.state.selection).toBeInstanceOf(NodeRangeSelection);
});

it("exposes the upstream drag handle on the image surface without including its caption", async () => {
  setup();
  render(<EditorContent editor={editor} />);
  await waitFor(() =>
    expect(
      editor.view.dom.querySelector(".tiptap-image-content"),
    ).toHaveAttribute("data-drag-handle"),
  );
  expect(
    editor.view.dom.querySelector(".tiptap-image-content"),
  ).toHaveAttribute("draggable", "true");
  expect(
    editor.view.dom.querySelector(".tiptap-image-caption[data-drag-handle]"),
  ).toBeNull();
});

it("retains managed media when the actual editor serializes a copied block range", () => {
  setup();
  const selection = NodeRangeSelection.create(
    editor.state.doc,
    0,
    editor.state.doc.content.size,
  );
  editor.view.dispatch(editor.state.tr.setSelection(selection));
  const { dom } = editor.view.serializeForClipboard(
    editor.state.selection.content(),
  );
  expect(dom.querySelector("img")?.getAttribute("src")).toContain(
    `/api/v1/media/${mediaId}/preview`,
  );
  const restored = DOMParser.fromSchema(editor.schema).parse(dom);
  expect(toArticleDocument(restored.toJSON())).toEqual(
    toArticleDocument(editor.getJSON()),
  );
});

it("keeps image chrome noneditable while captions follow editor editability", async () => {
  setup();
  editor.commands.setContent({
    type: "doc",
    content: [
      {
        type: "image",
        attrs: {
          media_id: mediaId,
          src: `/api/v1/media/${mediaId}/preview`,
          showCaption: true,
        },
        content: [{ type: "text", text: "Caption" }],
      },
    ],
  });
  render(<EditorContent editor={editor} />);
  await waitFor(() =>
    expect(
      editor.view.dom.querySelector(
        ".tiptap-image-caption [data-node-view-content-react]",
      ),
    ).toHaveAttribute("contenteditable", "true"),
  );
  expect(editor.view.dom.querySelector(".tiptap-image")).toHaveAttribute(
    "contenteditable",
    "false",
  );
  act(() => editor.setEditable(false));
  await waitFor(() =>
    expect(
      editor.view.dom.querySelector(
        ".tiptap-image-caption [data-node-view-content-react]",
      ),
    ).toHaveAttribute("contenteditable", "false"),
  );
  act(() => editor.setEditable(true));
  await waitFor(() =>
    expect(
      editor.view.dom.querySelector(
        ".tiptap-image-caption [data-node-view-content-react]",
      ),
    ).toHaveAttribute("contenteditable", "true"),
  );
});

it("does not leave a text caret inside a captionless image", () => {
  setup();
  const imagePos = editor.state.doc.firstChild!.nodeSize;
  editor.commands.setTextSelection(imagePos + 1);
  expect(editor.state.selection.toJSON()).toEqual({
    type: "node",
    anchor: imagePos,
  });
});

it.each([0, 3, 7])(
  "exits a caption on Enter without splitting its image (offset %s)",
  (offset) => {
    setup();
    editor.commands.setContent({
      type: "doc",
      content: [
        {
          type: "image",
          attrs: {
            media_id: mediaId,
            src: `/api/v1/media/${mediaId}/preview`,
            showCaption: true,
          },
          content: [{ type: "text", text: "Caption" }],
        },
      ],
    });
    const original = toArticleDocument(editor.getJSON()).content[0];
    editor.commands.setTextSelection(1 + offset);
    editor.view.dom.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }),
    );
    const saved = toArticleDocument(editor.getJSON());
    expect(saved.content.map((node) => node.type)).toEqual([
      "image",
      "paragraph",
    ]);
    expect(saved.content[0]).toEqual(original);
    expect(editor.state.selection.$from.parent.type.name).toBe("paragraph");
  },
);

it("keeps the caption content DOM mounted and hidden without creating an editable fallback", async () => {
  setup();
  render(<EditorContent editor={editor} />);
  await waitFor(() =>
    expect(
      editor.view.dom.querySelector(".tiptap-image-caption"),
    ).toHaveAttribute("hidden"),
  );
  expect(
    editor.view.dom.querySelector(".tiptap-image-caption"),
  ).toHaveAttribute("contenteditable", "false");
  expect(
    editor.view.dom.querySelector(
      ".node-image > [data-node-view-content-react]",
    ),
  ).toBeNull();
});

it.each([false, true])(
  "keeps uploaded media and captions through repeated HTML copies (caption=%s)",
  (caption) => {
    setup();
    editor.commands.setContent({
      type: "doc",
      content: [
        {
          type: "image",
          attrs: {
            src: `/api/v1/media/${mediaId}/preview`,
            showCaption: caption,
          },
          ...(caption
            ? { content: [{ type: "text", text: "Image description" }] }
            : {}),
        },
      ],
    });
    for (let copy = 0; copy < 2; copy += 1) {
      editor.commands.setNodeSelection(0);
      const { dom } = editor.view.serializeForClipboard(
        editor.state.selection.content(),
      );
      const restored = DOMParser.fromSchema(editor.schema).parse(dom);
      expect(restored.firstChild?.type.name).toBe("image");
      expect(restored.firstChild?.attrs.media_id).toBe(mediaId);
      expect(restored.firstChild?.textContent).toBe(
        caption ? "Image description" : "",
      );
      editor.commands.setContent(restored.toJSON());
    }
  },
);

it.each([null, "shiftKey", "metaKey", "ctrlKey", "altKey"] as const)(
  "collapses a clicked empty paragraph without overriding selection modifiers (%s)",
  (modifier) => {
    setup();
    const emptyPos =
      editor.state.doc.firstChild!.nodeSize +
      editor.state.doc.child(1).nodeSize;
    editor.commands.insertContentAt(emptyPos, { type: "paragraph" });
    const original = toArticleDocument(editor.getJSON());
    const range = NodeRangeSelection.create(
      editor.state.doc,
      0,
      editor.state.doc.content.size,
    );
    editor.view.dispatch(editor.state.tr.setSelection(range));
    const paragraph = editor.view.dom.querySelectorAll("p")[1];
    const click = new MouseEvent("selection-target", {
      bubbles: true,
      cancelable: true,
      ...(modifier ? { [modifier]: true } : {}),
    });
    paragraph.dispatchEvent(click);
    editor.view.someProp("handleDOMEvents", (handlers) =>
      handlers.mousedown?.(editor.view, click),
    );
    if (modifier) {
      expect(editor.state.selection.eq(range)).toBe(true);
      return;
    }
    expect(editor.state.selection.toJSON()).toEqual({
      type: "text",
      anchor: emptyPos + 1,
      head: emptyPos + 1,
    });
    expect(toArticleDocument(editor.getJSON())).toEqual(original);
    editor.view.dom.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      }),
    );
    const saved = toArticleDocument(editor.getJSON());
    expect(saved.content.map((node) => node.type)).toEqual([
      "paragraph",
      "image",
      "paragraph",
      "paragraph",
      "paragraph",
    ]);
    expect(saved.content[1]).toEqual(original.content[1]);
    expect(editor.state.selection.from).toBe(emptyPos + 3);
  },
);
