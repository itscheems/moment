import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { NavigationProvider } from "../../app/navigation";
import type { ArticleDocument } from "../../api/public";
import { RichContent } from "./RichContent";

afterEach(cleanup);
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
});
