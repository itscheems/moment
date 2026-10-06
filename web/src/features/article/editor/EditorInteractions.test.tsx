import "@testing-library/jest-dom/vitest";
import { Editor } from "@tiptap/core";
import { EditorContent, EditorContext } from "@tiptap/react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { articleEditorExtensions } from "./schema";
import { toArticleDocument, toEditorContent } from "./document";
import { DuplicateButton } from "@notion/components/ui/duplicate-button";
import { TextAlignButton } from "@notion/components/ui/text-align-button";
import { TableAddRowColumnButton } from "@notion/components/nodes/table-node/ui/table-add-row-column-button";
import { CharacterCount } from "./components/CharacterCount";
import { MentionDropdownMenu } from "@notion/components/ui/mention-dropdown-menu";
import { Toolbar } from "@notion/components/primitives/toolbar";
import { MentionProvider } from "../../../app/mentions";
import { NavigationProvider } from "../../../app/navigation";
import { ARTICLE_CODE_LANGUAGES } from "../../../components/content/articleContract";
import { lowlight } from "./lowlight";

let editor: Editor;
afterEach(() => {
  cleanup();
  editor?.destroy();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
function setup(content: string) {
  editor = new Editor({ extensions: articleEditorExtensions(), content });
  vi.spyOn(editor.view, "coordsAtPos").mockReturnValue({
    left: 20,
    right: 20,
    top: 20,
    bottom: 40,
  });
}

it("registers every persisted code language in the native highlighter", () => {
  for (const language of ARTICLE_CODE_LANGUAGES) {
    expect(lowlight.registered(language), language).toBe(true);
  }
});

it("copies a native code node without changing its content, caret, or language", async () => {
  setup(
    '<pre><code class="language-javascript">const value = "&lt;&gt;&amp;";\n  console.log(value);\n</code></pre>',
  );
  editor.commands.setContent({
    type: "doc",
    content: [
      {
        type: "codeBlock",
        attrs: { language: "javascript" },
        content: [
          {
            type: "text",
            text: 'const value = "<>&";\n  console.log(value);\n',
          },
        ],
      },
    ],
  });
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", {
    ...navigator,
    platform: navigator.platform,
    userAgent: navigator.userAgent,
    clipboard: { writeText },
  });
  render(<EditorContent editor={editor} />);
  const before = editor.getJSON();
  const selection = {
    from: editor.state.selection.from,
    to: editor.state.selection.to,
  };
  fireEvent.click(await screen.findByRole("button", { name: "复制代码" }));
  await vi.waitFor(() =>
    expect(writeText).toHaveBeenCalledWith(
      'const value = "<>&";\n  console.log(value);\n',
    ),
  );
  expect(editor.getJSON()).toEqual(before);
  expect({
    from: editor.state.selection.from,
    to: editor.state.selection.to,
  }).toEqual(selection);
  fireEvent.change(screen.getByRole("combobox", { name: "代码语言" }), {
    target: { value: "plaintext" },
  });
  expect(editor.state.doc.firstChild?.attrs.language).toBe("plaintext");
  expect(editor.view.dom.querySelector(".hljs-keyword")).toBeNull();
  fireEvent.change(screen.getByRole("combobox", { name: "代码语言" }), {
    target: { value: "javascript" },
  });
  expect(editor.view.dom.querySelector(".hljs-keyword")).toHaveTextContent(
    "const",
  );
  fireEvent.change(screen.getByRole("combobox", { name: "代码语言" }), {
    target: { value: "null" },
  });
  expect(editor.state.doc.firstChild?.attrs.language).toBeNull();
  expect(() => toArticleDocument(editor.getJSON())).not.toThrow();
});

it("uses the shared profile card for a native mention atom", async () => {
  setup(
    '<p><span data-type="mention" data-id="linlin" data-label="linlin">@linlin</span></p>',
  );
  render(
    <NavigationProvider>
      <MentionProvider
        identities={[
          {
            userId: "a".repeat(32),
            username: "linlin",
            displayName: "林林",
            bio: "沿河散步",
            avatarMediaId: null,
            coverMediaId: null,
            role: "member",
            profileUrl: "/linlin",
            status: null,
            usernameChosen: null,
          },
        ]}
      >
        <EditorContent editor={editor} />
      </MentionProvider>
    </NavigationProvider>,
  );
  fireEvent.focus(await screen.findByRole("link", { name: "@linlin" }));
  expect(
    await screen.findByRole("heading", { name: "林林" }),
  ).toBeInTheDocument();
  expect(screen.getByText("沿河散步")).toBeInTheDocument();
  expect(
    toArticleDocument(editor.getJSON()).content[0].content?.[0],
  ).toMatchObject({ type: "text", text: "@linlin" });
});

it("keeps text-field keys in the input while retaining toolbar navigation", () => {
  render(
    <Toolbar tabIndex={0}>
      <button type="button">Back</button>
      <input aria-label="Link URL" autoFocus />
      <button type="button">Apply</button>
    </Toolbar>,
  );
  const input = screen.getByRole("textbox", { name: "Link URL" });
  for (const key of ["ArrowLeft", "ArrowRight", "Home", "End", "Enter"]) {
    const event = new KeyboardEvent("keydown", {
      key,
      bubbles: true,
      cancelable: true,
    });
    act(() => input.dispatchEvent(event));
    expect(event.defaultPrevented).toBe(false);
    expect(input).toHaveFocus();
  }
  const back = screen.getByRole("button", { name: "Back" });
  act(() => back.focus());
  fireEvent.keyDown(back, { key: "ArrowRight" });
  expect(screen.getByRole("button", { name: "Apply" })).toHaveFocus();
});

