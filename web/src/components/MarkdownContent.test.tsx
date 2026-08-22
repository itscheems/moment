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

  it("links exact owner mentions without touching code or similar handles", () => {
    render(
      <MarkdownContent
        html={
          "<p>你好 @owner，别误连 @owner_two。</p><pre><code>@owner</code></pre>"
        }
        mentionUsername="owner"
      />,
    );

    const mention = screen.getByRole("link", { name: "@owner" });
    expect(mention).toHaveAttribute("href", "/owner");
    expect(mention).toHaveAttribute("data-moment-mention", "owner");
    expect(screen.getByText(/别误连 @owner_two/)).toBeInTheDocument();
    expect(document.querySelector("code a")).toBeNull();
  });
});
