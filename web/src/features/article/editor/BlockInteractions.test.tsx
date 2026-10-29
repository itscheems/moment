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
      editor.view.dom.querySelector(".tiptap-image-caption"),
    ).toHaveAttribute("contenteditable", "true"),
  );
  expect(editor.view.dom.querySelector(".tiptap-image")).toHaveAttribute(
    "contenteditable",
    "false",
  );
  act(() => editor.setEditable(false));
  await waitFor(() =>
    expect(
      editor.view.dom.querySelector(".tiptap-image-caption"),
    ).toHaveAttribute("contenteditable", "false"),
  );
  act(() => editor.setEditable(true));
  await waitFor(() =>
    expect(
      editor.view.dom.querySelector(".tiptap-image-caption"),
    ).toHaveAttribute("contenteditable", "true"),
  );
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
