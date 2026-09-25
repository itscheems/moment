import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import type {
  PublicArticlePost,
  PublicPost,
  SiteIdentity,
} from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { FeedbackProvider } from "../../components/Feedback";

const expireIfInvalidSession = vi.fn().mockReturnValue(false);

vi.mock("./OwnerProvider", () => ({
  useOwner: () => ({
    api: {},
    session: { csrfToken: "csrf", expiresAt: "", issuedAt: 0, version: "1" },
    state: "owner",
    account: null,
    limits: null,
    expireIfInvalidSession,
  }),
}));

const { OwnerPostActions } = await import("./OwnerPostActions");

const site: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "Moment",
  title: "乔乔的 Moment",
  bio: "",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};

const ARTICLE_ID = "01K2ABCDEF0123456789ABCDE0";
const DRAFT_ID = "01K2ABCDEF0123456789ABCDE9";

function article(): PublicArticlePost {
  return {
    id: ARTICLE_ID,
    kind: "article",
    contentMd: null,
    contentHtml: "",
    article: {
      title: "一条河流的十年",
      excerpt: "从上游走到入海口。",
      cover: null,
      body: null,
      bodySchemaVersion: null,
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

function moment(): PublicPost {
  return {
    ...article(),
    id: "01K2ABCDEF0123456789ABCDE1",
    kind: "moment",
    contentMd: "下雨了。",
    article: null,
  };
}

function show(post: PublicPost) {
  return render(
    <FeedbackProvider>
      <NavigationProvider>
        <OwnerPostActions post={post} site={site} />
      </NavigationProvider>
    </FeedbackProvider>,
  );
}

async function openMenuAndEdit(label: string) {
  fireEvent.click(screen.getByRole("button", { name: "更多操作" }));
  const item = await screen.findByRole("menuitem", { name: label });
  await act(async () => {
    fireEvent.click(item);
  });
}

beforeEach(() => {
  window.history.replaceState({}, "", `/m/${ARTICLE_ID}`);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// A Moment is its Markdown and the edit page changes it in place. An article is
// a document, and what is edited is a working copy -- so the same menu item has
// to mean two different things, and the Markdown box must never open over a
// body that is not Markdown.
it("edits an article through a working copy rather than the Markdown page", async () => {
  const fetcher = vi.fn(() =>
    Promise.resolve(
      new Response(
        JSON.stringify({
          data: {
            id: DRAFT_ID,
            title: "一条河流的十年",
            body: { schema_version: 1, type: "doc", content: [] },
            body_schema_version: 1,
            cover_media_id: null,
            published_entry_id: ARTICLE_ID,
            created_at: "2026-08-10T01:00:00Z",
            updated_at: "2026-08-10T01:00:00.000Z",
          },
          meta: { request_id: "r" },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  vi.stubGlobal("fetch", fetcher);

  show(article());
  await openMenuAndEdit("编辑文章");

  expect(fetcher).toHaveBeenCalledWith(
    `/api/v1/owner/articles/${ARTICLE_ID}/draft`,
    expect.objectContaining({ method: "POST" }),
  );
  expect(window.location.pathname).toBe(`/compose/article/${DRAFT_ID}`);
});

it("still sends a Moment to the page that edits Markdown", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);

  show(moment());
  await openMenuAndEdit("编辑");

  expect(fetcher).not.toHaveBeenCalled();
  expect(window.location.pathname).toBe(
    "/owner/m/01K2ABCDEF0123456789ABCDE1/edit",
  );
});
