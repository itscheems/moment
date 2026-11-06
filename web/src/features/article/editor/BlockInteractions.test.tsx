import "@testing-library/jest-dom/vitest";
import { Editor } from "@tiptap/core";
import { EditorContent } from "@tiptap/react";
import { NodeRangeSelection } from "@tiptap/extension-node-range";
import { DOMParser } from "@tiptap/pm/model";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { articleEditorExtensions } from "./schema";
import { toArticleDocument } from "./document";
import { DeleteNodeButton } from "@notion/components/ui/delete-node-button";

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

it.each(
  [
    {
      name: "formula",
      content: { type: "blockMath", attrs: { latex: "a+b" } },
    },
    {
      name: "code block",
      content: {
        type: "codeBlock",
        content: [{ type: "text", text: "const value = 1;" }],
      },
    },
  ].flatMap((block) => [8, 80].map((distance) => ({ ...block, distance }))),
)(
  "starts a text paragraph when clicking $distance px below a final $name",
  async ({ content, distance }) => {
    editor = new Editor({
      extensions: articleEditorExtensions(),
      content: { type: "doc", content: [content] },
    });
    await new Promise<void>((resolve) => editor.on("create", () => resolve()));
    render(<EditorContent editor={editor} />);
    const original = editor.getJSON();
    const block = editor.view.nodeDOM(0) as HTMLElement;
    vi.spyOn(editor.view.dom, "getBoundingClientRect").mockReturnValue(
      new DOMRect(0, 0, 600, 500),
    );
    vi.spyOn(block, "getBoundingClientRect").mockReturnValue(
      new DOMRect(24, 40, 552, 40),
    );
    editor.view.dom.style.paddingInline = "24px";
    const event = new MouseEvent("gap-test", {
      bubbles: true,
      cancelable: true,
      clientX: 100,
      clientY: 80 + distance,
    });
    editor.view.dom.dispatchEvent(event);
    const handled = editor.view.someProp("handleDOMEvents", (handlers) =>
      handlers.mousedown?.(editor.view, event),
    );
    expect(handled).toBe(true);
    expect(editor.state.doc.childCount).toBe(2);
    expect(editor.state.selection.$from.parent.type.name).toBe("paragraph");
    expect(editor.state.selection.empty).toBe(true);
    expect(editor.commands.undo()).toBe(true);
    expect(editor.getJSON()).toEqual(original);
  },
);

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

it("preserves image dimensions on the native lazy image before it loads", async () => {
  setup();
  editor.commands.setContent({
    type: "doc",
    content: [
      {
        type: "image",
        attrs: {
          media_id: mediaId,
          src: `/api/v1/media/${mediaId}/preview`,
          width: 1200,
          height: 800,
        },
      },
    ],
  });
  render(<EditorContent editor={editor} />);
  await waitFor(() => {
    const image = editor.view.dom.querySelector("img");
    expect(image).toHaveAttribute("width", "1200");
    expect(image).toHaveAttribute("height", "800");
    expect(image).toHaveAttribute("loading", "lazy");
  });
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

it.each([
  ["Backspace", "", ""],
  ["Backspace", "Body", ""],
  ["Backspace", "", "Caption"],
  ["Backspace", "Body", "Caption"],
  ["Delete", "", ""],
  ["Delete", "Body", ""],
  ["Delete", "", "Caption"],
  ["Delete", "Body", "Caption"],
])(
  "%s respects the image boundary (body=%s, caption=%s)",
  (key, text, caption) => {
    setup();
    const paragraph = {
      type: "paragraph",
      ...(text ? { content: [{ type: "text", text }] } : {}),
    };
    const image = {
      type: "image",
      attrs: { media_id: mediaId, showCaption: Boolean(caption) },
      ...(caption ? { content: [{ type: "text", text: caption }] } : {}),
    };
    editor.commands.setContent({
      type: "doc",
      content: key === "Backspace" ? [image, paragraph] : [paragraph, image],
    });
    const original = toArticleDocument(editor.getJSON()).content.find(
      (node) => node.type === "image",
    );
    editor.commands.setTextSelection(
      key === "Backspace"
        ? editor.state.doc.firstChild!.nodeSize + 1
        : 1 + text.length,
    );
    editor.view.dom.dispatchEvent(
      new KeyboardEvent("keydown", {
        key,
        bubbles: true,
        cancelable: true,
      }),
    );
    const images = toArticleDocument(editor.getJSON()).content.filter(
      (node) => node.type === "image",
    );
    expect(images).toEqual([original]);
    if (text) {
      expect(
        toArticleDocument(editor.getJSON()).content.find(
          (node) => node.type === "paragraph",
        )?.content?.[0].text,
      ).toBe(text);
    }
    // Removing an empty neighboring paragraph can enter a visible caption.
    if (caption && !text) {
      expect(editor.state.selection.$from.parent.type.name).toBe("image");
      expect(editor.state.selection.$from.parentOffset).toBe(
        key === "Backspace" ? caption.length : 0,
      );
    } else {
      expect(editor.state.selection.toJSON().type).toBe("node");
    }
  },
);

it("can delete an explicitly selected image", () => {
  setup();
  editor.commands.setNodeSelection(editor.state.doc.firstChild!.nodeSize);
  editor.view.dom.dispatchEvent(
    new KeyboardEvent("keydown", {
      key: "Backspace",
      bubbles: true,
      cancelable: true,
    }),
  );
  expect(
    toArticleDocument(editor.getJSON()).content.some(
      (node) => node.type === "image",
    ),
  ).toBe(false);
});

it("does not run a second global deletion when image controls are mounted", async () => {
  setup();
  editor.commands.setContent({
    type: "doc",
    content: [
      { type: "image", attrs: { media_id: mediaId } },
      { type: "paragraph" },
    ],
  });
  render(
    <>
      <EditorContent editor={editor} />
      <DeleteNodeButton editor={editor} />
    </>,
  );
  await waitFor(() =>
    expect(editor.view.dom.querySelector(".tiptap-image-img")).not.toBeNull(),
  );
  act(() => {
    editor.commands.setNodeSelection(0);
    editor.commands.setTextSelection(3);
  });
  act(() => {
    editor.view.dom.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Backspace",
        code: "Backspace",
        keyCode: 8,
        bubbles: true,
        cancelable: true,
      }),
    );
  });
  expect(
    toArticleDocument(editor.getJSON()).content.map((node) => node.type),
  ).toEqual(["image"]);
  expect(editor.state.selection.toJSON()).toEqual({ type: "node", anchor: 0 });
});
