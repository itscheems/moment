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

import { App } from "../../app/App";
import {
  makeImportCounts,
  makeReviewIdentity,
  makeReviewUser,
} from "../../test/archiveFixtures";
import { makeAccountLimits } from "../../test/accountFixtures";

const sessionKey = "moment.auth.session.v2";
const operationID = "import-20260811T013000000Z-0011223344556677";
const archiveSHA = "a".repeat(64);
const mergePlanID = "b".repeat(64);
const baseAuthoritySHA = "c".repeat(64);
const incomingAuthoritySHA = "d".repeat(64);
const mergeToken = "A".repeat(43);

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function failure(code: string, status: number): Response {
  return json(
    {
      error: {
        code,
        message: code.toLowerCase(),
        details: {},
        request_id: "req_error",
      },
    },
    status,
  );
}

function siteEnvelope() {
  return {
    data: {
      profile_user_id: "00000000000000000000000000000000",
      username: "owner",
      display_name: "乔乔",
      title: "乔乔的 Moment",
      bio: "沿着日常留下坐标",
      timezone: "Asia/Shanghai",
      public_base_url: "https://moment.example.com",
      avatar_media_id: null,
      cover_media_id: null,
      pinned_post_id: null,
      comments_enabled: true,
    },
    meta: {
      instance_id: "0123456789abcdef0123456789abcdef",
      request_id: "req_site",
    },
  };
}

function ownerStatusEnvelope() {
  return {
    data: {
      initialized: true,
      password_enabled: false,
      session_epoch: 3,
      initialized_at: "2026-08-01T00:00:00Z",
      rotated_at: "2026-08-09T00:00:00Z",
    },
    meta: { request_id: "req_owner" },
  };
}

