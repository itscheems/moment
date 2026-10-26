import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  PublicApi,
  PublicApiError,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { SessionProvider } from "../auth/SessionProvider";
import { ImageGrid } from "./ImageGrid";
import { MediaViewerProvider } from "./MediaViewerProvider";

const site: SiteIdentity = {
  profileUserId: "author",
  username: "writer",
  displayName: "测试作者",
  title: "测试站点",
  bio: "",
  timezone: "UTC",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: "fixture",
  instanceInitializedAt: null,
};
const media = [0, 1].map((position) => ({
  mediaId: "media-" + position,
  position,
  mimeType: "image/png",
  altText: "图" + (position + 1),
  width: 600,
  height: 400,
}));
const post: PublicPost = {
  id: "post-one",
  kind: "moment",
  article: null,
  contentMd: "来源正文",
  contentHtml: "<p>来源正文</p>",
  createdAt: "2026-10-26T00:00:00Z",
  updatedAt: "2026-10-26T00:00:00Z",
  publishedAt: "2026-10-26T00:00:00Z",
  repostedAt: null,
  timelineItemKind: "original",
  visibility: "everyone",
  commentCount: 0,
  media,
  linkCards: [],
};
function harness(api: PublicApi, scope = "reader") {
  return (
    <NavigationProvider>
      <SessionProvider>
        <MediaViewerProvider api={api} site={site} scope={scope}>
          <span>底层阅读页</span>
          <ImageGrid media={media} source={{ postId: post.id }} />
        </MediaViewerProvider>
      </SessionProvider>
    </NavigationProvider>
  );
}
function mockApi() {
  const api = new PublicApi();
  vi.spyOn(api, "post").mockResolvedValue(post);
  vi.spyOn(api, "comments").mockResolvedValue({
    items: [],
    pinned: [],
    pinnedCommentId: null,
    nextCursor: null,
  });
  return api;
}
beforeEach(() => {
  window.history.replaceState({}, "", "/");
  window.sessionStorage.clear();
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            error: {
              code: "UNAUTHORIZED",
              message: "sign in",
              details: {},
              request_id: "fixture",
            },
          }),
          { status: 401, headers: { "Content-Type": "application/json" } },
        ),
    ),
  );
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: query.includes("min-width"),
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  );
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("published media viewer", () => {
  it("opens the clicked image with the owning post and one attachment presentation", async () => {
    const api = mockApi();
    render(harness(api));
    fireEvent.click(screen.getByRole("button", { name: /打开第 2 张图片/ }));
    const dialog = await screen.findByRole("dialog", { name: "图片 2 / 2" });
    expect(await within(dialog).findByText("来源正文")).toBeInTheDocument();
    expect(api.post).toHaveBeenCalledWith(post.id, expect.any(AbortSignal));
    expect(within(dialog).getAllByRole("img", { name: "图2" })).toHaveLength(1);
    expect(within(dialog).queryByRole("button", { name: /打开第/ })).toBeNull();
    expect(
      within(dialog).queryByRole("button", { name: "下一张图片" }),
    ).toBeNull();
    expect(within(dialog).getByText("登录后回复")).toBeInTheDocument();
  });

  it("keeps discussion mounted across image changes and restores trigger focus on close", async () => {
    const api = mockApi();
    render(harness(api));
    const trigger = screen.getByRole("button", { name: /打开第 1 张图片/ });
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByText("来源正文");
    const originalArticle = dialog.querySelector("article");
    fireEvent.click(within(dialog).getByRole("button", { name: "下一张图片" }));
    expect(dialog.querySelector("article")).toBe(originalArticle);
    expect(api.comments).toHaveBeenCalledTimes(1);
    fireEvent.click(
      within(dialog).getByRole("button", { name: "关闭图片预览" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it("revalidates a stale gallery and removes media no longer on the owning post", async () => {
    const api = mockApi();
    vi.mocked(api.post).mockResolvedValueOnce({ ...post, media: [media[1]] });
    render(harness(api));
    fireEvent.click(screen.getByRole("button", { name: /打开第 1 张图片/ }));
    await waitFor(() =>
      expect(screen.getByRole("dialog")).toHaveAccessibleName("图片 1 / 1"),
    );
    expect(
      within(screen.getByRole("dialog")).getByRole("img", { name: "图2" }),
    ).toBeInTheDocument();
    expect(
      within(screen.getByRole("dialog")).queryByRole("img", { name: "图1" }),
    ).toBeNull();
  });

  it("keeps the clicked image when a fresh post has reordered its media", async () => {
    const api = mockApi();
    vi.mocked(api.post).mockResolvedValueOnce({
      ...post,
      media: [
        { ...media[1], position: 0 },
        { ...media[0], position: 1 },
      ],
    });
    render(harness(api));
    fireEvent.click(screen.getByRole("button", { name: /打开第 1 张图片/ }));
    await waitFor(() =>
      expect(screen.getByRole("dialog")).toHaveAccessibleName("图片 2 / 2"),
    );
    expect(
      within(screen.getByRole("dialog")).getByRole("img", { name: "图1" }),
    ).toBeInTheDocument();
  });

  it("does not mistake a gallery from a previous page session for the current gallery", async () => {
    const api = mockApi();
    const firstPage = render(harness(api));
    fireEvent.click(screen.getByRole("button", { name: /打开第 1 张图片/ }));
    await screen.findByRole("dialog", { name: "图片 1 / 2" });
    const staleEntry = structuredClone(window.history.state);
    firstPage.unmount();
    render(harness(api));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    fireEvent.click(screen.getByRole("button", { name: /打开第 2 张图片/ }));
    await screen.findByRole("dialog", { name: "图片 2 / 2" });
    await act(async () => {
      window.history.replaceState(staleEntry, "", "/");
      window.dispatchEvent(
        new PopStateEvent("popstate", { state: staleEntry }),
      );
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("withdraws cached media and original access when the source is no longer readable", async () => {
    const api = mockApi();
    vi.mocked(api.post).mockRejectedValue(
      new PublicApiError("missing", { status: 404, code: "POST_NOT_FOUND" }),
    );
    render(harness(api));
    fireEvent.click(screen.getByRole("button", { name: /打开第 1 张图片/ }));
    const dialog = await screen.findByRole("dialog");
    expect(
      await within(dialog).findByText("此内容已不可用。"),
    ).toBeInTheDocument();
    expect(within(dialog).queryByRole("img", { name: "图1" })).toBeNull();
    expect(within(dialog).queryByRole("link", { name: "打开原图" })).toBeNull();
  });

  it("clears viewer buffers on account scope changes without remounting the reading page", async () => {
    const api = mockApi();
    const view = render(harness(api));
    const underlying = screen.getByText("底层阅读页");
    fireEvent.click(screen.getByRole("button", { name: /打开第 1 张图片/ }));
    await screen.findByRole("dialog");
    await act(async () => view.rerender(harness(api, "another-reader")));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByText("底层阅读页")).toBe(underlying);
  });
});
