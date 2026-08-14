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

const sessionKey = "moment.owner.session.v1";
const operationID = "import-20260811T013000000Z-0011223344556677";
const oldOperationID = "import-20260810T013000000Z-8899aabbccddeeff";
const baselineOperationID = "import-20260811T012000000Z-aabbccddeeff0011";
const archiveSHA = "a".repeat(64);

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
      owner_id: "00000000000000000000000000000000",
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

function planData(archiveBytes: number) {
  const replacement = 2_000;
  const staging = 5_000;
  const safety = 2 ** 31;
  const required = staging + replacement * 2 + safety;
  return {
    dry_run: true,
    format_version: 1,
    archive_sha256: archiveSHA,
    counts: { entries: 12, comments: 4, media: 3 },
    time_range: {
      from: "2024-01-02T03:04:05Z",
      to: "2026-08-10T00:00:00.1Z",
    },
    media_bytes: 1_000,
    missing_alt: 1,
    disk: {
      archive_bytes: archiveBytes,
      expanded_bytes: 3_000,
      staging_bytes: staging,
      pre_import_backup_bytes: replacement,
      rollback_reserve_bytes: replacement,
      safety_margin_bytes: safety,
      required_additional_bytes: required,
      estimated_peak_footprint_bytes: archiveBytes + replacement + required,
      available_bytes: required + 20_000,
      space_ok: true,
    },
    replacement: {
      counts: { entries: 8, comments: 2, media: 2 },
      media_bytes: 1_000,
      data_bytes: replacement,
    },
    warnings: ["1 media usages have no image description"],
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

  respond(data: unknown) {
    this.status = 200;
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

function installFetch(
  importStatus: () => Promise<Response> = async () =>
    failure("IMPORT_STAGING_INVALID", 409),
  extra?: (url: string, init?: RequestInit) => Promise<Response> | undefined,
) {
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/api/v1/site") return json(siteEnvelope());
      if (url === "/api/v1/owner/status") return json(ownerStatusEnvelope());
      if (url === "/api/v1/owner/import/status") return importStatus();
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

async function createPlan(file: File) {
  fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
    target: { files: [file] },
  });
  fireEvent.click(screen.getByRole("button", { name: "开始预检" }));
  const request = ArchiveRequest.requests.at(-1)!;
  expect(request.url).toBe("/api/v1/owner/import/plan");
  request.respond(planData(file.size));
  await screen.findByRole("heading", { name: "恢复前影响清单" });
}

describe("owner backup and restore", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/owner/settings");
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
    expect(screen.getByLabelText(/^时区/)).toBeDisabled();
    expect(screen.getByRole("button", { name: "更换密钥" })).toBeDisabled();
    await act(async () => {
      resolveInitial(failure("IMPORT_STAGING_INVALID", 409));
    });
    await waitFor(() => expect(screen.getByLabelText(/^时区/)).toBeEnabled());
    expect(screen.getByRole("button", { name: "更换密钥" })).toBeEnabled();
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
      { href: "/api/v1/owner/export", download: "moment-export.zip" },
    ]);
    expect(await screen.findByText(/已请求下载/)).toBeInTheDocument();
    expect(fetcher).not.toHaveBeenCalledWith(
      "/api/v1/owner/export",
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
    expect(screen.getByLabelText(/^时区/)).toBeDisabled();
    expect(screen.getByRole("button", { name: "更换密钥" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "选择备份文件" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "下载整站备份" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "立即重试状态" }),
    );
    await waitFor(() => expect(screen.getByLabelText(/^时区/)).toBeEnabled());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    fireEvent.click(screen.getByRole("button", { name: "下载整站备份" }));
    expect(clicks).toEqual(["/api/v1/owner/export", "/api/v1/owner/export"]);
  });

  it("summarizes impact and reveals the complete ledger on explicit intent", async () => {
    installFetch();
    await renderSettings();
    const file = new File(["portable archive"], "长文件名-<script>.zip", {
      type: "text/plain",
    });
    await createPlan(file);

    const decision = screen.getByLabelText("恢复决定摘要");
    expect(within(decision).getByText("12 条 Moment")).toBeInTheDocument();
    expect(within(decision).getByText("8 条 Moment")).toBeInTheDocument();
    expect(within(decision).getByText("容量检查")).toBeInTheDocument();
    expect(within(decision).getByText("通过")).toBeInTheDocument();

    const summary = screen.getByText("查看完整替换、容量与校验账本");
    const details = summary.closest("details");
    expect(details).not.toHaveAttribute("open");
    expect(summary.tabIndex).toBe(0);
    summary.focus();
    expect(summary).toHaveFocus();
    fireEvent.click(summary);
    await waitFor(() => expect(details).toHaveAttribute("open"));
    expect(screen.getByText(/格式 v1/)).toBeInTheDocument();
    expect(screen.getByText(/自动恢复前备份/)).toBeInTheDocument();
    expect(screen.getByText("恢复所需空间")).toBeInTheDocument();
    expect(screen.getByText(/1 media usages/)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/^时区/), {
      target: { value: "Asia/Tokyo" },
    });
    expect(
      screen.getByRole("button", { name: "确认替换当前站点" }),
    ).toBeDisabled();
    expect(screen.getByText(/先保存或放弃当前站点设置/)).toBeInTheDocument();
    expect(ArchiveRequest.requests).toHaveLength(1);
  });

  it("blocks restore while a newly rotated credential is visible", async () => {
    installFetch(undefined, (url, init) => {
      if (url !== "/api/v1/owner/token/rotate" || init?.method !== "POST") {
        return undefined;
      }
      return Promise.resolve(
        json({
          data: {
            token: "one-time-owner-token",
            initialized: true,
            session_epoch: 4,
            initialized_at: "2026-08-01T00:00:00Z",
            rotated_at: "2026-08-11T00:00:00Z",
          },
          meta: { request_id: "req_rotate" },
        }),
      );
    });
    await renderSettings();
    await createPlan(new File(["portable archive"], "backup.zip"));
    fireEvent.click(screen.getByRole("button", { name: "更换密钥" }));
    fireEvent.click(await screen.findByRole("button", { name: "确认更换" }));
    expect(await screen.findByText("one-time-owner-token")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "确认替换当前站点" }),
    ).toBeDisabled();
    expect(ArchiveRequest.requests).toHaveLength(1);
  });

  it("aborts a stale plan when the selected file changes", async () => {
    installFetch();
    await renderSettings();
    const first = new File(["first archive"], "first.zip");
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: { files: [first] },
    });
    fireEvent.click(screen.getByRole("button", { name: "开始预检" }));
    const stale = ArchiveRequest.requests[0];

    const second = new File(["second archive"], "second.zip");
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: { files: [second] },
    });
    expect(stale.aborted).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "开始预检" }));
    stale.respond(planData(first.size));
    ArchiveRequest.requests[1].respond(planData(second.size));
    expect(await screen.findByText(/second\.zip/)).toBeInTheDocument();
    expect(screen.queryByText(/first\.zip/)).not.toBeInTheDocument();
  });

  it("keeps invalid and insufficient-space archives before the destructive boundary", async () => {
    installFetch();
    await renderSettings();
    const first = new File(["invalid archive"], "invalid.zip");
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: { files: [first] },
    });
    fireEvent.click(screen.getByRole("button", { name: "开始预检" }));
    ArchiveRequest.requests[0].respondFailure("EXPORT_INVALID", 422);
    expect(
      await screen.findByText(/不是可恢复的 Moment 备份/),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "确认替换当前站点" }),
    ).not.toBeInTheDocument();

    const second = new File(["large archive"], "large.zip");
    fireEvent.change(screen.getByLabelText("选择 Moment 备份 ZIP"), {
      target: { files: [second] },
    });
    fireEvent.click(screen.getByRole("button", { name: "开始预检" }));
    ArchiveRequest.requests[1].respondFailure(
      "IMPORT_DISK_SPACE_INSUFFICIENT",
      507,
    );
    expect(await screen.findByText(/可用磁盘空间不足/)).toBeInTheDocument();
    expect(ArchiveRequest.requests).toHaveLength(2);
  });

  it("reuses the exact file and SHA, closes confirmation, and preserves browser state", async () => {
    vi.spyOn(window, "scrollY", "get").mockReturnValue(720);
    const fetcher = installFetch();
    const draftKey = "moment.compose.draft.v1:instance";
    window.localStorage.setItem(draftKey, "draft stays local");
    await renderSettings();
    const file = new File(["portable archive"], "backup.zip");
    await createPlan(file);

    const trigger = screen.getByRole("button", {
      name: "确认替换当前站点",
    });
    fireEvent.click(trigger);
    const dialog = await screen.findByRole("dialog", {
      name: "用这份备份替换当前站点？",
    });
    expect(document.documentElement.style.overflowY).toBe("hidden");
    expect(document.body.style.position).toBe("fixed");
    expect(document.body.style.top).toBe("-720px");
    const finalImpact = within(dialog).getByLabelText("最终恢复影响");
    expect(
      within(finalImpact).getByText(/12 Moment · 4 评论 · 3 媒体/),
    ).toBeInTheDocument();
    expect(
      within(finalImpact).getByText(/8 Moment · 2 评论 · 2 媒体/),
    ).toBeInTheDocument();
    expect(within(finalImpact).getByText(/已通过 · 还需/)).toBeInTheDocument();
    expect(
      within(dialog).getByText("这是覆盖恢复，不会合并数据。"),
    ).toBeInTheDocument();
    const cancel = await screen.findByRole("button", { name: "返回检查" });
    expect(cancel).toHaveFocus();
    fireEvent.click(screen.getByRole("button", { name: "确认并开始恢复" }));
    await waitFor(() => expect(ArchiveRequest.requests).toHaveLength(2));
    expect(screen.getByText("正在确认服务器状态").closest("div")).toHaveFocus();
    expect(screen.getByLabelText(/^时区/)).toBeDisabled();
    expect(screen.getByRole("button", { name: "更换密钥" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/^时区/), {
      target: { value: "Asia/Tokyo" },
    });
    expect(screen.getByLabelText(/^时区/)).toHaveValue("Asia/Shanghai");
    expect(
      fetcher.mock.calls.some(
        ([url, init]) =>
          String(url) === "/api/v1/owner/site" && init?.method === "PATCH",
      ),
    ).toBe(false);
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "用这份备份替换当前站点？" }),
      ).not.toBeInTheDocument(),
    );
    expect(document.documentElement.style.overflowY).toBe("");
    expect(document.body.style.position).toBe("");
    expect(window.scrollTo).toHaveBeenCalledWith(0, 720);

    const restoreRequest = ArchiveRequest.requests[1];
    expect(restoreRequest.url).toBe("/api/v1/owner/import/restore");
    expect(restoreRequest.sent).toBe(file);
    expect(restoreRequest.headers.get("X-Moment-Archive-SHA256")).toBe(
      archiveSHA,
    );
    restoreRequest.respond({ status: statusData("succeeded") });
    expect(
      await screen.findByText("备份已恢复并通过就绪检查"),
    ).toBeInTheDocument();
    expect(ArchiveRequest.requests).toHaveLength(2);
    expect(window.localStorage.getItem(draftKey)).toBe("draft stays local");
    expect(window.sessionStorage.getItem(sessionKey)).not.toBeNull();
    expect(screen.getByLabelText(/^时区/)).toBeDisabled();
  });

  it("refreshes the baseline and ignores it after an ambiguous restore", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let statusCalls = 0;
    installFetch(async () => {
      statusCalls += 1;
      if (statusCalls === 1) {
        return json({
          data: statusData("succeeded", oldOperationID),
          meta: { request_id: "req_initial_a" },
        });
      }
      if (statusCalls <= 3) {
        return json({
          data: statusData("succeeded", baselineOperationID),
          meta: { request_id: `req_baseline_b_${statusCalls}` },
        });
      }
      return json({
        data: statusData(statusCalls === 4 ? "claimed" : "succeeded"),
        meta: { request_id: `req_new_${statusCalls}` },
      });
    });
    await renderSettings();
    await screen.findByText("备份已恢复并通过就绪检查");
    const file = new File(["portable archive"], "backup.zip");
    await createPlan(file);
    fireEvent.click(screen.getByRole("button", { name: "确认替换当前站点" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "确认并开始恢复" }),
    );
    await waitFor(() => expect(statusCalls).toBe(2));
    await waitFor(() => expect(ArchiveRequest.requests).toHaveLength(2));
    ArchiveRequest.requests[1].fail();
    expect(await screen.findByText(/恢复结果尚未确认/)).toBeInTheDocument();
    expect(
      screen.queryByText("备份已恢复并通过就绪检查"),
    ).not.toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(await screen.findByText("服务器已接管恢复")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(
      await screen.findByText("备份已恢复并通过就绪检查"),
    ).toBeInTheDocument();
    expect(ArchiveRequest.requests).toHaveLength(2);
  });

  it("does not upload while another restore is active", async () => {
    let statusCalls = 0;
    let finishStatus!: (response: Response) => void;
    const terminal = new Promise<Response>((resolve) => {
      finishStatus = resolve;
    });
    installFetch(async () => {
      statusCalls += 1;
      if (statusCalls === 1) return failure("IMPORT_STAGING_INVALID", 409);
      if (statusCalls === 2) {
        return json({
          data: statusData("claimed"),
          meta: { request_id: "req_active_baseline" },
        });
      }
      return terminal;
    });
    await renderSettings();
    await createPlan(new File(["portable archive"], "backup.zip"));
    fireEvent.click(screen.getByRole("button", { name: "确认替换当前站点" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "确认并开始恢复" }),
    );
    expect(await screen.findByText(/另一项恢复正在进行/)).toBeInTheDocument();
    expect(ArchiveRequest.requests).toHaveLength(1);
    finishStatus(
      json({
        data: statusData("succeeded"),
        meta: { request_id: "req_active_terminal" },
      }),
    );
    expect(
      await screen.findByText("备份已恢复并通过就绪检查"),
    ).toBeInTheDocument();
    expect(ArchiveRequest.requests).toHaveLength(1);
  });

  it("does not overwrite a needs-attention recovery journal", async () => {
    let statusCalls = 0;
    installFetch(async () => {
      statusCalls += 1;
      return statusCalls === 1
        ? failure("IMPORT_STAGING_INVALID", 409)
        : json({
            data: statusData("needs_attention"),
            meta: { request_id: "req_needs_attention_baseline" },
          });
    });
    await renderSettings();
    await createPlan(new File(["portable archive"], "backup.zip"));
    fireEvent.click(screen.getByRole("button", { name: "确认替换当前站点" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "确认并开始恢复" }),
    );
    expect(await screen.findByText("恢复状态需要人工处理")).toBeInTheDocument();
    expect(screen.getByText(/本文件尚未上传/)).toBeInTheDocument();
    expect(ArchiveRequest.requests).toHaveLength(1);
    expect(
      screen.getByRole("button", { name: "确认替换当前站点" }),
    ).toBeDisabled();
  });

  it("queries durable status after a deterministic restore HTTP error", async () => {
    let statusCalls = 0;
    installFetch(async () => {
      statusCalls += 1;
      if (statusCalls <= 2) return failure("IMPORT_STAGING_INVALID", 409);
      return json({
        data: statusData("rolled_back"),
        meta: { request_id: "req_rolled_back_after_422" },
      });
    });
    await renderSettings();
    await createPlan(new File(["portable archive"], "backup.zip"));
    fireEvent.click(screen.getByRole("button", { name: "确认替换当前站点" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "确认并开始恢复" }),
    );
    await waitFor(() => expect(ArchiveRequest.requests).toHaveLength(2));
    ArchiveRequest.requests[1].respondFailure("DATA_INTEGRITY_FAILED", 422);
    expect(await screen.findByText("已自动恢复原站点")).toBeInTheDocument();
    expect(statusCalls).toBe(3);
    expect(ArchiveRequest.requests).toHaveLength(2);
  });

  it("bounds an unassociated baseline and leaves manual status recovery", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let statusCalls = 0;
    installFetch(async () => {
      statusCalls += 1;
      return json({
        data: statusData("succeeded", oldOperationID),
        meta: { request_id: `req_same_baseline_${statusCalls}` },
      });
    });
    await renderSettings();
    await createPlan(new File(["portable archive"], "backup.zip"));
    fireEvent.click(screen.getByRole("button", { name: "确认替换当前站点" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "确认并开始恢复" }),
    );
    await waitFor(() => expect(ArchiveRequest.requests).toHaveLength(2));
    ArchiveRequest.requests[1].respondFailure("DATA_INTEGRITY_FAILED", 422);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4_000);
    });
    expect(await screen.findByText(/自动关联已经停止/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "立即重试状态" })).toBeEnabled();
    expect(screen.getByLabelText(/^时区/)).toBeDisabled();
    expect(screen.getByRole("button", { name: "更换密钥" })).toBeDisabled();
    expect(statusCalls).toBe(7);
    expect(ArchiveRequest.requests).toHaveLength(2);
  });

  it("does not attribute a followed foreign operation terminal to this file", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let statusCalls = 0;
    installFetch(async () => {
      statusCalls += 1;
      if (statusCalls <= 2) return failure("IMPORT_STAGING_INVALID", 409);
      if (statusCalls === 3) {
        return json({
          data: statusData("claimed"),
          meta: { request_id: "req_expected_a" },
        });
      }
      if (statusCalls === 4) {
        return json({
          data: statusData("claimed", baselineOperationID),
          meta: { request_id: "req_changed_b_active" },
        });
      }
      return json({
        data: statusData("succeeded", baselineOperationID),
        meta: { request_id: "req_changed_b_terminal" },
      });
    });
    await renderSettings();
    await createPlan(new File(["portable archive"], "backup.zip"));
    fireEvent.click(screen.getByRole("button", { name: "确认替换当前站点" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "确认并开始恢复" }),
    );
    await waitFor(() => expect(ArchiveRequest.requests).toHaveLength(2));
    ArchiveRequest.requests[1].fail();
    expect(await screen.findByText("服务器已接管恢复")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(
      await screen.findByText(/服务器记录已经切换到另一项恢复/),
    ).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(
      await screen.findByText(/另一项恢复已经到达终态/),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("备份已恢复并通过就绪检查"),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "立即重试状态" })).toBeEnabled();
    expect(ArchiveRequest.requests).toHaveLength(2);
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