function mergePlanData() {
  const defaultSelection = {
    plan_id: mergePlanID,
    archive_sha256: archiveSHA,
    base_generation: "gen-old",
    base_authority_sha256: baseAuthoritySHA,
    items: [
      { kind: "entry", id: "post-conflict", action: "keep_local" },
      { kind: "entry", id: "post-new", action: "add" },
    ],
    site_fields: [{ field: "title", action: "keep_local" }],
  };
  return {
    plan: {
      version: 1,
      plan_id: mergePlanID,
      archive_sha256: archiveSHA,
      base_generation: "gen-old",
      base_authority_sha256: baseAuthoritySHA,
      incoming_authority_sha256: incomingAuthoritySHA,
      profile_user_id: "00000000000000000000000000000000",
      expires_at: "2999-08-11T02:00:00Z",
      summary: {
        unchanged: 0,
        incoming_only: 2,
        local_only: 1,
        conflict: 3,
      },
      items: [
        {
          kind: "entry",
          id: "post-conflict",
          classification: "conflict",
          changed_fields: ["content"],
          dependencies: [],
          selectable: true,
        },
        {
          kind: "entry",
          id: "post-new",
          classification: "incoming_only",
          changed_fields: [],
          dependencies: [],
          selectable: true,
        },
        {
          kind: "entry",
          id: "post-after-export",
          classification: "local_only",
          changed_fields: [],
          dependencies: [],
          selectable: false,
        },
        // Reported, never selectable.
        {
          kind: "user",
          id: "11111111111111111111111111111111",
          classification: "conflict",
          changed_fields: ["display_name"],
          dependencies: [],
          selectable: false,
        },
        {
          kind: "identity",
          id: "github:4242",
          classification: "incoming_only",
          changed_fields: [],
          dependencies: [
            { kind: "user", id: "11111111111111111111111111111111" },
          ],
          selectable: false,
        },
      ],
      site_fields: [
        {
          field: "title",
          classification: "conflict",
          local_value: "当前标题",
          incoming_value: "备份标题",
          dependencies: [],
        },
      ],
      default_selection: defaultSelection,
    },
    review: {
      incoming_only: {
        total: 2,
        truncated: false,
        items: [
          {
            kind: "entry",
            id: "post-new",
            classification: "incoming_only",
            changed_fields: [],
            incoming: {
              id: "post-new",
              content_md: "备份里新增的 Moment 正文",
              created_at: "2026-08-10T08:00:00Z",
              published_at: "2026-08-10T08:00:00Z",
              media: [],
            },
          },
          {
            kind: "identity",
            id: "github:4242",
            classification: "incoming_only",
            changed_fields: [],
            incoming: makeReviewIdentity(),
          },
        ],
      },
      local_only: {
        total: 1,
        truncated: false,
        items: [
          {
            kind: "entry",
            id: "post-after-export",
            classification: "local_only",
            changed_fields: [],
            local: {
              id: "post-after-export",
              content_md: "导出备份后新发布的 Moment",
              created_at: "2026-08-11T08:00:00Z",
              published_at: "2026-08-11T08:00:00Z",
              location: {
                name: "陆悦天地",
                display_name: "中科路1600号",
                country_code: "CN",
                country: "中国",
                region: "上海市",
                city: "上海市",
                district: "浦东新区",
                category: "mall",
                provider: "manual",
                provider_place_id: "",
                latitude: null,
                longitude: null,
                coordinate_system: null,
              },
              media: [],
            },
          },
        ],
      },
      conflicts: {
        total: 2,
        truncated: false,
        items: [
          {
            kind: "user",
            id: "11111111111111111111111111111111",
            classification: "conflict",
            changed_fields: ["display_name"],
            local: makeReviewUser(),
            incoming: makeReviewUser({ display_name: "林林（备份）" }),
          },
          {
            kind: "entry",
            id: "post-conflict",
            classification: "conflict",
            changed_fields: ["content"],
            local: {
              id: "post-conflict",
              content_md: "当前站点修改后的正文",
              created_at: "2026-08-09T08:00:00Z",
              published_at: "2026-08-09T08:00:00Z",
              media: [],
            },
            incoming: {
              id: "post-conflict",
              content_md: "备份文件中的旧正文",
              created_at: "2026-08-09T08:00:00Z",
              published_at: "2026-08-09T08:00:00Z",
              media: [],
            },
          },
        ],
      },
      site_fields: [
        {
          field: "title",
          classification: "conflict",
          local_value: "当前标题",
          incoming_value: "备份标题",
        },
      ],
    },
    incoming_counts: makeImportCounts({
      entries: 2,
      users: 3,
      articles: 0,
      article_drafts: 0,
      identities: 3,
    }),
    incoming_media_bytes: 0,
    disk: {
      archive_bytes: 5,
      expanded_bytes: 3_000,
      staging_bytes: 5_000,
      pre_import_backup_bytes: 2_000,
      rollback_reserve_bytes: 2_000,
      safety_margin_bytes: 2 ** 31,
      required_additional_bytes: 2 ** 31 + 9_000,
      estimated_peak_footprint_bytes: 2 ** 31 + 11_005,
      available_bytes: 2 ** 31 + 30_000,
      space_ok: true,
    },
    replacement: {
      counts: makeImportCounts({ entries: 3, users: 2, identities: 2 }),
      media_bytes: 0,
      data_bytes: 2_000,
    },
    merge_allowed: true,
    merge_blocked_reason: null,
    warnings: [],
    expires_at: "2999-08-11T02:00:00Z",
    staging_token: mergeToken,
  };
}

type Phase =
  "claimed" | "succeeded" | "failed" | "rolled_back" | "needs_attention";

function statusData(phase: Phase, id = operationID) {
  const previous = "gen-old";
  const succeeded = phase === "succeeded";
  const rolledBack = phase === "rolled_back";
  const needsAttention = phase === "needs_attention";
  const failed = phase === "failed";
  const active = succeeded ? id : previous;
  const backupVerified = phase !== "claimed" && !failed;
  const data: Record<string, unknown> = {
    operation_id: id,
    phase,
    active_generation: active,
    previous_generation: previous,
    new_generation: id,
    backup_verified: backupVerified,
    rollback_attempted: rolledBack || needsAttention,
    rollback_succeeded: rolledBack,
    started_at: "2026-08-11T01:30:00Z",
    updated_at: "2026-08-11T01:30:01Z",
  };
  if (backupVerified) data.backup_name = "pre-import-20260811.zip";
  if (failed || rolledBack || needsAttention) {
    data.error_code = "DATA_INTEGRITY_FAILED";
  }
  if (succeeded || failed || rolledBack) {
    data.completed_at = "2026-08-11T01:30:01Z";
  }
  return data;
}

