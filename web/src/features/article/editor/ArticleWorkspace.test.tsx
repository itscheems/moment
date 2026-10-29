import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NavigationProvider } from "../../../app/navigation";
import { FeedbackProvider } from "../../../components/Feedback";
import { ArticleDraftApi, type ArticleDraft } from "../articleDrafts";
import type { PublicPost, SiteIdentity } from "../../../api/public";
import ArticleWorkspace from "./ArticleWorkspace";

const session = vi.hoisted(() => ({
  account: {
    userId: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    username: "alice",
    displayName: "Alice",
    bio: "",
    avatarMediaId: null,
    coverMediaId: null,
    status: "active",
    role: "member",
  },
  session: { csrfToken: "csrf" },
  expireIfInvalidSession: vi.fn(),
}));
vi.mock("../../auth/SessionProvider", () => ({ useSession: () => session }));
vi.mock("./ArticleEditor", () => ({
  ArticleEditor: ({
    draftId,
    focused,
    onToggleFocus,
    site,
  }: {
    draftId: string;
    focused: boolean;
    onToggleFocus: () => void;
    site: SiteIdentity;
  }) => (
    <div>
      <span>
        编辑器 {draftId} · {site.username}
      </span>
      <button onClick={onToggleFocus}>
        {focused ? "退出全屏编辑" : "全屏编辑"}
      </button>
    </div>
  ),
}));
vi.mock("../ArticlePage", () => ({
  ArticlePage: ({ post }: { post: PublicPost }) => (
    <article>{post.article?.title} 正文展示</article>
  ),
}));
const site: SiteIdentity = {
  profileUserId: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  username: "admin",
  displayName: "Admin",
  title: "Moment",
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
const draft: ArticleDraft = {
  id: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
  authorUserId: session.account.userId,
  title: "",
  sourceState: "draft",
  sourceVisibility: "everyone",
  body: { schema_version: 1, type: "doc", content: [] },
  bodySchemaVersion: 1,
  coverMediaId: null,
  publishedEntryId: null,
  createdAt: "2026-10-07T01:00:00Z",
  updatedAt: "2026-10-07T01:00:00Z",
};
const post: PublicPost = {
  id: "01ARZ3NDEKTSV4RRFFQ69G5FAW",
  kind: "article",
  contentMd: null,
  article: {
    title: "已发布文章",
    excerpt: "简短摘录",
    cover: null,
    body: draft.body,
    bodySchemaVersion: 1,
  },
  contentHtml: "<p>正文</p>",
  createdAt: draft.createdAt,
  updatedAt: draft.updatedAt,
  publishedAt: draft.createdAt,
  repostedAt: null,
  visibility: "everyone",
  timelineItemKind: "original",
  commentCount: 0,
  media: [],
  linkCards: [],
};
function mount(path = "/compose/articles") {
  window.history.replaceState({}, "", path);
  return render(
    <FeedbackProvider>
      <NavigationProvider>
        <ArticleWorkspace site={site} />
      </NavigationProvider>
    </FeedbackProvider>,
  );
}
beforeEach(() => {
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }));
  vi.spyOn(ArticleDraftApi.prototype, "list").mockResolvedValue([]);
  vi.spyOn(ArticleDraftApi.prototype, "published").mockResolvedValue({
    items: [],
    nextCursor: null,
    prevCursor: null,
    refreshCursor: null,
  });
  vi.spyOn(ArticleDraftApi.prototype, "readManaged").mockResolvedValue(post);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
describe("author article workspace", () => {
  it("creates a server draft immediately without typing, prevents duplicate clicks and uses the current author", async () => {
    let finish!: (draft: ArticleDraft) => void;
    const create = vi
      .spyOn(ArticleDraftApi.prototype, "create")
      .mockReturnValue(
        new Promise((resolve) => {
          finish = resolve;
        }),
      );
    mount();
    await waitFor(() =>
      expect(screen.queryByText("正在读取文章…")).not.toBeInTheDocument(),
    );
    const button = screen.getAllByRole("button", { name: "写文章" })[0]!;
    fireEvent.click(button);
    fireEvent.click(button);
    expect(create).toHaveBeenCalledTimes(1);
    expect(create.mock.calls[0]?.[1].body.content).toEqual([]);
    expect(create.mock.calls[0]?.[2]).toBeTruthy();
    vi.mocked(ArticleDraftApi.prototype.list).mockResolvedValue([
      {
        id: draft.id,
        title: "",
        publishedEntryId: null,
        createdAt: draft.createdAt,
        updatedAt: draft.updatedAt,
        authorUserId: draft.authorUserId,
        sourceState: "draft",
        sourceVisibility: "everyone",
      },
    ]);
    finish(draft);
    await screen.findByText(`编辑器 ${draft.id} · alice`);
    expect(window.location.pathname).toBe(
      `/compose/articles/drafts/${draft.id}`,
    );
    expect(screen.getByText("（未填写标题）")).toBeInTheDocument();
  });
  it("retries an uncertain create with the same idempotency key", async () => {
    const create = vi
      .spyOn(ArticleDraftApi.prototype, "create")
      .mockRejectedValueOnce(new Error("network"))
      .mockResolvedValueOnce(draft);
    mount();
    await waitFor(() =>
      expect(screen.queryByText("正在读取文章…")).not.toBeInTheDocument(),
    );
    fireEvent.click(screen.getAllByRole("button", { name: "写文章" })[0]!);
    await waitFor(() =>
      expect(
        screen.getAllByRole("button", { name: "写文章" })[0],
      ).toBeEnabled(),
    );
    fireEvent.click(screen.getAllByRole("button", { name: "写文章" })[0]!);
    await screen.findByText(`编辑器 ${draft.id} · alice`);
    expect(create.mock.calls[0]?.[2]).toBe(create.mock.calls[1]?.[2]);
  });
  it("toggles focus without changing the selected draft identity", async () => {
    mount(`/compose/articles/drafts/${draft.id}`);
    await screen.findByText(`编辑器 ${draft.id} · alice`);
    fireEvent.click(screen.getByRole("button", { name: "全屏编辑" }));
    expect(window.location.pathname).toBe(
      `/compose/articles/drafts/${draft.id}/focus`,
    );
    fireEvent.click(screen.getByRole("button", { name: "退出全屏编辑" }));
    expect(window.location.pathname).toBe(
      `/compose/articles/drafts/${draft.id}`,
    );
  });
  it("opens published rows readonly and creates a working copy only on explicit edit", async () => {
    vi.mocked(ArticleDraftApi.prototype.published).mockResolvedValue({
      items: [post],
      nextCursor: null,
      prevCursor: null,
      refreshCursor: null,
    });
    const edit = vi
      .spyOn(ArticleDraftApi.prototype, "openPublished")
      .mockResolvedValue({
        ...draft,
        publishedEntryId: post.id,
        sourceState: "working_copy",
      });
    mount();
    fireEvent.click(screen.getByRole("tab", { name: "已发布" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /已发布.*已发布文章/ }),
    );
    await screen.findByText("已发布文章 正文展示");
    expect(edit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "编辑文章" }));
    await screen.findByText(`编辑器 ${draft.id} · alice`);
    expect(edit).toHaveBeenCalledWith("csrf", post.id);
  });
});
