import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type {
  PublicArticle,
  PublicArticlePost,
  PublicPost,
  SiteIdentity,
} from "../../api/public";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { NavigationProvider } from "../../app/navigation";
import { PostPresentation } from "../posts/PostPresentation";
import { ArticleCard } from "./ArticleCard";
import articleCss from "./Article.module.css?inline";

afterEach(() => {
  cleanup();
  window.history.replaceState({}, "", "/");
});

const site: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "Moment",
  title: "乔乔的 Moment",
  bio: "一些正在发生的事",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};

const coverMediaId = "01K2ABCDEF0123456789ABCDE7";

function makeArticle(
  overrides: Partial<PublicArticle> = {},
): PublicArticlePost {
  return {
    id: "01K2ABCDEF0123456789ABCDE0",
    kind: "article",
    contentMd: null,
    // What a list read carries: the card, and neither the body nor the HTML.
    contentHtml: "",
    article: {
      title: "一条河流的十年",
      excerpt: "从上游走到入海口，记下水色每一年的变化。",
      cover: {
        mediaId: coverMediaId,
        mimeType: "image/jpeg",
        width: 1600,
        height: 900,
      },
      body: null,
      bodySchemaVersion: null,
      ...overrides,
    },
    createdAt: "2026-08-10T01:00:00.000Z",
    updatedAt: "2026-08-10T01:00:00.000Z",
    publishedAt: "2026-08-10T01:00:00.000Z",
    repostedAt: null,
    visibility: "everyone",
    timelineItemKind: "original",
    commentCount: 0,
    media: [],
    linkCards: [],
  };
}

function makeMoment(): PublicPost {
  return {
    id: "01K2ABCDEF0123456789ABCDE1",
    kind: "moment",
    contentMd: "今天下了一场雨。",
    article: null,
    contentHtml: "<p>今天下了一场雨。</p>",
    createdAt: "2026-08-10T01:00:00.000Z",
    updatedAt: "2026-08-10T01:00:00.000Z",
    publishedAt: "2026-08-10T01:00:00.000Z",
    repostedAt: null,
    visibility: "everyone",
    timelineItemKind: "original",
    commentCount: 0,
    media: [],
    linkCards: [],
  };
}

describe("article card", () => {
  it("leads with a cover, a marked type, a title and an excerpt", () => {
    render(<ArticleCard post={makeArticle()} />);

    expect(screen.getByText("Article")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "一条河流的十年" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("从上游走到入海口，记下水色每一年的变化。"),
    ).toBeInTheDocument();
  });

  it("reserves the cover's box from the geometry the card arrived with", () => {
    const { container } = render(<ArticleCard post={makeArticle()} />);
    const cover = container.querySelector("img");

    expect(cover).toHaveAttribute("src", mediaPreviewUrl(coverMediaId));
    expect(cover).toHaveAttribute("width", "1600");
    expect(cover).toHaveAttribute("height", "900");
    expect(cover).toHaveAttribute("loading", "lazy");
    // The title carries the meaning; the picture repeats it.
    expect(cover).toHaveAttribute("alt", "");
  });

  it("keeps the type and complete title in one header without adding a second link", () => {
    const { container } = render(
      <ArticleCard post={makeArticle({ cover: null })} />,
    );

    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByText("Article")).toBeInTheDocument();
    const mark = screen.getByText("Article");
    const heading = screen.getByRole("heading", { name: "一条河流的十年" });
    expect(mark.parentElement).toBe(heading.parentElement);
    expect(container.querySelector("a")).toBeNull();
    expect(mark.querySelector('[aria-hidden="true"]')).toBeInTheDocument();
  });

  it("preserves multiline titles and excerpts for presentation without changing the source text", () => {
    const title = `沿着一条河走过很多年\n${"A long article title ".repeat(8)}`;
    const excerpt = "第一段摘录\n第二段摘录\n继续走到入海口。";
    const { container } = render(
      <ArticleCard post={makeArticle({ title, excerpt, cover: null })} />,
    );

    expect(screen.getByRole("heading").textContent).toBe(title);
    expect(container.querySelector("p")?.textContent).toBe(excerpt);
  });

  it("omits an excerpt the server had nothing to fill", () => {
    const { container } = render(
      <ArticleCard post={makeArticle({ excerpt: "" })} />,
    );

    expect(container.querySelectorAll("p")).toHaveLength(0);
  });
});