class ArchiveRequest extends EventTarget {
  static requests: ArchiveRequest[] = [];

  readonly upload = new EventTarget();
  readonly headers = new Map<string, string>();
  method = "";
  url = "";
  withCredentials = false;
  status = 0;
  responseText = "";
  sent: Document | XMLHttpRequestBodyInit | null = null;
  aborted = false;

  constructor() {
    super();
    ArchiveRequest.requests.push(this);
  }

  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }

  setRequestHeader(name: string, value: string) {
    this.headers.set(name, value);
  }

  getResponseHeader(name: string) {
    return name.toLowerCase() === "content-type" ? "application/json" : null;
  }

  send(body: Document | XMLHttpRequestBodyInit | null) {
    this.sent = body;
  }

  abort() {
    this.aborted = true;
    this.dispatchEvent(new Event("abort"));
  }

  fail() {
    this.dispatchEvent(new Event("error"));
  }

  respond(data: unknown, status = 200) {
    this.status = status;
    this.responseText = JSON.stringify({
      data,
      meta: { request_id: "req_import" },
    });
    this.dispatchEvent(new Event("load"));
  }

  respondFailure(code: string, status: number) {
    this.status = status;
    this.responseText = JSON.stringify({
      error: {
        code,
        message: code.toLowerCase(),
        details: {},
        request_id: "req_import_error",
      },
    });
    this.dispatchEvent(new Event("load"));
  }
}

function installSession() {
  window.sessionStorage.setItem(
    sessionKey,
    JSON.stringify({
      csrf_token: "csrf-settings",
      expires_at: "2999-08-11T01:00:00.000Z",
    }),
  );
}

function accountSessionEnvelope() {
  return {
    data: {
      expires_at: "2999-08-11T01:00:00.000Z",
      csrf_token: "csrf-from-cookie",
      account: {
        user_id: "00000000000000000000000000000000",
        username: "owner",
        display_name: "乔乔",
        bio: "",
        avatar_media_id: null,
        cover_media_id: null,
        role: "admin",
        profile_url: "/owner",
        status: "active",
        username_chosen: true,
      },
      limits: makeAccountLimits(),
    },
    meta: { request_id: "req_account_session" },
  };
}

function signedOutEnvelope() {
  return {
    error: {
      code: "UNAUTHORIZED",
      message: "auth required",
      details: {},
      request_id: "req_auth",
    },
  };
}

function installFetch(
  importStatus: () => Promise<Response> = async () =>
    failure("IMPORT_STAGING_INVALID", 409),
  extra?: (url: string, init?: RequestInit) => Promise<Response> | undefined,
) {
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/api/v1/site") return json(siteEnvelope());
      if (url === "/api/v1/status") return json(ownerStatusEnvelope());
      if (url === "/api/v1/account/session") {
        return window.sessionStorage.getItem(sessionKey)
          ? json(accountSessionEnvelope())
          : json(signedOutEnvelope(), 401);
      }
      if (url === "/api/v1/backups/import/status") return importStatus();
      const routed = extra?.(url, init);
      if (routed) return routed;
      return json({}, 404);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

async function renderSettings(waitForPicker = true) {
  render(<App />);
  await screen.findByRole("heading", { name: "备份与恢复" });
  if (waitForPicker) {
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "选择备份文件" }),
      ).toBeEnabled(),
    );
  }
}

async function createReview(file: File) {
  fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
    target: { files: [file] },
  });
  fireEvent.click(screen.getByRole("button", { name: "检查备份" }));
  const request = ArchiveRequest.requests.at(-1)!;
  expect(request.url).toBe("/api/v1/backups/import/review");
  request.respond(mergePlanData(), 201);
  await screen.findByRole("heading", { name: "审阅并合并" });
}

