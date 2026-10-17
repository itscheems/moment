import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NavigationProvider } from "../../app/navigation";
import { MentionProvider } from "../../app/mentions";
import type { Account, ArticleDocument } from "../../api/public";
import { RichContent } from "./RichContent";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
const picture = "01K2ABCDEF0123456789ABCDE7";
const document: ArticleDocument = {
  schema_version: 1,
  type: "doc",
  content: [
    { type: "paragraph" },
    {
      type: "paragraph",
      content: [
        {
          type: "text",
          text: "读写一致",
          marks: [
            { type: "bold" },
            { type: "italic" },
            { type: "underline" },
            { type: "strike" },
          ],
        },
        { type: "hardBreak" },
        { type: "text", text: "第二行" },
      ],
    },
    {
      type: "orderedList",
      attrs: { start: 3 },
      content: [
        {
          type: "listItem",
          content: [
            { type: "paragraph", content: [{ type: "text", text: "第三项" }] },
            {
              type: "bulletList",
              content: [
                {
                  type: "listItem",
                  content: [
                    {
                      type: "paragraph",
                      content: [{ type: "text", text: "嵌套项" }],
                    },
                  ],
                },
              ],
            },
          ],
        },
      ],
    },
    {
      type: "codeBlock",
      attrs: { language: "javascript" },
      content: [
        { type: "text", text: 'const html = "<script>alert(1)</script>";' },
      ],
    },
    { type: "image", attrs: { media_id: picture, alt: "河流" } },
  ],
};

describe("shared Tiptap document rendering", () => {
  it("preserves empty lines, breaks, nested lists, marks, and image geometry without an editor", () => {
    const { container } = render(
      <NavigationProvider>
        <RichContent
          document={document}
          media={[
            {
              mediaId: picture,
              mimeType: "image/jpeg",
              width: 1200,
              height: 800,
            },
          ]}
        />
      </NavigationProvider>,
    );
    expect(container.querySelector("[contenteditable]")).toBeNull();
    expect(
      container.querySelector(
        "[data-content-renderer='tiptap'] > p:first-child > br",
      ),
    ).not.toBeNull();
    expect(container.querySelector("p strong")).not.toBeNull();
    expect(container.querySelector("p em")).not.toBeNull();
    expect(container.querySelector("p u")).not.toBeNull();
    expect(container.querySelector("p s")).not.toBeNull();
    expect(container.querySelector("ol")).toHaveAttribute("start", "3");
    expect(container.querySelector("ol li ul li")).toHaveTextContent("嵌套项");
    expect(screen.getByAltText("河流")).toHaveAttribute("width", "1200");
    expect(screen.getByAltText("河流")).toHaveAttribute("height", "800");
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("pre code")).toHaveTextContent(
      'const html = "<script>alert(1)</script>";',
    );
    expect(container.querySelector("pre .hljs-keyword")).toHaveTextContent(
      "const",
    );
  });

  it("keeps mounted body elements stable when a surrounding component renders again", () => {
    const view = render(
      <NavigationProvider>
        <RichContent document={document} />
      </NavigationProvider>,
    );
    const before = view.container.querySelector("pre code");
    view.rerender(
      <NavigationProvider>
        <RichContent document={document} />
      </NavigationProvider>,
    );
    expect(view.container.querySelector("pre code")).toBe(before);
  });

  it("copies only the clicked block's exact code without rendering controls into the source", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", {
      ...navigator,
      userAgent: navigator.userAgent,
      platform: navigator.platform,
      clipboard: { writeText },
    });
    const source = 'const text = "<>&";\n  console.log(text);\n';
    const doc: ArticleDocument = {
      schema_version: 2,
      type: "doc",
      content: [
        {
          type: "codeBlock",
          attrs: { language: "javascript" },
          content: [{ type: "text", text: "first();" }],
        },
        {
          type: "codeBlock",
          attrs: { language: "javascript" },
          content: [{ type: "text", text: source }],
        },
      ],
    };
    const view = render(
      <NavigationProvider>
        <RichContent document={doc} />
      </NavigationProvider>,
    );
    fireEvent.click(screen.getAllByRole("button", { name: "复制代码" })[1]);
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(source));
    expect(writeText).toHaveBeenCalledTimes(1);
    expect(view.container.querySelectorAll("pre code")[1]?.textContent).toBe(
      source,
    );
    expect(view.container.querySelector("[contenteditable]")).toBeNull();
  });

  it("opens the existing identity card for an article mention and unmounts it on navigation", async () => {
    const identity: Account = {
      userId: "a".repeat(32),
      username: "linlin",
      displayName: "林林",
      bio: "沿河散步",
      avatarMediaId: null,
      coverMediaId: null,
      role: "member",
      profileUrl: "/linlin",
      status: "active",
      usernameChosen: true,
    };
    const doc: ArticleDocument = {
      schema_version: 2,
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [{ type: "text", text: "你好 @linlin" }],
        },
      ],
    };
    const fetcher = vi.fn();
    vi.stubGlobal("fetch", fetcher);
    render(
      <NavigationProvider>
        <MentionProvider identities={[identity]}>
          <RichContent document={doc} />
        </MentionProvider>
      </NavigationProvider>,
    );
    fireEvent.focus(screen.getByRole("link", { name: "@linlin" }));
    const name = await screen.findByRole("heading", { name: "林林" });
    expect(screen.getByText("沿河散步")).toBeInTheDocument();
    expect(fetcher).not.toHaveBeenCalled();
    fireEvent.click(name.querySelector("a")!);
    await vi.waitFor(() =>
      expect(screen.queryByRole("heading", { name: "林林" })).toBeNull(),
    );
    expect(window.location.pathname).toBe("/linlin");
  });
});
