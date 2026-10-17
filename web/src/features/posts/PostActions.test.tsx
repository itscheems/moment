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

const viewer = vi.hoisted(() => ({
  current: { userId: "0".repeat(32), role: "admin", status: "active" },
}));
const expireIfInvalidSession = vi.fn().mockReturnValue(false);

vi.mock("../auth/SessionProvider", () => ({
  isAdministrator: (account: { role: string } | null) =>
    account?.role === "admin",
  useSession: () => ({
    api: {},
    session: { csrfToken: "csrf", expiresAt: "", issuedAt: 0, version: "1" },
    state: "signed-in",
    account: viewer.current,
    limits: null,
    expireIfInvalidSession,
  }),
}));

const { PostActions } = await import("./PostActions");

const site: SiteIdentity = {
  profileUserId: "00000000000000000000000000000000",
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
    author: {
      userId: "00000000000000000000000000000000",
      username: "owner",
      displayName: "Moment",
      avatarMediaId: null,
      profileUrl: "/owner",
    },
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

function show(post: PublicPost, showArticleEdit = false) {
  return render(
    <FeedbackProvider>
      <NavigationProvider>
        <PostActions
          post={post}
          site={site}
          showArticleEdit={showArticleEdit}
        />
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
  viewer.current = { userId: "0".repeat(32), role: "admin", status: "active" };
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
    `/api/v1/articles/${ARTICLE_ID}/draft`,
    expect.objectContaining({ method: "POST" }),
  );
  expect(window.location.pathname).toBe(`/compose/articles/drafts/${DRAFT_ID}`);
});

it("still sends a Moment to the page that edits Markdown", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);

  show(moment());
  await openMenuAndEdit("编辑");

  expect(fetcher).not.toHaveBeenCalled();
  expect(window.location.pathname).toBe("/m/01K2ABCDEF0123456789ABCDE1/edit");
});

it("opens the visible article edit button through the existing working-copy endpoint", async () => {
  let resolve!: (value: Response) => void;
  const fetcher = vi.fn(
    () =>
      new Promise<Response>((finish) => {
        resolve = finish;
      }),
  );
  vi.stubGlobal("fetch", fetcher);
  show(article(), true);
  const edit = screen.getByRole("button", { name: "编辑文章" });
  fireEvent.click(edit);
  expect(screen.getByRole("button", { name: "正在打开…" })).toBeDisabled();
  fireEvent.click(edit);
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher).toHaveBeenCalledWith(
    `/api/v1/articles/${ARTICLE_ID}/draft`,
    expect.objectContaining({ method: "POST" }),
  );
  await act(async () =>
    resolve(
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
        { status: 200 },
      ),
    ),
  );
  expect(window.location.pathname).toBe(`/compose/articles/drafts/${DRAFT_ID}`);
});

it("keeps an unsuccessful edit opening on the article and allows retry", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response("{}", { status: 500 })),
  );
  show(article(), true);
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: "编辑文章" })),
  );
  expect(window.location.pathname).toBe(`/m/${ARTICLE_ID}`);
  expect(screen.getByRole("alert")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "编辑文章" })).toBeEnabled();
});

it("does not expose the article button on ordinary Moments", () => {
  show(moment(), true);
  expect(
    screen.queryByRole("button", { name: "编辑文章" }),
  ).not.toBeInTheDocument();
});

it("lets a member edit their own text without offering a site pin", async () => {
  viewer.current.role = "member";
  show(moment());
  fireEvent.click(screen.getByRole("button", { name: "更多操作" }));
  expect(
    await screen.findByRole("menuitem", { name: "编辑" }),
  ).toBeInTheDocument();
  expect(screen.getByRole("menuitem", { name: "删除" })).toBeInTheDocument();
  expect(screen.queryByRole("menuitem", { name: "置顶到个人资料" })).toBeNull();
});
it("lets an administrator moderate another writer without rewriting their text", async () => {
  viewer.current.userId = "1".repeat(32);
  show(moment());
  fireEvent.click(screen.getByRole("button", { name: "更多操作" }));
  expect(
    await screen.findByRole("menuitem", { name: "删除" }),
  ).toBeInTheDocument();
  expect(screen.queryByRole("menuitem", { name: "编辑" })).toBeNull();
  expect(
    screen.getByRole("menuitem", { name: "置顶到个人资料" }),
  ).toBeInTheDocument();
});
it("hides write actions on another writer's post from a member", () => {
  viewer.current = { userId: "1".repeat(32), role: "member", status: "active" };
  show(moment());
  expect(screen.queryByRole("button", { name: "更多操作" })).toBeNull();
});
