import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OwnerApi } from "./owner";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const operationID = "import-20260811T013000000Z-0011223344556677";
const archiveSHA = "a".repeat(64);
const mergePlanID = "b".repeat(64);
const baseAuthoritySHA = "c".repeat(64);
const incomingAuthoritySHA = "d".repeat(64);
const mergeToken = "A".repeat(43);

function planData(archiveBytes: number) {
  const replacementDataBytes = 2_000;
  const mediaBytes = 1_000;
  const expandedBytes = 3_000;
  const stagingBytes = 5_000;
  const base = stagingBytes + replacementDataBytes * 2;
  const safety = 2 ** 31;
  const required = base + safety;
  return {
    dry_run: true,
    format_version: 1,
    archive_sha256: archiveSHA,
    counts: { entries: 1, comments: 2, media: 1 },
    time_range: {
      from: "2026-08-10T00:00:00Z",
      to: "2026-08-10T00:00:00.1Z",
    },
    media_bytes: mediaBytes,
    missing_alt: 1,
    disk: {
      archive_bytes: archiveBytes,
      expanded_bytes: expandedBytes,
      staging_bytes: stagingBytes,
      pre_import_backup_bytes: replacementDataBytes,
      rollback_reserve_bytes: replacementDataBytes,
      safety_margin_bytes: safety,
      required_additional_bytes: required,
      estimated_peak_footprint_bytes:
        archiveBytes + replacementDataBytes + required,
      available_bytes: required + 10_000,
      space_ok: true,
    },
    replacement: {
      counts: { entries: 8, comments: 13, media: 5 },
      media_bytes: 1_000,
      data_bytes: replacementDataBytes,
    },
    warnings: ["1 media usages have no image description"],
  };
}

function mergePlanData() {
  const importPlan = planData(9);
  return {
    plan: {
      version: 1,
      plan_id: mergePlanID,
      archive_sha256: archiveSHA,
      base_generation: "gen-old",
      base_authority_sha256: baseAuthoritySHA,
      incoming_authority_sha256: incomingAuthoritySHA,
      owner_id: "00000000000000000000000000000000",
      expires_at: "2999-08-11T02:00:00Z",
      summary: {
        unchanged: 0,
        incoming_only: 1,
        local_only: 0,
        conflict: 1,
      },
      items: [
        {
          kind: "entry",
          id: "post-conflict",
          classification: "conflict",
          changed_fields: ["content_md"],
          dependencies: [],
          selectable: true,
        },
        {
          kind: "comment",
          id: "comment-new",
          classification: "incoming_only",
          changed_fields: [],
          dependencies: [{ kind: "entry", id: "post-conflict" }],
          selectable: true,
        },
      ],
      site_fields: [],
      default_selection: {
        plan_id: mergePlanID,
        archive_sha256: archiveSHA,
        base_generation: "gen-old",
        base_authority_sha256: baseAuthoritySHA,
        items: [
          { kind: "entry", id: "post-conflict", action: "keep_local" },
          { kind: "comment", id: "comment-new", action: "add" },
        ],
        site_fields: [],
      },
    },
    review: {
      incoming_only: {
        total: 1,
        truncated: false,
        items: [
          {
            kind: "comment",
            id: "comment-new",
            classification: "incoming_only",
            changed_fields: [],
            incoming: {
              id: "comment-new",
              content_md: "incoming reply",
              entry_id: "post-conflict",
              author_kind: "guest",
              author_name: "Guest",
              created_at: "2026-08-11T00:00:00Z",
              media: [],
            },
          },
        ],
      },
      local_only: { total: 0, truncated: false, items: [] },
      conflicts: {
        total: 1,
        truncated: false,
        items: [
          {
            kind: "entry",
            id: "post-conflict",
            classification: "conflict",
            changed_fields: ["content_md"],
            local: {
              id: "post-conflict",
              content_md: "local post",
              created_at: "2026-08-10T00:00:00Z",
              updated_at: "2026-08-10T00:00:00Z",
              published_at: "2026-08-10T00:00:00Z",
              media: [],
            },
            incoming: {
              id: "post-conflict",
              content_md: "backup post",
              created_at: "2026-08-10T00:00:00Z",
              updated_at: "2026-08-11T00:00:00Z",
              published_at: "2026-08-10T00:00:00Z",
              media: [],
            },
          },
        ],
      },
      site_fields: [],
    },
    incoming_counts: { entries: 1, comments: 1, media: 0 },
    incoming_media_bytes: 0,
    disk: importPlan.disk,
    replacement: importPlan.replacement,
    merge_allowed: true,
    merge_blocked_reason: null,
    warnings: [],
    expires_at: "2999-08-11T02:00:00Z",
    staging_token: mergeToken,
  };
}