it("uses the source duplicate action and undo without losing block content", () => {
  setup("<p><strong>first</strong></p><p>last</p>");
  editor.commands.setNodeSelection(0);
  render(
    <>
      <EditorContent editor={editor} />
      <DuplicateButton editor={editor} />
    </>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Duplicate node" }));
  expect(editor.state.doc.childCount).toBe(3);
  expect(editor.state.doc.child(1).textContent).toBe("first");
  act(() => {
    editor.commands.undo();
  });
  expect(editor.state.doc.childCount).toBe(2);
});

it("persists native alignment through save and reopen", () => {
  setup("<p>alignment</p>");
  editor.commands.setTextSelection(2);
  render(
    <>
      <EditorContent editor={editor} />
      <TextAlignButton editor={editor} align="center" />
    </>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Align center" }));
  const saved = toArticleDocument(editor.getJSON());
  expect(saved.content[0].attrs?.textAlign).toBe("center");
  act(() => {
    editor.commands.setContent(toEditorContent(saved));
  });
  expect(editor.state.doc.child(0).attrs.textAlign).toBe("center");
});

it("inserts a row with the source table action and preserves text on undo", () => {
  setup(
    "<table><tbody><tr><td>A</td><td>B</td></tr><tr><td>C</td><td>D</td></tr></tbody></table>",
  );
  editor.commands.setTextSelection(4);
  render(
    <>
      <EditorContent editor={editor} />
      <TableAddRowColumnButton
        editor={editor}
        orientation="row"
        index={0}
        tablePos={0}
        side="below"
      />
    </>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Insert row below" }));
  expect(editor.state.doc.child(0).childCount).toBe(3);
  act(() => {
    editor.commands.undo();
  });
  expect(editor.state.doc.child(0).childCount).toBe(2);
  expect(editor.state.doc.textContent).toBe("ABCD");
});

it("uses native highlighting and character counting without saving presentation decorations", () => {
  setup(
    '<pre><code class="language-js">const value = 1;</code></pre><p>中文 👋</p>',
  );
  render(
    <>
      <EditorContent editor={editor} />
      <CharacterCount editor={editor} />
    </>,
  );
  expect(editor.view.dom.querySelector(".hljs-keyword")).toHaveTextContent(
    "const",
  );
  expect(JSON.stringify(toArticleDocument(editor.getJSON()))).not.toContain(
    "hljs",
  );
  expect(editor.storage.characterCount.characters()).toBe(
    Array.from("constvalue=1;中文👋").length,
  );
});

it("keeps native emoji and mention suggestions readable after saving and reopening", () => {
  setup("<p></p>");
  editor.commands.setEmoji("sparkles");
  editor.commands.insertContent([
    { type: "text", text: " " },
    { type: "mention", attrs: { id: "xiaoman", label: "xiaoman" } },
  ]);
  const saved = toArticleDocument(editor.getJSON());
  const text = saved.content[0].content
    ?.map((node) => node.text ?? "")
    .join("");
  expect(text).toBe("✨ @xiaoman");
  render(<CharacterCount editor={editor} />);
  expect(screen.getByText("正文 9 字")).toBeInTheDocument();
  editor.commands.setContent(toEditorContent(saved));
  expect(editor.getText()).toBe("✨ @xiaoman");
});

it("replaces only a mention query in the middle of prose, preserving its suffix and next heading", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          data: [
            {
              owner_id: "00000000000000000000000000000000",
              username: "xiaoman",
              display_name: "小满",
              avatar_media_id: null,
              profile_url: "/xiaoman",
            },
          ],
          meta: { request_id: "mention-test" },
        }),
        { headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  setup("<p>before after</p><h1>Keep this heading</h1>");
  render(
    <EditorContext.Provider value={{ editor }}>
      <EditorContent editor={editor} />
      <MentionDropdownMenu />
    </EditorContext.Provider>,
  );
  await act(async () => {
    editor.commands.setTextSelection(8);
    editor.commands.insertContent("@xiaoman");
  });
  fireEvent.click(
    await screen.findByRole("button", { name: /小满 · @xiaoman/ }),
  );
  expect(
    editor.state.doc
      .child(0)
      .textBetween(0, editor.state.doc.child(0).content.size, "", (node) =>
        node.type.name === "mention" ? `@${node.attrs.id}` : "",
      ),
  ).toBe("before @xiaoman after");
  expect(editor.state.doc.child(1).textContent).toBe("Keep this heading");
});

it("uses native search and replace without persisting result decorations", () => {
  setup("<p>river River river</p>");
  editor.commands.setSearchTerm("river");
  editor.commands.setReplaceTerm("stream");
  editor.commands.replaceAll();
  expect(editor.getText()).toBe("stream stream stream");
  const saved = toArticleDocument(editor.getJSON());
  expect(JSON.stringify(saved)).not.toContain("find-and-replace");
  editor.commands.undo();
  expect(editor.getText()).toBe("river River river");
});

it("parses Markdown through the template extension and keeps its formatting on the wire", () => {
  setup("## 河流\n\n**加粗** 和 [参考](https://example.com/river)");
  const saved = toArticleDocument(editor.getJSON());
  expect(saved.content[0]).toMatchObject({
    type: "heading",
    attrs: { level: 2 },
  });
  expect(saved.content[1].content).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ text: "加粗", marks: [{ type: "bold" }] }),
      expect.objectContaining({
        text: "参考",
        marks: [{ type: "link", attrs: { href: "https://example.com/river" } }],
      }),
    ]),
  );
});
