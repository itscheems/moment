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

import i18n from "../../i18n";
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
      display_name: "Jojo",
      title: "Jojo’s Moment",
      bio: "Notes from daily life",
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
          local_value: "Current title",
          incoming_value: "Archive title",
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
              content_md: "Entry added by the archive",
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
              content_md: "Entry published after export",
              created_at: "2026-08-11T08:00:00Z",
              published_at: "2026-08-11T08:00:00Z",
              location: {
                name: "City Plaza",
                display_name: "1600 Market Street",
                country_code: "CN",
                country: "Example Country",
                region: "Metro City",
                city: "Metro City",
                district: "Riverside",
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
            incoming: makeReviewUser({ display_name: "Morgan (archive)" }),
          },
          {
            kind: "entry",
            id: "post-conflict",
            classification: "conflict",
            changed_fields: ["content"],
            local: {
              id: "post-conflict",
              content_md: "Locally updated entry",
              created_at: "2026-08-09T08:00:00Z",
              published_at: "2026-08-09T08:00:00Z",
              media: [],
            },
            incoming: {
              id: "post-conflict",
              content_md: "Earlier archived entry",
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
          local_value: "Current title",
          incoming_value: "Archive title",
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
      preferences: { ui_locale: null },
      expires_at: "2999-08-11T01:00:00.000Z",
      csrf_token: "csrf-from-cookie",
      account: {
        user_id: "00000000000000000000000000000000",
        username: "owner",
        display_name: "Jojo",
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
      if (url === "/api/v1/account/reauthentication")
        return json({
          data: {
            confirmed: true,
            expires_at: new Date(Date.now() + 300000).toISOString(),
          },
          meta: { request_id: "req_confirmation" },
        });
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
  await screen.findByRole("heading", { name: "Restore from a backup" });
  if (waitForPicker) {
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Choose backup file" }),
      ).toBeEnabled(),
    );
  }
}

async function createReview(file: File) {
  fireEvent.change(screen.getByLabelText("Choose a Moment backup ZIP"), {
    target: { files: [file] },
  });
  fireEvent.click(screen.getByRole("button", { name: "Inspect backup" }));
  const request = ArchiveRequest.requests.at(-1)!;
  expect(request.url).toBe("/api/v1/backups/import/review");
  request.respond(mergePlanData(), 201);
  await screen.findByRole("heading", { name: "Review and merge" });
}

