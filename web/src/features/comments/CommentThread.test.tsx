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

import { PublicApi, PublicApiError, type SiteIdentity } from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { OwnerProvider, useOwner } from "../owner/OwnerProvider";
import { CommentThread } from "./CommentThread";

const postId = "01K2ABCDEF0123456789ABCDE0";
const rootId = "01K2ABCDEF0123456789ABCDE1";
const replyId = "01K2ABCDEF0123456789ABCDE2";
const createdId = "01K2ABCDEF0123456789ABCDE3";
const sessionKey = "moment.owner.session.v1";

const site: SiteIdentity = {
  title: "乔乔的 Moment",
  bio: "沿着日常留下坐标",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  commentsEnabled: true,
  instanceId: null,
};

function comment(
  id: string,
  content: string,
  options: {
    parentId?: string | null;
    owner?: boolean;
    authorName?: string;
    createdAt?: string;
  } = {},
) {
  const parentId = options.parentId ?? null;
  return {
    id,
    post_id: postId,
    parent_id: parentId,
    depth: parentId ? 1 : 0,
    author_kind: options.owner ? "owner" : "guest",
    author_name: options.authorName ?? (options.owner ? "Owner" : "访客"),
    content_md: content,
    content_html: `<p>${content}</p>\n`,
    created_at: options.createdAt ?? "2026-08-10T01:03:00Z",
  };
}

const root = comment(rootId, "顶级回应");
const reply = comment(replyId, "已有一级回复", {
  parentId: rootId,
  owner: true,
  createdAt: "2026-08-10T01:04:00Z",
});

function json(body: unknown, status = 200, headers?: HeadersInit): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

function success(
  data: unknown,
  meta: Record<string, unknown> = {},
  status = 200,
) {
  return json({ data, meta: { request_id: "req_test", ...meta } }, status);
}

function failure(code: string, status: number, details = {}) {
  return json(
    {
      error: {
        code,
        message: "stable code wins",
        details,
        request_id: "req_error",
      },
    },
    status,
  );
}

function unauthorized() {
  return failure("UNAUTHORIZED", 401);
}

function preauthenticate() {
  window.sessionStorage.setItem(
    sessionKey,
    JSON.stringify({
      csrf_token: "csrf-old",
      expires_at: "2999-08-11T01:00:00.000Z",
    }),
  );
}