function statusData(
  phase: "claimed" | "succeeded" | "needs_attention" = "succeeded",
) {
  const base = {
    operation_id: operationID,
    phase,
    active_generation: phase === "succeeded" ? operationID : "gen-old",
    previous_generation: "gen-old",
    new_generation: operationID,
    backup_verified: phase !== "claimed",
    rollback_attempted: phase === "needs_attention",
    rollback_succeeded: false,
    started_at: "2026-08-11T01:30:00Z",
    updated_at:
      phase === "claimed" ? "2026-08-11T01:30:00.1Z" : "2026-08-11T01:30:01Z",
  };
  if (phase === "claimed") return base;
  return {
    ...base,
    backup_name: "pre-import-20260811.zip",
    ...(phase === "needs_attention"
      ? {
          error_code: "DATA_INTEGRITY_FAILED",
          completed_at: "2026-08-11T01:30:01.1Z",
        }
      : { completed_at: "2026-08-11T01:30:01Z" }),
  };
}

class FakeArchiveRequest extends EventTarget {
  static requests: FakeArchiveRequest[] = [];

  readonly upload = new EventTarget();
  readonly headers = new Map<string, string>();
  method = "";
  url = "";
  withCredentials = false;
  status = 0;
  responseText = "";
  contentType = "application/json";
  sent: Document | XMLHttpRequestBodyInit | null = null;
  aborted = false;

  constructor() {
    super();
    FakeArchiveRequest.requests.push(this);
  }

  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }

  setRequestHeader(name: string, value: string) {
    this.headers.set(name, value);
  }

  getResponseHeader(name: string) {
    return name.toLowerCase() === "content-type" ? this.contentType : null;
  }

  send(body: Document | XMLHttpRequestBodyInit | null) {
    this.sent = body;
  }

  abort() {
    this.aborted = true;
    this.dispatchEvent(new Event("abort"));
  }

  respondRaw(body: string, status = 200) {
    this.status = status;
    this.responseText = body;
    this.dispatchEvent(new Event("load"));
  }

  respond(data: unknown, status = 200) {
    this.respondRaw(
      JSON.stringify({ data, meta: { request_id: "req_import" } }),
      status,
    );
  }
}

