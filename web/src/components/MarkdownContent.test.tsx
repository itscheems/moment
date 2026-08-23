import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render as renderBare,
  screen,
  waitFor,
} from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NavigationProvider, useNavigation } from "../app/navigation";
import { MarkdownContent } from "./MarkdownContent";

// Rendered markdown carries real anchors, and keeping the internal ones inside
// the app needs the router. This component never renders outside it, so the
// tests do not either.
function render(ui: ReactElement) {
  return renderBare(<NavigationProvider>{ui}</NavigationProvider>);
}

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

  it.each([
    ["@login", "登录页"],
    ["@search", "搜索页"],
    ["@assets", "静态资源目录"],
    ["@Settings", "保留名的大小写变体"],
  ])("leaves the reserved handle %s as text (%s)", (handle) => {
    // No account can ever hold one of these, so the link would be wrong for
    // good rather than merely early. These are plain anchors, so following one
    // reloads the browser onto the sign-in form or an asset directory and takes
    // the reader out of the conversation they were reading.
    const { container } = render(
      <MarkdownContent html={`<p>看 ${handle} 这个。</p>`} />,
    );

    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).toContain(handle);
  });

  it("still links a real handle standing beside a reserved one", () => {
    const { container } = render(
      <MarkdownContent html="<p>@login 之后找 @linlin。</p>" />,
    );

    expect(container.querySelectorAll("a")).toHaveLength(1);
    expect(screen.getByRole("link", { name: "@linlin" })).toHaveAttribute(
      "href",
      "/linlin",
    );
    expect(container.textContent).toContain("@login 之后找 @linlin。");
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

// The markup goes in through dangerouslySetInnerHTML, so these are real
// anchors with no React handler on them. Every one of them reloaded the whole
// page -- mentions included -- throwing away the timeline the reader was in
// the middle of.
describe("links written into rendered markdown", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  function renderWithNavigation(html: string) {
    const seen: string[] = [];
    function Probe() {
      const { pathname } = useNavigation();
      seen.push(pathname);
      return <MarkdownContent html={html} />;
    }
    render(<Probe />);
    return seen;
  }

  it("follows a handle mention without leaving the app", () => {
    window.history.replaceState({}, "", "/");
    const seen = renderWithNavigation("<p>问 @cheemschen 一下</p>");

    const link = screen.getByRole("link", { name: "@cheemschen" });
    fireEvent.click(link, { button: 0 });

    expect(seen.at(-1)).toBe("/cheemschen");
    expect(window.location.pathname).toBe("/cheemschen");
  });

  it("leaves an author's link to somewhere else alone", () => {
    window.history.replaceState({}, "", "/");
    renderWithNavigation('<p><a href="https://example.test/a">去别处</a></p>');

    const link = screen.getByRole("link", { name: "去别处" });
    const event = new window.MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      button: 0,
    });
    link.dispatchEvent(event);

    // Not intercepted: the browser is left to do what it does with an
    // off-origin address, which in jsdom is nothing at all.
    expect(event.defaultPrevented).toBe(false);
    expect(window.location.pathname).toBe("/");
  });

  it("leaves a modified click to the browser", () => {
    window.history.replaceState({}, "", "/");
    const seen = renderWithNavigation("<p>问 @cheemschen 一下</p>");

    fireEvent.click(screen.getByRole("link", { name: "@cheemschen" }), {
      button: 0,
      metaKey: true,
    });

    expect(seen.at(-1)).toBe("/");
  });
});