function installApi(
  handler: (
    url: string,
    init: RequestInit | undefined,
  ) => Response | Promise<Response> | undefined,
) {
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const response = await handler(url, init);
      if (response) return response;
      if (url === "/api/v1/owner/status") return unauthorized();
      return json({}, 404);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

function ExpireOwnerControl() {
  const owner = useOwner();
  return (
    <button
      onClick={() =>
        owner.expireIfInvalidSession(
          new PublicApiError("expired", {
            code: "UNAUTHORIZED",
            status: 401,
          }),
        )
      }
      type="button"
    >
      使会话过期
    </button>
  );
}

function renderThread(currentSite = site, withExpireControl = false) {
  return render(
    <NavigationProvider>
      <OwnerProvider>
        {withExpireControl ? <ExpireOwnerControl /> : null}
        <CommentThread
          api={new PublicApi()}
          postId={postId}
          site={currentSite}
        />
      </OwnerProvider>
    </NavigationProvider>,
  );
}

describe("public comment thread", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    vi.stubGlobal(
      "requestAnimationFrame",
      vi.fn((callback: FrameRequestCallback) => {
        callback(0);
        return 1;
      }),
    );
    vi.stubGlobal("cancelAnimationFrame", vi.fn());
    vi.stubGlobal("scrollTo", vi.fn());
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("keeps a flat one-level presentation and places reply composition after its root", async () => {
    const fetcher = installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [root, reply] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return success(
          comment(createdId, "新一级回复", {
            parentId: rootId,
            authorName: "后来的人",
            createdAt: "2026-08-10T01:05:00Z",
          }),
          { replayed: false },
          201,
        );
      }
    });
    renderThread();

    const existingReply = await screen.findByText("已有一级回复");
    expect(existingReply.closest("li")).toHaveAttribute("data-depth", "1");
    expect(screen.getAllByRole("button", { name: "回复" })).toHaveLength(2);

    const replyArticle = screen.getByRole("article", {
      name: "乔乔的 Moment的一级回复",
    });
    fireEvent.click(within(replyArticle).getByRole("button", { name: "回复" }));
    const replyForm = screen.getByRole("form", { name: "回复 访客" });
    expect(screen.getAllByRole("form", { name: "回复 访客" })).toHaveLength(1);
    expect(replyForm.closest("li")?.previousElementSibling).toHaveAttribute(
      "data-comment-id",
      rootId,
    );
    fireEvent.change(screen.getAllByLabelText("显示名")[1], {
      target: { value: "后来的人" },
    });
    fireEvent.change(screen.getByLabelText("回复内容"), {
      target: { value: "新一级回复" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发表回复" }));

    await waitFor(() =>
      expect(
        fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
      ).toHaveLength(1),
    );
    const createdReply = await screen.findByText("新一级回复");
    expect(createdReply).toBeInTheDocument();
    await waitFor(() => expect(createdReply.closest("li")).toHaveFocus());
    const mutation = fetcher.mock.calls.find(
      ([url, init]) =>
        String(url).endsWith("/comments") && init?.method === "POST",
    );
    expect(JSON.parse(String(mutation?.[1]?.body))).toMatchObject({
      author_name: "后来的人",
      parent_id: rootId,
      website: "",
    });
    expect(
      Array.from(
        document.querySelectorAll<HTMLElement>("[data-comment-id]"),
      ).map((item) => [item.dataset.commentId, item.dataset.depth]),
    ).toEqual([
      [rootId, "0"],
      [replyId, "1"],
      [createdId, "1"],
    ]);
  });

  it("sends a script-filled honeypot and never reconciles its rejected comment", async () => {
    let accepted = false;
    const fetcher = installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        const body = JSON.parse(String(init.body)) as { website: string };
        if (body.website) return failure("COMMENT_REJECTED", 422);
        accepted = true;
        return success(
          comment(createdId, "真实回应"),
          { replayed: false },
          201,
        );
      }
    });
    renderThread();

    await screen.findByText("暂无回应");
    fireEvent.change(screen.getByLabelText("显示名"), {
      target: { value: "访客" },
    });
    fireEvent.change(screen.getByLabelText("回应内容"), {
      target: { value: "真实回应" },
    });
    const honeypot = document.querySelector<HTMLInputElement>(
      'input[name="website"]',
    );
    expect(honeypot).toHaveAttribute("aria-hidden", "true");
    expect(honeypot).toHaveAttribute("tabindex", "-1");
    fireEvent.change(honeypot!, { target: { value: "bot.example" } });
    fireEvent.click(screen.getByRole("button", { name: "发表回应" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("防滥用检查");
    expect(
      document.querySelector(`[data-comment-id="${createdId}"]`),
    ).not.toBeInTheDocument();
    expect(accepted).toBe(false);
    const rejectedBody = JSON.parse(
      String(
        fetcher.mock.calls.find(([, init]) => init?.method === "POST")?.[1]
          ?.body,
      ),
    );
    expect(rejectedBody.website).toBe("bot.example");
  });

  it("keeps existing responses readable when new comments are disabled", async () => {
    installApi((url) => {
      if (url.endsWith("/comments")) return success({ items: [root, reply] });
    });
    renderThread({ ...site, commentsEnabled: false });

    expect(await screen.findByText("顶级回应")).toBeInTheDocument();
    expect(screen.getByText("已有一级回复")).toBeInTheDocument();
    expect(screen.getByText("回应已关闭")).toBeInTheDocument();
    expect(
      screen.queryByRole("form", { name: "发表顶级回应" }),
    ).not.toBeInTheDocument();
  });

  it("preserves guest fields and the idempotency key through a Retry-After window", async () => {
    vi.useFakeTimers();
    let submissions = 0;
    const keys: string[] = [];
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        submissions += 1;
        keys.push(new Headers(init.headers).get("Idempotency-Key") ?? "");
        if (submissions === 1) {
          return new Response(
            JSON.stringify({
              error: {
                code: "RATE_LIMITED",
                message: "slow",
                details: { retry_after_seconds: 1 },
                request_id: "req_rate",
              },
            }),
            { status: 429, headers: { "Retry-After": "1" } },
          );
        }
        return success(
          comment(createdId, "稍后重试", { authorName: "耐心访客" }),
          { replayed: false },
          201,
        );
      }
    });
    renderThread();

    await act(async () => undefined);
    fireEvent.change(screen.getByLabelText("显示名"), {
      target: { value: "耐心访客" },
    });
    fireEvent.change(screen.getByLabelText("回应内容"), {
      target: { value: "稍后重试" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发表回应" }));
    await act(async () => undefined);
    expect(screen.getByText(/请在 1 秒后重试/)).toBeInTheDocument();
    expect(screen.getByLabelText("回应内容")).toHaveValue("稍后重试");

    await act(async () => vi.advanceTimersByTime(1_100));
    fireEvent.click(screen.getByRole("button", { name: "发表回应" }));
    await act(async () => undefined);
    expect(keys).toHaveLength(2);
    expect(keys[1]).toBe(keys[0]);
  });

  it("guards a pending create and reconciles the confirmed comment only once", async () => {
    let resolveCreate!: (response: Response) => void;
    const pendingCreate = new Promise<Response>((resolve) => {
      resolveCreate = resolve;
    });
    const fetcher = installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return pendingCreate;
      }
    });
    renderThread();

    await screen.findByText("暂无回应");
    fireEvent.change(screen.getByLabelText("显示名"), {
      target: { value: "访客" },
    });
    fireEvent.change(screen.getByLabelText("回应内容"), {
      target: { value: "只发表一次" },
    });
    const submit = screen.getByRole("button", { name: "发表回应" });
    fireEvent.click(submit);
    fireEvent.click(submit);
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
    ).toHaveLength(1);

    resolveCreate(
      success(comment(createdId, "只发表一次"), { replayed: false }, 201),
    );
    expect(await screen.findAllByText("只发表一次")).toHaveLength(1);
  });

  it("keeps text and existing responses when the server closes comments during submit", async () => {
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [root] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return failure("COMMENTS_DISABLED", 403);
      }
    });
    renderThread();

    await screen.findByText("顶级回应");
    fireEvent.change(screen.getByLabelText("显示名"), {
      target: { value: "访客" },
    });
    fireEvent.change(screen.getByLabelText("回应内容"), {
      target: { value: "关闭前写下" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发表回应" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "回应刚刚被关闭",
    );
    expect(screen.getByLabelText("回应内容")).toHaveValue("关闭前写下");
    expect(screen.getByText("顶级回应")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "发表回应" })).toBeDisabled();
  });

  it("reuses an owner idempotency key after a network failure", async () => {
    preauthenticate();
    let submissions = 0;
    const keys: string[] = [];
    installApi((url, init) => {
      if (url === "/api/v1/owner/status") {
        return success({
          initialized: true,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.includes("/owner/posts/") && init?.method === "POST") {
        submissions += 1;
        keys.push(new Headers(init.headers).get("Idempotency-Key") ?? "");
        if (submissions === 1) throw new TypeError("offline");
        return success(
          comment(createdId, "Owner 重试", { owner: true }),
          { replayed: false },
          201,
        );
      }
    });
    renderThread();

    await screen.findByText("暂无回应");
    expect(screen.queryByLabelText("显示名")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("回应内容"), {
      target: { value: "Owner 重试" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发表回应" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("文字已保留");
    fireEvent.click(screen.getByRole("button", { name: "发表回应" }));

    expect(await screen.findByText("Owner 重试")).toBeInTheDocument();
    expect(keys).toHaveLength(2);
    expect(keys[1]).toBe(keys[0]);
  });

  it.each([
    {
      name: "top-level comment",
      formName: "发表顶级回应",
      fieldName: "回应内容",
      submitName: "发表回应",
      content: "过期前写好的顶级回应",
      parentId: null,
      openReply: false,
    },
    {
      name: "reply",
      formName: "回复 访客",
      fieldName: "回复内容",
      submitName: "发表回复",
      content: "过期前写好的一级回复",
      parentId: rootId,
      openReply: true,
    },
  ])(
    "reauthenticates a pre-expired $name without losing or duplicating its draft",
    async ({
      formName,
      fieldName,
      submitName,
      content,
      parentId,
      openReply,
    }) => {
      preauthenticate();
      const createTokens: Array<string | null> = [];
      const createBodies: Array<Record<string, unknown>> = [];
      installApi((url, init) => {
        if (url === "/api/v1/owner/status") {
          return success({
            initialized: true,
            session_epoch: 1,
            initialized_at: "2026-08-01T00:00:00Z",
          });
        }
        if (url.endsWith("/comments") && init?.method !== "POST") {
          return success({ items: [root] });
        }
        if (url === "/api/v1/owner/session" && init?.method === "POST") {
          return success({
            csrf_token: "csrf-new",
            expires_at: "2999-08-11T01:00:00Z",
          });
        }
        if (url.includes("/owner/posts/") && init?.method === "POST") {
          createTokens.push(new Headers(init.headers).get("X-CSRF-Token"));
          createBodies.push(JSON.parse(String(init.body)));
          return success(
            comment(createdId, content, { owner: true, parentId }),
            { replayed: false },
            201,
          );
        }
      });
      renderThread(site, true);

      await screen.findByText("顶级回应");
      if (openReply) {
        fireEvent.click(screen.getByRole("button", { name: "回复" }));
      }
      const form = screen.getByRole("form", { name: formName });
      const field = within(form).getByRole("textbox", { name: fieldName });
      fireEvent.change(field, { target: { value: content } });
      fireEvent.click(screen.getByRole("button", { name: "使会话过期" }));

      expect(field).toHaveValue(content);
      const surface = form.parentElement!;
      expect(within(surface).getByRole("alert")).toHaveTextContent(
        "文字已保留",
      );
      const credential = within(surface).getByLabelText("所有者令牌");
      fireEvent.change(credential, { target: { value: "new-owner-token" } });
      fireEvent.click(
        within(surface).getByRole("button", { name: "刷新身份" }),
      );
      await waitFor(() =>
        expect(
          within(form).getByRole("button", { name: submitName }),
        ).toBeEnabled(),
      );
      expect(field).toHaveValue(content);
      expect(field).toHaveFocus();
      fireEvent.click(within(form).getByRole("button", { name: submitName }));

      await waitFor(() => expect(createTokens).toHaveLength(1));
      expect(await screen.findByText(content)).toBeInTheDocument();
      expect(createTokens).toEqual(["csrf-new"]);
      expect(createBodies).toEqual([
        { content_md: content, parent_id: parentId },
      ]);
    },
  );

  it("cascades a confirmed root deletion and restores focus to the thread heading", async () => {
    preauthenticate();
    installApi((url, init) => {
      if (url === "/api/v1/owner/status") {
        return success({
          initialized: true,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (url.endsWith("/comments") && init?.method !== "DELETE") {
        return success({ items: [root, reply] });
      }
      if (url.endsWith(`/owner/comments/${rootId}`)) {
        return success(root, {
          deleted: true,
          dry_run: false,
          replies_affected: 1,
        });
      }
    });
    renderThread();

    await screen.findByText("已有一级回复");
    expect(screen.getByText("Owner")).toBeInTheDocument();
    expect(screen.getByText("乔乔的 Moment")).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "删除" })[0]);
    expect(await screen.findByRole("dialog")).toHaveTextContent(
      "下面的 1 条一级回复",
    );
    fireEvent.click(screen.getByRole("button", { name: "删除回应和回复" }));

    await waitFor(() =>
      expect(screen.queryByText("已有一级回复")).not.toBeInTheDocument(),
    );
    expect(screen.queryByText("顶级回应")).not.toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "回应" })).toHaveFocus(),
    );
  });

  it("keeps the delete dialog mounted for inline reauthentication and retries with new CSRF", async () => {
    preauthenticate();
    let deletes = 0;
    const deleteTokens: Array<string | null> = [];
    installApi((url, init) => {
      if (url === "/api/v1/owner/status") {
        return success({
          initialized: true,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (url.endsWith("/comments") && init?.method !== "DELETE") {
        return success({ items: [root] });
      }
      if (url === "/api/v1/owner/session" && init?.method === "POST") {
        return success({
          csrf_token: "csrf-new",
          expires_at: "2999-08-11T01:00:00Z",
        });
      }
      if (url.endsWith(`/owner/comments/${rootId}`)) {
        deletes += 1;
        deleteTokens.push(new Headers(init?.headers).get("X-CSRF-Token"));
        return deletes === 1
          ? unauthorized()
          : success(root, {
              deleted: true,
              dry_run: false,
              replies_affected: 0,
            });
      }
    });
    renderThread();

    await screen.findByText("顶级回应");
    fireEvent.click(screen.getByRole("button", { name: "删除" }));
    fireEvent.click(await screen.findByRole("button", { name: "删除回应" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("刷新身份后可在这里重新删除");
    fireEvent.change(within(dialog).getByLabelText("所有者令牌"), {
      target: { value: "new-owner-token" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "刷新身份" }));
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "删除回应" }),
      ).toBeEnabled(),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "删除回应" }));
    await waitFor(() =>
      expect(screen.queryByText("顶级回应")).not.toBeInTheDocument(),
    );
    expect(deleteTokens).toEqual(["csrf-old", "csrf-new"]);
  });

  it("reauthenticates inside a pre-expired delete dialog and deletes once with restored focus", async () => {
    preauthenticate();
    const deleteTokens: Array<string | null> = [];
    installApi((url, init) => {
      if (url === "/api/v1/owner/status") {
        return success({
          initialized: true,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (url.endsWith("/comments") && init?.method !== "DELETE") {
        return success({ items: [root] });
      }
      if (url === "/api/v1/owner/session" && init?.method === "POST") {
        return success({
          csrf_token: "csrf-new",
          expires_at: "2999-08-11T01:00:00Z",
        });
      }
      if (url.endsWith(`/owner/comments/${rootId}`)) {
        deleteTokens.push(new Headers(init?.headers).get("X-CSRF-Token"));
        return success(root, {
          deleted: true,
          dry_run: false,
          replies_affected: 0,
        });
      }
    });
    renderThread(site, true);

    await screen.findByText("顶级回应");
    fireEvent.click(screen.getByRole("button", { name: "使会话过期" }));
    fireEvent.click(screen.getByRole("button", { name: "删除" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("刷新身份后可在这里重新删除");
    expect(
      within(dialog).getByRole("button", { name: "删除回应" }),
    ).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText("所有者令牌"), {
      target: { value: "new-owner-token" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "刷新身份" }));
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "删除回应" }),
      ).toBeEnabled(),
    );
    expect(dialog).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "删除回应" }));

    await waitFor(() =>
      expect(screen.queryByText("顶级回应")).not.toBeInTheDocument(),
    );
    expect(deleteTokens).toEqual(["csrf-new"]);
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "回应" })).toHaveFocus(),
    );
  });
});