describe("backup and restore owner API", () => {
  beforeEach(() => {
    FakeArchiveRequest.requests = [];
    vi.stubGlobal("XMLHttpRequest", FakeArchiveRequest);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("uploads the selected file for a zero-persistence exact plan", async () => {
    const file = new File(["zip bytes"], "hostile <name>.zip", {
      type: "text/plain",
    });
    const api = new OwnerApi(vi.fn() as typeof fetch);
    const progress = vi.fn();
    const pending = api.planImport("csrf-owner", file, progress);
    const request = FakeArchiveRequest.requests[0];

    expect(request.url).toBe("/api/v1/owner/import/plan");
    expect(request.withCredentials).toBe(true);
    expect(request.sent).toBe(file);
    expect(request.headers).toEqual(
      new Map([
        ["Accept", "application/json"],
        ["Content-Type", "application/zip"],
        ["X-CSRF-Token", "csrf-owner"],
      ]),
    );
    request.upload.dispatchEvent(
      new ProgressEvent("progress", {
        lengthComputable: true,
        loaded: 5,
        total: 10,
      }),
    );
    expect(progress).toHaveBeenCalledWith(50);
    request.respond(planData(file.size));
    await expect(pending).resolves.toMatchObject({
      archiveSHA256: archiveSHA,
      counts: { entries: 1, comments: 2, media: 1 },
      disk: { spaceOK: true },
    });
    expect(progress).toHaveBeenLastCalledWith(100);
  });

  it("plans and commits a strictly bound merge selection", async () => {
    const file = new File(["zip bytes"], "merge.zip", {
      type: "application/zip",
    });
    const fetcher = vi.fn().mockResolvedValue(
      json({
        data: { status: statusData("succeeded") },
        meta: { request_id: "req_merge" },
      }),
    );
    const api = new OwnerApi(fetcher as typeof fetch);
    const pending = api.planMergeImport("csrf-owner", file);
    const request = FakeArchiveRequest.requests[0];
    expect(request.url).toBe("/api/v1/owner/import/merge/plan");
    request.respond(mergePlanData(), 201);
    const inspection = await pending;
    expect(inspection).toMatchObject({
      stagingToken: mergeToken,
      plan: {
        plan_id: mergePlanID,
        summary: { incoming_only: 1, conflict: 1 },
      },
    });
    inspection.plan.default_selection.items[0].action = "keep_both";
    await expect(
      api.commitMergeImport(
        "csrf-owner",
        inspection.stagingToken,
        inspection.plan.default_selection,
      ),
    ).resolves.toMatchObject({ phase: "succeeded" });
    expect(fetcher).toHaveBeenCalledWith(
      "/api/v1/owner/import/merge/commit",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          staging_token: mergeToken,
          selection: inspection.plan.default_selection,
        }),
      }),
    );

    fetcher.mockResolvedValueOnce(
      json({
        data: { discarded: true },
        meta: { request_id: "req_discard" },
      }),
    );
    await expect(
      api.discardMergeImport("csrf-owner", inspection.stagingToken),
    ).resolves.toBeUndefined();
    expect(fetcher).toHaveBeenLastCalledWith(
      "/api/v1/owner/import/merge/discard",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ staging_token: mergeToken }),
      }),
    );
  });

  it("rejects a merge plan whose summary or default selection is inconsistent", async () => {
    const file = new File(["zip bytes"], "merge.zip");
    const api = new OwnerApi(vi.fn() as typeof fetch);

    const badSummary = api.planMergeImport("csrf", file);
    const summaryData = mergePlanData();
    summaryData.plan.summary.conflict = 2;
    FakeArchiveRequest.requests[0].respond(summaryData, 201);
    await expect(badSummary).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });

    const badSelection = api.planMergeImport("csrf", file);
    const selectionData = mergePlanData();
    selectionData.plan.default_selection.items[0].action = "skip";
    FakeArchiveRequest.requests[1].respond(selectionData, 201);
    await expect(badSelection).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("rejects duplicate JSON keys and inconsistent preview arithmetic", async () => {
    const file = new File(["zip bytes"], "backup.zip");
    const api = new OwnerApi(vi.fn() as typeof fetch);
    const duplicate = api.planImport("csrf", file);
    const data = JSON.stringify(planData(file.size));
    FakeArchiveRequest.requests[0].respondRaw(
      `{"data":${data},"data":${data},"meta":{"request_id":"req_import"}}`,
    );
    await expect(duplicate).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });

    const inconsistent = api.planImport("csrf", file);
    const broken = planData(file.size);
    broken.disk.required_additional_bytes += 1;
    FakeArchiveRequest.requests[1].respond(broken);
    await expect(inconsistent).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("fails closed on noncanonical plan success envelopes", async () => {
    const file = new File(["zip bytes"], "backup.zip");
    const api = new OwnerApi(vi.fn() as typeof fetch);
    const attempts: Array<(request: FakeArchiveRequest) => void> = [
      (request) => request.respond(planData(file.size), 201),
      (request) => {
        request.contentType = "text/html";
        request.respond(planData(file.size));
      },
      (request) => {
        const data = planData(file.size) as Record<string, unknown>;
        data.staging_token = "must-not-appear";
        request.respond(data);
      },
      (request) => {
        const data = planData(file.size);
        data.archive_sha256 = archiveSHA.toUpperCase();
        request.respond(data);
      },
      (request) => {
        const data = planData(file.size);
        data.counts.entries = Number.MAX_SAFE_INTEGER + 1;
        request.respond(data);
      },
      (request) => {
        const data = planData(file.size);
        data.time_range.to = "2026-08-10T00:00:00.100Z";
        request.respond(data);
      },
      (request) =>
        request.respondRaw(
          `${JSON.stringify({ data: planData(file.size), meta: { request_id: "req_import" } })} trailing`,
        ),
      (request) =>
        request.respondRaw(
          JSON.stringify({
            data: planData(file.size),
            meta: { request_id: "req_import", extra: true },
          }),
        ),
    ];

    for (const respond of attempts) {
      const pending = api.planImport("csrf", file);
      respond(FakeArchiveRequest.requests.at(-1)!);
      await expect(pending).rejects.toMatchObject({
        code: "API_RESPONSE_INVALID",
      });
    }
  });

  it("settles an invalid no-body XHR status instead of hanging", async () => {
    const file = new File(["zip bytes"], "backup.zip");
    const api = new OwnerApi(vi.fn() as typeof fetch);
    const pending = api.planImport("csrf", file);
    FakeArchiveRequest.requests[0].respondRaw("", 204);
    await expect(pending).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("orders canonical nanosecond instants and accepts completed needs-attention", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(
        json({ data: statusData("claimed"), meta: { request_id: "req_one" } }),
      )
      .mockResolvedValueOnce(
        json({
          data: statusData("needs_attention"),
          meta: { request_id: "req_two" },
        }),
      );
    const api = new OwnerApi(fetcher as typeof fetch);

    await expect(api.importStatus()).resolves.toMatchObject({
      phase: "claimed",
      updatedAt: "2026-08-11T01:30:00.1Z",
    });
    await expect(api.importStatus()).resolves.toMatchObject({
      phase: "needs_attention",
      completedAt: "2026-08-11T01:30:01.1Z",
      errorCode: "DATA_INTEGRITY_FAILED",
    });
  });

  it("rejects contradictory status relations and sends no restore secret on status", async () => {
    const invalidStatuses = [
      { ...statusData("claimed"), completed_at: "2026-08-11T01:30:01Z" },
      { ...statusData("succeeded"), active_generation: "gen-old" },
      { ...statusData("succeeded"), backup_name: "../backup.zip" },
      {
        ...statusData("needs_attention"),
        rollback_succeeded: true,
      },
      {
        ...statusData("needs_attention"),
        updated_at: "2026-08-11T01:30:01.2Z",
        completed_at: "2026-08-11T01:30:01.1Z",
      },
    ];
    const fetcher = vi.fn();
    for (const invalid of invalidStatuses) {
      fetcher.mockResolvedValueOnce(
        json({ data: invalid, meta: { request_id: "req_status" } }),
      );
    }
    const api = new OwnerApi(fetcher as typeof fetch);
    for (let index = 0; index < invalidStatuses.length; index += 1) {
      await expect(api.importStatus()).rejects.toMatchObject({
        code: "API_RESPONSE_INVALID",
      });
    }
    for (const [url, init] of fetcher.mock.calls) {
      expect(url).toBe("/api/v1/owner/import/status");
      expect(init.method).toBe("GET");
      expect(init.body).toBeUndefined();
      expect(new Headers(init.headers).has("X-CSRF-Token")).toBe(false);
      expect(String(url)).not.toContain("token");
    }
  });

  it("restores only the same file bound to the planned SHA and canonical success", async () => {
    const file = new File(["zip bytes"], "backup.zip");
    const api = new OwnerApi(vi.fn() as typeof fetch);
    const pending = api.restoreImport("csrf", file, archiveSHA);
    const request = FakeArchiveRequest.requests[0];
    expect(request.url).toBe("/api/v1/owner/import/restore");
    expect(request.sent).toBe(file);
    expect(request.headers.get("X-Moment-Archive-SHA256")).toBe(archiveSHA);
    request.respond({ status: statusData("succeeded") });
    await expect(pending).resolves.toMatchObject({
      phase: "succeeded",
      activeGeneration: operationID,
      backupVerified: true,
    });
  });

  it("rejects a non-success restore response and aborts exactly once", async () => {
    const file = new File(["zip bytes"], "backup.zip");
    const api = new OwnerApi(vi.fn() as typeof fetch);
    const nonSuccess = api.restoreImport("csrf", file, archiveSHA);
    FakeArchiveRequest.requests[0].respond({ status: statusData("claimed") });
    await expect(nonSuccess).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });

    const controller = new AbortController();
    const aborted = api.planImport("csrf", file, undefined, controller.signal);
    const request = FakeArchiveRequest.requests[1];
    controller.abort();
    await expect(aborted).rejects.toMatchObject({ code: "CANCELLED" });
    expect(request.aborted).toBe(true);
  });
});
