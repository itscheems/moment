import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { NavigationProvider } from "../../app/navigation";
import { MarkdownBody, parseRenderedMarkdown } from "./MarkdownBody";

afterEach(cleanup);
const show = (html: string) =>
  render(
    <NavigationProvider>
      <MarkdownBody html={html} />
    </NavigationProvider>,
  );

describe("Markdown adapted into a Tiptap document", () => {
  it("highlights searches in links, inline code, and across fence syntax tokens", () => {
    const { container, rerender } = render(
      <NavigationProvider>
        <MarkdownBody
          searchQuery="const value"
          html={
            '<p><a href="/owner">const value</a> <code>const value</code></p><pre><code class="language-js">const value = 1;</code></pre>'
          }
        />
      </NavigationProvider>,
    );
    expect(container.querySelector("a mark")).toHaveTextContent("const value");
    expect(container.querySelector("p code mark")).toHaveTextContent(
      "const value",
    );
    expect(
      Array.from(
        container.querySelectorAll("pre mark"),
        (node) => node.textContent,
      ).join(""),
    ).toBe("const value");
    expect(container.querySelector("pre .hljs-keyword")).toHaveTextContent(
      "const",
    );
    rerender(
      <NavigationProvider>
        <MarkdownBody searchQuery="[a]" html="<p>[a] a</p>" />
      </NavigationProvider>,
    );
    expect(container.querySelector("mark")).toHaveTextContent("[a]");
  });
  it("preserves Markdown-only heading levels, anchors, search matches, and aligned tables", () => {
    const { container } = show(
      '<h6 id="旧日记">旧日记</h6>\n<p>一段<mark>中文</mark>正文。</p>\n' +
        '<table><thead><tr><th align="center">年份</th></tr></thead><tbody><tr><td align="right">2026</td></tr></tbody></table>',
    );
    expect(screen.getByRole("heading", { level: 6 })).toHaveAttribute(
      "id",
      "旧日记",
    );
    expect(container.querySelector("mark")).toHaveTextContent("中文");
    expect(screen.getByRole("columnheader")).toHaveStyle({
      textAlign: "center",
    });
    expect(screen.getByRole("cell")).toHaveStyle({ textAlign: "right" });
  });

  it("keeps soft and hard breaks without manufacturing paragraphs from HTML formatting", () => {
    const html =
      "<p>第一行\n第二行<br>\n第三行 <strong>保留</strong> 空格</p>\n<blockquote>\n<p>引用</p>\n</blockquote>\n";
    const doc = parseRenderedMarkdown(html);
    expect(doc.content).toHaveLength(2);
    const { container } = show(html);
    const p = container.querySelector("[data-content-renderer] > p");
    expect(p?.textContent).toBe("第一行\n第二行第三行 保留 空格");
    expect(p?.querySelectorAll("br")).toHaveLength(1);
    expect(container.querySelector("blockquote p")).toHaveTextContent("引用");
  });

  it("keeps tight and loose nested lists and their starting number", () => {
    const { container } = show(
      '<ol start="3">\n<li>第三项\n<ul>\n<li>子项</li>\n</ul>\n</li>\n<li>\n<p>第四项</p>\n<p>第二段</p>\n</li>\n</ol>',
    );
    expect(container.querySelector("ol")).toHaveAttribute("start", "3");
    expect(container.querySelectorAll("ol > li")).toHaveLength(2);
    expect(container.querySelector("ul li")).toHaveTextContent("子项");
    expect(
      container.querySelector("ol > li:last-child")?.querySelectorAll("p"),
    ).toHaveLength(2);
  });

  it("retains complete Chinese-adjacent URLs and escaped code", () => {
    const href =
      "https://example.com/path?utm_source=moment&utm_medium=timeline";
    const { container } = show(
      `<p>留个链接：<a href="${href.replaceAll("&", "&amp;")}">${href}</a>。</p><pre><code class="language-plaintext">&lt;div&gt;  @owner\n&lt;/div&gt;</code></pre>`,
    );
    expect(screen.getByRole("link")).toHaveAttribute("href", href);
    expect(container.querySelector("code")?.textContent).toBe(
      "<div>  @owner\n</div>",
    );
    expect(container.querySelector("code a")).toBeNull();
  });

  it("never displays raw script, event handlers, or external images", () => {
    const { container } = show(
      '<p onclick="alert(1)">文字<img src="https://example.com/tracker.png" onerror="alert(1)"></p><script>alert(1)</script><p><a href="javascript:alert(1)">不安全</a></p>',
    );
    expect(
      container.querySelector("script, img, [onclick], [onerror]"),
    ).toBeNull();
    expect(container.querySelector("a[href^='javascript:']")).toBeNull();
    expect(container).toHaveTextContent("文字");
  });
});
