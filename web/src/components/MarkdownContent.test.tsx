import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { MarkdownContent } from "./MarkdownContent";

afterEach(cleanup);

describe("MarkdownContent", () => {
  it.each([
    ["python", "def hello():\n    return True"],
    ["java", "class Hello { public static void main(String[] args) {} }"],
    ["go", "package main\nfunc main() {}"],
    ["js", "const greeting = () => true;"],
    ["sql", "SELECT id FROM moments WHERE id = 1;"],
  ])("highlights an explicit %s fence", async (language, source) => {
    const { container } = render(
      <MarkdownContent
        html={`<pre><code class="language-${language}">${source}</code></pre>`}
      />,
    );
    await waitFor(() =>
      expect(container.querySelector("code")).toHaveAttribute(
        "data-highlighted",
        "yes",
      ),
    );
    expect(container.querySelector("code .hljs-keyword")).toBeInTheDocument();
  });

  it("keeps plain and unknown fences readable without guessing", () => {
    const { container } = render(
      <MarkdownContent
        html={
          '<pre><code class="language-moment">plain &amp; safe</code></pre>'
        }
      />,
    );
    const code = container.querySelector("code");
    expect(code).toHaveTextContent("plain & safe");
    expect(code).not.toHaveAttribute("data-highlighted");
    expect(code?.querySelector("span")).toBeNull();
  });

  it("still links the owner's handle", () => {
    render(<MarkdownContent html="<p>你好 @owner。</p>" />);

    const mention = screen.getByRole("link", { name: "@owner" });
    expect(mention).toHaveAttribute("href", "/owner");
    expect(mention).toHaveAttribute("data-moment-mention", "owner");
  });

  it("links a handle that is not the owner's", () => {
    // The composer's autocomplete searches every approved account, so a name
    // it offered has to arrive as a link. Recognising only the owner left a
    // member's handle as grey text in the middle of a sentence about them.
    render(<MarkdownContent html="<p>@owner 和 @linlin 都在。</p>" />);

    expect(screen.getByRole("link", { name: "@owner" })).toHaveAttribute(
      "href",
      "/owner",
    );
    const member = screen.getByRole("link", { name: "@linlin" });
    expect(member).toHaveAttribute("href", "/linlin");
    expect(member).toHaveAttribute("data-moment-mention", "linlin");
  });

  it("addresses a capitalised handle at its canonical lowercase", () => {
    // Handles are lowercase by rule and nobody typing one thinks about that.
    // What was typed stays on screen; only the address is lowered.
    render(<MarkdownContent html="<p>谢谢 @Cheems！</p>" />);

    const mention = screen.getByRole("link", { name: "@Cheems" });
    expect(mention).toHaveAttribute("href", "/cheems");
    expect(mention).toHaveAttribute("data-moment-mention", "cheems");
  });

  it("leaves mentions inside code and inside existing links alone", () => {
    render(
      <MarkdownContent
        html={
          '<p><a href="/elsewhere">@linlin</a> 写道：</p>' +
          "<pre><code>@owner</code></pre><p>用 <code>@linlin</code> 引用。</p>"
        }
      />,
    );

    expect(document.querySelector("code a")).toBeNull();
    expect(document.querySelectorAll("a")).toHaveLength(1);
    expect(screen.getByRole("link", { name: "@linlin" })).toHaveAttribute(
      "href",
      "/elsewhere",
    );
  });

  it.each([
    ["@ab", "太短"],
    ["@9lives", "数字开头"],
    ["@has-dash", "含连字符"],
    ["@" + "a".repeat(31), "太长"],
  ])("leaves %s as text (%s)", (handle) => {
    const { container } = render(
      <MarkdownContent html={`<p>看 ${handle} 这个。</p>`} />,
    );

    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).toContain(handle);
  });

  it("does not mistake an email address for a mention", () => {
    // The leading guard carries this: an address has a word character before
    // the at sign and a mention never does.
    const { container } = render(
      <MarkdownContent html="<p>写信到 alice@example.com 就好。</p>" />,
    );

    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).toContain("alice@example.com");
  });
});