describe("owner backup and restore", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/settings/backups");
    window.sessionStorage.clear();
    window.localStorage.clear();
    installSession();
    ArchiveRequest.requests = [];
    vi.stubGlobal("XMLHttpRequest", ArchiveRequest);
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    );
    vi.stubGlobal("scrollTo", vi.fn());
    vi.stubGlobal(
      "requestAnimationFrame",
      vi.fn((callback: FrameRequestCallback) => {
        callback(0);
        return 1;
      }),
    );
  });

  it("reviews merge conflicts and commits the explicit selection", async () => {
    const fetcher = installFetch(
      async () => failure("IMPORT_STAGING_INVALID", 409),
      (url) => {
        if (url === "/api/v1/backups/import/merge/commit") {
          return Promise.resolve(
            json({
              data: { status: statusData("succeeded") },
              meta: { request_id: "req_merge" },
            }),
          );
        }
        return undefined;
      },
    );
    await renderSettings();
    const file = new File(["merge"], "merge.zip", {
      type: "application/zip",
    });
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: { files: [file] },
    });
    fireEvent.click(screen.getByRole("button", { name: "检查备份" }));
    const request = ArchiveRequest.requests.at(-1)!;
    expect(request.url).toBe("/api/v1/backups/import/review");
    request.respond(mergePlanData(), 201);

    await screen.findByRole("heading", { name: "审阅并合并" });
    expect(screen.getByText("导出备份后新发布的 Moment")).toBeInTheDocument();
    expect(screen.getByText("当前站点修改后的正文")).toBeInTheDocument();
    expect(screen.getByText("备份文件中的旧正文")).toBeInTheDocument();

    // Accounts are reported, never chosen. They get their own section with the
    // reason stated, rather than a row of controls the server would refuse or
    // -- worse -- a silent omission of the people a restore brings with it.
    const accounts = screen.getByRole("region", { name: "账户与登录方式" });
    // Both sides of the conflict are shown, so the handle appears twice.
    expect(within(accounts).getAllByText("@linlin")).toHaveLength(2);
    expect(within(accounts).getAllByText("成员").length).toBeGreaterThan(0);
    expect(within(accounts).getAllByText("已启用").length).toBeGreaterThan(0);
    expect(within(accounts).getByText("林林（备份）")).toBeInTheDocument();
    expect(within(accounts).getByText("GitHub")).toBeInTheDocument();
    expect(
      within(accounts).getByText("linlin@example.com"),
    ).toBeInTheDocument();
    expect(
      within(accounts).getByText("两侧都有 · 以当前站点为准"),
    ).toBeInTheDocument();
    expect(
      within(accounts).getByText("备份独有 · 会被加入"),
    ).toBeInTheDocument();
    expect(
      within(accounts).getByText(/“两者都保留”会把同一个人拆成两个/),
    ).toBeInTheDocument();
    expect(within(accounts).queryAllByRole("radio")).toHaveLength(0);
    expect(within(accounts).queryAllByRole("radiogroup")).toHaveLength(0);
    // And they stay out of the lists that do carry controls.
    expect(
      screen.queryByRole("radiogroup", { name: /账户/ }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("radio", { name: "两者都保留" }));
    fireEvent.click(screen.getByRole("radio", { name: "使用备份值" }));
    expect(
      screen.queryByRole("combobox", { name: /冲突处理/ }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "提交合并" }));
    expect(
      await screen.findByRole("heading", {
        name: "按当前选择合并这份备份？",
      }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "确认并合并" }));

    expect(
      await screen.findByText("备份已合并并通过就绪检查"),
    ).toBeInTheDocument();
    const commitCall = fetcher.mock.calls.find(
      ([input]) => String(input) === "/api/v1/backups/import/merge/commit",
    );
    expect(commitCall).toBeDefined();
    const body = JSON.parse(String(commitCall?.[1]?.body)) as {
      staging_token: string;
      selection: {
        items: Array<{ id: string; action: string }>;
        site_fields: Array<{ field: string; action: string }>;
      };
    };
    expect(body.staging_token).toBe(mergeToken);
    expect(body.selection.items).toContainEqual({
      kind: "entry",
      id: "post-conflict",
      action: "keep_both",
    });
    expect(body.selection.site_fields).toContainEqual({
      field: "title",
      action: "use_incoming",
    });
  });

  /**
   * A section heading has to count the section.
   *
   * Account rows are lifted out of these two lists into their own read-only
   * section, and the headings went on printing the server's untouched bucket
   * total -- so an archive carrying accounts made every heading claim more
   * items than the list beneath it held, and the difference was exactly the
   * people the reader had just been shown somewhere else.
   */
  it("counts the rows each merge section actually shows", async () => {
    installFetch(async () => failure("IMPORT_STAGING_INVALID", 409));
    await renderSettings();
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: {
        files: [new File(["merge"], "merge.zip", { type: "application/zip" })],
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "检查备份" }));
    ArchiveRequest.requests.at(-1)!.respond(mergePlanData(), 201);

    await screen.findByRole("heading", { name: "审阅并合并" });

    // The backup's incoming-only bucket holds two rows and one of them is a
    // credential, which this section does not render.
    const incoming = screen.getByRole("heading", { name: "备份新增" });
    expect(incoming.nextElementSibling).toHaveTextContent(/^1 项$/);
    expect(screen.getByText("备份里新增的 Moment 正文")).toBeInTheDocument();

    // The current-only bucket carries no accounts, so nothing moved.
    const localOnly = screen.getByRole("heading", {
      name: "当前独有 · 会保留",
    });
    expect(localOnly.nextElementSibling).toHaveTextContent(/^1 项$/);
  });

  it("says which refusal blocked the merge", async () => {
    installFetch(async () => failure("IMPORT_STAGING_INVALID", 409));
    await renderSettings();
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: {
        files: [new File(["merge"], "merge.zip", { type: "application/zip" })],
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "检查备份" }));
    ArchiveRequest.requests.at(-1)!.respond(
      {
        ...mergePlanData(),
        merge_allowed: false,
        merge_blocked_reason: "USERNAME_TAKEN",
      },
      201,
    );

    await screen.findByRole("heading", { name: "审阅覆盖恢复" });
    expect(
      screen.getByText(/同一个用户名，合并无法决定该给谁改名/),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "安全合并" })).toBeDisabled();
  });

  it("discards an uncommitted merge plan when leaving settings so a later file can be inspected", async () => {
    const fetcher = installFetch(
      async () => failure("IMPORT_STAGING_INVALID", 409),
      (url) =>
        url === "/api/v1/backups/import/merge/discard"
          ? Promise.resolve(
              json({
                data: { discarded: true },
                meta: { request_id: "req_discard" },
              }),
            )
          : undefined,
    );
    const view = render(<App />);
    await screen.findByRole("heading", { name: "备份与恢复" });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "选择备份文件" }),
      ).toBeEnabled(),
    );

    const first = new File(["merge-one"], "first.zip", {
      type: "application/zip",
    });
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: { files: [first] },
    });
    fireEvent.click(screen.getByRole("button", { name: "检查备份" }));
    ArchiveRequest.requests.at(-1)!.respond(mergePlanData(), 201);
    await screen.findByRole("heading", { name: "审阅并合并" });

    view.unmount();
    await waitFor(() =>
      expect(fetcher).toHaveBeenCalledWith(
        "/api/v1/backups/import/merge/discard",
        expect.objectContaining({
          body: JSON.stringify({ staging_token: mergeToken }),
          keepalive: true,
          method: "POST",
        }),
      ),
    );

    render(<App />);
    await screen.findByRole("heading", { name: "备份与恢复" });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "选择备份文件" }),
      ).toBeEnabled(),
    );
    const second = new File(["merge-two"], "second.zip", {
      type: "application/zip",
    });
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: { files: [second] },
    });
    fireEvent.click(screen.getByRole("button", { name: "检查备份" }));
    const nextRequest = ArchiveRequest.requests.at(-1)!;
    expect(nextRequest.url).toBe("/api/v1/backups/import/review");
    nextRequest.respond(mergePlanData(), 201);
    expect(
      await screen.findByRole("heading", { name: "审阅并合并" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(
        "另一个恢复或维护操作正在进行。请先查询当前状态，稍后再预检。",
      ),
    ).not.toBeInTheDocument();
  });

  it("treats an explicitly stale merge plan as rejected instead of resubmitting", async () => {
    const fetcher = installFetch(
      async () => failure("IMPORT_STAGING_INVALID", 409),
      (url) =>
        url === "/api/v1/backups/import/merge/commit"
          ? Promise.resolve(failure("IMPORT_MERGE_PLAN_STALE", 409))
          : undefined,
    );
    await renderSettings();
    const file = new File(["merge"], "stale.zip", {
      type: "application/zip",
    });
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: { files: [file] },
    });
    fireEvent.click(screen.getByRole("button", { name: "检查备份" }));
    ArchiveRequest.requests.at(-1)!.respond(mergePlanData(), 201);
    await screen.findByRole("heading", { name: "审阅并合并" });
    fireEvent.click(screen.getByRole("button", { name: "提交合并" }));
    fireEvent.click(screen.getByRole("button", { name: "确认并合并" }));

    expect(
      await screen.findByText(
        "合并计划已过期或当前站点已变化，请重新检查合并冲突。",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "审阅并合并" }),
    ).not.toBeInTheDocument();
    expect(
      fetcher.mock.calls.filter(
        ([input]) => String(input) === "/api/v1/backups/import/merge/commit",
      ),
    ).toHaveLength(1);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("keeps the initial view focused on status, export, and file entry", async () => {
    installFetch();
    await renderSettings();

    expect(
      screen.getByRole("heading", { name: "当前没有进行中的恢复" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "下载整站备份" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "选择备份文件" })).toBeEnabled();
    expect(
      screen.queryByRole("heading", { name: "恢复前影响清单" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("暂存重建")).not.toBeInTheDocument();
  });

  it("keeps settings read-only until the initial restore status is known", async () => {
    let resolveInitial!: (response: Response) => void;
    installFetch(
      () =>
        new Promise<Response>((resolve) => {
          resolveInitial = resolve;
        }),
    );
    render(<App />);
    await screen.findByRole("heading", { name: "备份与恢复" });
    await waitFor(() => expect(resolveInitial).toBeTypeOf("function"));
    expect(screen.getByRole("button", { name: "选择备份文件" })).toBeDisabled();
    await act(async () => {
      resolveInitial(failure("IMPORT_STAGING_INVALID", 409));
    });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "选择备份文件" }),
      ).toBeEnabled(),
    );
  });

  it("uses native same-origin streaming download once without buffering", async () => {
    const fetcher = installFetch();
    const clicks: Array<{ href: string; download: string }> = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clicks.push({
        href: this.getAttribute("href") ?? "",
        download: this.download,
      });
    });
    await renderSettings();

    const button = screen.getByRole("button", { name: "下载整站备份" });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(clicks).toEqual([
      { href: "/api/v1/backups/export", download: "moment-export.zip" },
    ]);
    expect(await screen.findByText(/已请求下载/)).toBeInTheDocument();
    expect(fetcher).not.toHaveBeenCalledWith(
      "/api/v1/backups/export",
      expect.anything(),
    );
  });

  it("releases the native download guard even while status reconnects", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let statusCalls = 0;
    installFetch(async () => {
      statusCalls += 1;
      if (statusCalls === 1) throw new TypeError("offline");
      return failure("IMPORT_STAGING_INVALID", 409);
    });
    const clicks: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clicks.push(this.getAttribute("href") ?? "");
    });
    render(<App />);
    await screen.findByRole("heading", { name: "备份与恢复" });
    await screen.findByRole("button", { name: "立即重试状态" });
    expect(screen.getByRole("button", { name: "选择备份文件" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "选择备份文件" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "下载整站备份" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "立即重试状态" }),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "选择备份文件" }),
      ).toBeEnabled(),
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    fireEvent.click(screen.getByRole("button", { name: "下载整站备份" }));
    expect(clicks).toEqual([
      "/api/v1/backups/export",
      "/api/v1/backups/export",
    ]);
  });

  it("shows current-only content before an exact replacement and commits the reviewed plan", async () => {
    vi.spyOn(window, "scrollY", "get").mockReturnValue(720);
    const fetcher = installFetch(
      async () => failure("IMPORT_STAGING_INVALID", 409),
      (url) =>
        url === "/api/v1/backups/import/review/replace"
          ? Promise.resolve(
              json({
                data: { status: statusData("succeeded") },
                meta: { request_id: "req_replace" },
              }),
            )
          : undefined,
    );
    await renderSettings();
    await createReview(new File(["merge"], "backup.zip"));

    fireEvent.click(screen.getByRole("button", { name: "完全替换" }));
    expect(
      await screen.findByRole("heading", { name: "审阅覆盖恢复" }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("当前独有 · 将移除")).toHaveLength(2);
    expect(screen.getByText("导出备份后新发布的 Moment")).toBeInTheDocument();
    expect(screen.getByTitle("中科路1600号")).toHaveTextContent(
      "陆悦天地 · 浦东新区",
    );

    fireEvent.click(screen.getByRole("button", { name: "确认完全替换" }));
    const dialog = await screen.findByRole("dialog", {
      name: "完全替换当前站点？",
    });
    expect(within(dialog).getByText("1 项将移除")).toBeInTheDocument();
    // A replacement takes the account graph with it, so the confirmation has
    // to say how many accounts arrive and how many stop existing. Reading
    // that number off the archive is the whole reason the server sends it.
    expect(
      within(dialog).getByText("2 Moment · 0 评论 · 3 账户"),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("2 个将被替换")).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "关闭覆盖恢复确认" }),
    ).toBeInTheDocument();
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.top).toBe("");

    fireEvent.click(
      within(dialog).getByRole("button", { name: "确认完全替换" }),
    );
    expect(
      await screen.findByText("备份已恢复并通过就绪检查"),
    ).toBeInTheDocument();
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.documentElement.style.overflow).toBe("");

    const call = fetcher.mock.calls.find(
      ([input]) => String(input) === "/api/v1/backups/import/review/replace",
    );
    expect(call).toBeDefined();
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({
      staging_token: mergeToken,
      plan_id: mergePlanID,
    });
  });

  it("rejects an exact replacement when the site changed after review", async () => {
    const fetcher = installFetch(
      async () => failure("IMPORT_STAGING_INVALID", 409),
      (url) =>
        url === "/api/v1/backups/import/review/replace"
          ? Promise.resolve(failure("IMPORT_MERGE_PLAN_STALE", 409))
          : undefined,
    );
    await renderSettings();
    await createReview(new File(["merge"], "backup.zip"));
    fireEvent.click(screen.getByRole("button", { name: "完全替换" }));
    fireEvent.click(screen.getByRole("button", { name: "确认完全替换" }));
    fireEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "确认完全替换",
      }),
    );

    expect(
      await screen.findByText(
        "审阅后当前站点已经发生变化。为保护新内容，恢复已拒绝；请重新检查备份。",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "审阅覆盖恢复" }),
    ).not.toBeInTheDocument();
    expect(
      fetcher.mock.calls.filter(
        ([input]) => String(input) === "/api/v1/backups/import/review/replace",
      ),
    ).toHaveLength(1);
  });

  it("aborts an obsolete review when another file is selected", async () => {
    installFetch();
    await renderSettings();
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: { files: [new File(["first"], "first.zip")] },
    });
    fireEvent.click(screen.getByRole("button", { name: "检查备份" }));
    const stale = ArchiveRequest.requests[0];

    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: { files: [new File(["second"], "second.zip")] },
    });
    expect(stale.aborted).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "检查备份" }));
    stale.respond(mergePlanData(), 201);
    ArchiveRequest.requests[1].respond(mergePlanData(), 201);
    expect(await screen.findByText(/second\.zip/)).toBeInTheDocument();
    expect(screen.queryByText(/first\.zip/)).not.toBeInTheDocument();
  });

  it.each([
    ["failed", "恢复在切换前停止"],
    ["rolled_back", "已自动恢复原站点"],
    ["needs_attention", "恢复状态需要人工处理"],
  ] as const)("explains the %s terminal outcome", async (phase, title) => {
    installFetch(async () =>
      json({
        data: statusData(phase),
        meta: { request_id: `req_${phase}` },
      }),
    );
    await renderSettings(phase !== "needs_attention");
    expect(await screen.findByText(title)).toBeInTheDocument();
    if (phase === "needs_attention") {
      expect(
        screen.getByText("moment doctor --deep --json", { exact: false }),
      ).toBeInTheDocument();
      const errorCode = screen.getByText("DATA_INTEGRITY_FAILED");
      expect(errorCode).toBeInTheDocument();
      expect(errorCode.closest("details")).toHaveAttribute("open");
      expect(
        screen.getByRole("button", { name: "选择备份文件" }),
      ).toBeDisabled();
    }
  });
});