describe("owner backup and restore", () => {
  beforeEach(async () => {
    window.history.replaceState({}, "", "/settings/backups/restore");
    window.sessionStorage.clear();
    window.localStorage.clear();
    window.localStorage.setItem("moment.ui-locale", "en");
    await i18n.changeLanguage("en");
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
    fireEvent.change(screen.getByLabelText("Choose a Moment backup ZIP"), {
      target: { files: [file] },
    });
    fireEvent.click(screen.getByRole("button", { name: "Inspect backup" }));
    const request = ArchiveRequest.requests.at(-1)!;
    expect(request.url).toBe("/api/v1/backups/import/review");
    request.respond(mergePlanData(), 201);

    await screen.findByRole("heading", { name: "Review and merge" });
    expect(
      screen.getByText("Entry published after export"),
    ).toBeInTheDocument();
    expect(screen.getByText("Locally updated entry")).toBeInTheDocument();
    expect(screen.getByText("Earlier archived entry")).toBeInTheDocument();

    // Accounts are reported, never chosen. They get their own section with the
    // reason stated, rather than a row of controls the server would refuse or
    // -- worse -- a silent omission of the people a restore brings with it.
    const accounts = screen.getByRole("region", {
      name: "Accounts and login methods",
    });
    // Both sides of the conflict are shown, so the handle appears twice.
    expect(within(accounts).getAllByText("@linlin")).toHaveLength(2);
    expect(within(accounts).getAllByText("Member").length).toBeGreaterThan(0);
    expect(within(accounts).getAllByText("Active").length).toBeGreaterThan(0);
    expect(within(accounts).getByText("Morgan (archive)")).toBeInTheDocument();
    expect(within(accounts).getByText("GitHub")).toBeInTheDocument();
    expect(
      within(accounts).getByText("linlin@example.com"),
    ).toBeInTheDocument();
    expect(
      within(accounts).getByText("Both · Current site wins"),
    ).toBeInTheDocument();
    expect(
      within(accounts).getByText("Backup only · Will be added"),
    ).toBeInTheDocument();
    expect(
      within(accounts).getByText(
        /Keeping both would split that person into two accounts/,
      ),
    ).toBeInTheDocument();
    expect(within(accounts).queryAllByRole("radio")).toHaveLength(0);
    expect(within(accounts).queryAllByRole("radiogroup")).toHaveLength(0);
    // And they stay out of the lists that do carry controls.
    expect(
      screen.queryByRole("radiogroup", { name: /Account/ }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("radio", { name: "Keep both" }));
    fireEvent.click(screen.getByRole("radio", { name: "Use backup value" }));
    expect(
      screen.queryByRole("combobox", { name: /Conflict handling/ }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Submit merge" }));
    expect(
      await screen.findByRole("heading", {
        name: "Merge this backup with these choices?",
      }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Confirm and merge" }));

    expect(
      await screen.findByText("Backup merged and readiness verified"),
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
    fireEvent.change(screen.getByLabelText("Choose a Moment backup ZIP"), {
      target: {
        files: [new File(["merge"], "merge.zip", { type: "application/zip" })],
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "Inspect backup" }));
    ArchiveRequest.requests.at(-1)!.respond(mergePlanData(), 201);

    await screen.findByRole("heading", { name: "Review and merge" });

    // The backup's incoming-only bucket holds two rows and one of them is a
    // credential, which this section does not render.
    const incoming = screen.getByRole("heading", { name: "Added by backup" });
    expect(incoming.nextElementSibling).toHaveTextContent(/^1 items$/);
    expect(screen.getByText("Entry added by the archive")).toBeInTheDocument();

    // The current-only bucket carries no accounts, so nothing moved.
    const localOnly = screen.getByRole("heading", {
      name: "Local only · Will be kept",
    });
    expect(localOnly.nextElementSibling).toHaveTextContent(/^1 items$/);
  });

  it("says which refusal blocked the merge", async () => {
    installFetch(async () => failure("IMPORT_STAGING_INVALID", 409));
    await renderSettings();
    fireEvent.change(screen.getByLabelText("Choose a Moment backup ZIP"), {
      target: {
        files: [new File(["merge"], "merge.zip", { type: "application/zip" })],
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "Inspect backup" }));
    ArchiveRequest.requests.at(-1)!.respond(
      {
        ...mergePlanData(),
        merge_allowed: false,
        merge_blocked_reason: "USERNAME_TAKEN",
      },
      201,
    );

    await screen.findByRole("heading", { name: "Review replacement restore" });
    expect(
      screen.getByText(
        /A backup account and a different local account share a username/,
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Safe merge" })).toBeDisabled();
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
    await screen.findByRole("heading", { name: "Restore from a backup" });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Choose backup file" }),
      ).toBeEnabled(),
    );

    const first = new File(["merge-one"], "first.zip", {
      type: "application/zip",
    });
    fireEvent.change(screen.getByLabelText("Choose a Moment backup ZIP"), {
      target: { files: [first] },
    });
    fireEvent.click(screen.getByRole("button", { name: "Inspect backup" }));
    ArchiveRequest.requests.at(-1)!.respond(mergePlanData(), 201);
    await screen.findByRole("heading", { name: "Review and merge" });

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
    await screen.findByRole("heading", { name: "Restore from a backup" });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Choose backup file" }),
      ).toBeEnabled(),
    );
    const second = new File(["merge-two"], "second.zip", {
      type: "application/zip",
    });
    fireEvent.change(screen.getByLabelText("Choose a Moment backup ZIP"), {
      target: { files: [second] },
    });
    fireEvent.click(screen.getByRole("button", { name: "Inspect backup" }));
    const nextRequest = ArchiveRequest.requests.at(-1)!;
    expect(nextRequest.url).toBe("/api/v1/backups/import/review");
    nextRequest.respond(mergePlanData(), 201);
    expect(
      await screen.findByRole("heading", { name: "Review and merge" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(
        "Another restore or maintenance operation is running. Check its status before inspecting again.",
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
    fireEvent.change(screen.getByLabelText("Choose a Moment backup ZIP"), {
      target: { files: [file] },
    });
    fireEvent.click(screen.getByRole("button", { name: "Inspect backup" }));
    ArchiveRequest.requests.at(-1)!.respond(mergePlanData(), 201);
    await screen.findByRole("heading", { name: "Review and merge" });
    fireEvent.click(screen.getByRole("button", { name: "Submit merge" }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm and merge" }));

    expect(
      await screen.findByText(
        "The merge plan expired or the site changed. Check conflicts again.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Review and merge" }),
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

  it("keeps the restore page focused on status and file entry", async () => {
    installFetch();
    await renderSettings();

    expect(
      screen.getByRole("heading", { name: "No restore is running" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Download site backup" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Choose backup file" }),
    ).toBeEnabled();
    expect(
      screen.queryByRole("heading", { name: "Restore impact review" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Staged rebuild")).not.toBeInTheDocument();
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
    await screen.findByRole("heading", { name: "Restore from a backup" });
    await waitFor(() => expect(resolveInitial).toBeTypeOf("function"));
    expect(
      screen.getByRole("button", { name: "Choose backup file" }),
    ).toBeDisabled();
    await act(async () => {
      resolveInitial(failure("IMPORT_STAGING_INVALID", 409));
    });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Choose backup file" }),
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

    fireEvent.click(screen.getByRole("button", { name: "Go back" }));
    fireEvent.click(
      await screen.findByRole("link", { name: /^Download a site backup/ }),
    );
    const button = await screen.findByRole("button", {
      name: "Download site backup",
    });
    fireEvent.click(button);
    fireEvent.click(button);
    await waitFor(() =>
      expect(clicks).toEqual([
        { href: "/api/v1/backups/export", download: "moment-export.zip" },
      ]),
    );
    expect(await screen.findByText(/Download requested/)).toBeInTheDocument();
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
    await screen.findByRole("heading", { name: "Restore from a backup" });
    await screen.findByRole("button", { name: "Retry status now" });
    expect(
      screen.getByRole("button", { name: "Choose backup file" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Choose backup file" }),
    ).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Go back" }));
    fireEvent.click(
      await screen.findByRole("link", { name: /^Download a site backup/ }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Download site backup" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Go back" }));
    fireEvent.click(
      await screen.findByRole("link", { name: /^Restore from a backup/ }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Retry status now" }),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Choose backup file" }),
      ).toBeEnabled(),
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    fireEvent.click(screen.getByRole("button", { name: "Go back" }));
    fireEvent.click(
      await screen.findByRole("link", { name: /^Download a site backup/ }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Download site backup" }),
    );
    await waitFor(() =>
      expect(clicks).toEqual([
        "/api/v1/backups/export",
        "/api/v1/backups/export",
      ]),
    );
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

    fireEvent.click(screen.getByRole("button", { name: "Replace everything" }));
    expect(
      await screen.findByRole("heading", {
        name: "Review replacement restore",
      }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("Local only · Will be removed")).toHaveLength(2);
    expect(
      screen.getByText("Entry published after export"),
    ).toBeInTheDocument();
    expect(screen.getByTitle("1600 Market Street")).toHaveTextContent(
      "City Plaza · Riverside",
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Confirm replacement" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Replace the entire site?",
    });
    expect(
      within(dialog).getByText("1 items will be removed"),
    ).toBeInTheDocument();
    // A replacement takes the account graph with it, so the confirmation has
    // to say how many accounts arrive and how many stop existing. Reading
    // that number off the archive is the whole reason the server sends it.
    expect(
      within(dialog).getByText("2 Moment · 0 Comments · 3 Account"),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("2 will be replaced")).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", {
        name: "Close replacement confirmation",
      }),
    ).toBeInTheDocument();
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.top).toBe("");

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Confirm replacement" }),
    );
    expect(
      await screen.findByText("Backup restored and readiness verified"),
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
    fireEvent.click(screen.getByRole("button", { name: "Replace everything" }));
    fireEvent.click(
      screen.getByRole("button", { name: "Confirm replacement" }),
    );
    fireEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "Confirm replacement",
      }),
    );

    expect(
      await screen.findByText(
        "The site changed since your review. Restore was refused to protect new content. Inspect the backup again.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Review replacement restore" }),
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
    fireEvent.change(screen.getByLabelText("Choose a Moment backup ZIP"), {
      target: { files: [new File(["first"], "first.zip")] },
    });
    fireEvent.click(screen.getByRole("button", { name: "Inspect backup" }));
    const stale = ArchiveRequest.requests[0];

    fireEvent.change(screen.getByLabelText("Choose a Moment backup ZIP"), {
      target: { files: [new File(["second"], "second.zip")] },
    });
    expect(stale.aborted).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Inspect backup" }));
    stale.respond(mergePlanData(), 201);
    ArchiveRequest.requests[1].respond(mergePlanData(), 201);
    expect(await screen.findByText(/second\.zip/)).toBeInTheDocument();
    expect(screen.queryByText(/first\.zip/)).not.toBeInTheDocument();
  });

  it.each([
    ["failed", "Restore stopped before switching data"],
    ["rolled_back", "Original site restored automatically"],
    ["needs_attention", "Restore requires manual action"],
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
        screen.getByRole("button", { name: "Choose backup file" }),
      ).toBeDisabled();
    }
  });
});