describe("an article in the timeline", () => {
  it("renders the card in place of the prose and keeps the action row", () => {
    render(
      <NavigationProvider>
        <PostPresentation post={makeArticle()} site={site} timelineDensity />
      </NavigationProvider>,
    );

    expect(
      screen.getByRole("heading", { name: "一条河流的十年" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Article")).toBeInTheDocument();
    // The whole card is still one keyboard stop that opens the detail page,
    // and the reply and share actions are the ones a moment has.
    expect(
      screen.getByRole("link", { name: "打开这条 Moment 详情" }),
    ).toHaveAttribute("href", "/m/01K2ABCDEF0123456789ABCDE0");
    expect(
      screen.getByRole("link", { name: /打开完整 Moment/ }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "分享这条 Moment" }),
    ).toBeInTheDocument();
  });

  it("draws no attachment grid for the pictures the body places", () => {
    const post = makeArticle();
    const { container } = render(
      <NavigationProvider>
        <PostPresentation
          post={{
            ...post,
            // What the projection carries for an article: the pictures its
            // body places, in the same field a moment's attachments use.
            media: [
              {
                mediaId: "01K2ABCDEF0123456789ABCDE6",
                position: 0,
                altText: "入海口",
                mimeType: "image/jpeg",
                width: 1600,
                height: 900,
              },
            ],
          }}
          site={site}
          timelineDensity
        />
      </NavigationProvider>,
    );

    // Only the cover. A grid of the body's pictures would show the article
    // twice, once as a card and once as an album.
    expect(container.querySelectorAll("img")).toHaveLength(1);
    expect(screen.queryByAltText("入海口")).toBeNull();
  });

  it("leaves a moment rendering its own body", () => {
    render(
      <NavigationProvider>
        <PostPresentation post={makeMoment()} site={site} timelineDensity />
      </NavigationProvider>,
    );

    expect(screen.getByText("今天下了一场雨。")).toBeInTheDocument();
    expect(screen.queryByText("Article")).toBeNull();
  });

  it("keeps a quoted article inside its own destination without adding article or media links", () => {
    const article = makeArticle();
    const { container } = render(
      <NavigationProvider>
        <PostPresentation
          post={{
            ...makeMoment(),
            quotedPost: {
              unavailable: false,
              id: article.id,
              kind: "article",
              article: article.article,
              contentMd: null,
              contentHtml: "",
              publishedAt: article.publishedAt,
              media: [],
              location: null,
            },
          }}
          site={site}
          timelineDensity
        />
      </NavigationProvider>,
    );

    const quote = screen.getByRole("link", { name: "打开被引用的 Moment" });
    expect(quote).toContainElement(
      screen.getByRole("heading", { name: article.article.title }),
    );
    expect(quote.querySelectorAll("a")).toHaveLength(0);
    expect(quote).toHaveAttribute("tabindex", "0");
    expect(container.querySelectorAll("img")).toHaveLength(1);
  });
});

/**
 * The stylesheet with its comments and every `@media (hover: hover)` block cut
 * out of it, leaving only rules that apply to a pointer that cannot hover.
 */
function withoutHoverQueries(source: string): string {
  const css = source.replace(/\/\*[\s\S]*?\*\//g, "");
  let kept = "";
  let index = 0;
  for (;;) {
    const start = css.indexOf("@media (hover: hover)", index);
    if (start === -1) return kept + css.slice(index);
    kept += css.slice(index, start);
    let depth = 0;
    let end = css.length;
    for (let cursor = css.indexOf("{", start); cursor < css.length; cursor++) {
      if (css[cursor] === "{") depth += 1;
      else if (css[cursor] === "}" && --depth === 0) {
        end = cursor;
        break;
      }
    }
    index = end + 1;
  }
}

describe("article styling contract", () => {
  it("keeps every hover rule behind a pointer that can hover", () => {
    // A tap latches :hover on a touch screen, which is what b121e4e fixed
    // everywhere else; a new stylesheet must not reintroduce it.
    expect(articleCss).toContain(":hover");
    expect(withoutHoverQueries(articleCss)).not.toContain(":hover");
  });

  it("dresses the card from the shared tokens, so both themes follow", () => {
    expect(articleCss).toContain("var(--border-subtle)");
    expect(articleCss).toContain("var(--surface-muted)");
    expect(articleCss).toContain("var(--text-secondary)");
  });
});
