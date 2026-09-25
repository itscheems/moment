import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SiteIdentity } from "../../../api/public";
import { NavigationProvider } from "../../../app/navigation";
import { FeedbackProvider } from "../../../components/Feedback";

const uploadMedia = vi.fn();
const expireIfInvalidSession = vi.fn().mockReturnValue(false);

vi.mock("../../owner/OwnerProvider", () => ({
  useOwner: () => ({
    api: { uploadMedia },
    session: { csrfToken: "csrf", expiresAt: "", issuedAt: 0, version: "1" },
    state: "owner",
    account: null,
    limits: null,
    expireIfInvalidSession,
  }),
}));

const { ArticleEditor } = await import("./ArticleEditor");

const site: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "乔乔",
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

const DRAFT_ID = "01K2ABCDEF0123456789ABCDE0";

const body = {
  schema_version: 1,
  type: "doc",
  content: [
    { type: "paragraph", content: [{ type: "text", text: "上游的水是清的" }] },
  ],
};

function draftRow(overrides: Record<string, unknown> = {}) {
  return {
    id: DRAFT_ID,
    title: "一条河流的十年",
    body,
    body_schema_version: 1,
    cover_media_id: null,
    published_entry_id: null,
    created_at: "2026-09-25T01:00:00Z",
    updated_at: "2026-09-25T02:00:00.000Z",
    ...overrides,
  };
}

function success(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ data, meta: { request_id: "r" } }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function failure(
  code: string,
  status: number,
  details: unknown = {},
): Response {
  return new Response(
    JSON.stringify({
      error: { code, message: "no", details, request_id: "r" },
    }),
    { status, headers: { "Content-Type": "application/json" } },
  );
}

interface Call {
  method: string;
  url: string;
  body: unknown;
}

let calls: Call[] = [];
let onPut: (() => Response) | null = null;
let onPost: (() => Response) | null = null;

function stubFetch() {
  const fetcher = vi.fn(
    (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({
        method,
        url,
        body:
          typeof init?.body === "string"
            ? (JSON.parse(init.body) as unknown)
            : null,
      });
      if (url === "/api/v1/owner/articles/drafts" && method === "GET") {
        return Promise.resolve(success([draftRow()]));
      }
      if (url === "/api/v1/owner/articles/drafts" && method === "POST") {
        return Promise.resolve(onPost ? onPost() : success(draftRow(), 201));
      }
      if (method === "PUT") {
        return Promise.resolve(
          onPut
            ? onPut()
            : success(draftRow({ updated_at: "2026-09-25T02:05:00.000Z" })),
        );
      }
      return Promise.resolve(success(draftRow()));
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

function renderEditor(draftId: string | null) {
  return render(
    <FeedbackProvider>
      <NavigationProvider>
        <ArticleEditor draftId={draftId} site={site} />
      </NavigationProvider>
    </FeedbackProvider>,
  );
}

function titleField(): HTMLTextAreaElement {
  const field = screen.getByLabelText("文章标题");
  if (!(field instanceof HTMLTextAreaElement))
    throw new Error("no title field");
  return field;
}

async function typeTitle(value: string) {
  const field = titleField();
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      "value",
    )?.set;
    setter?.call(field, value);
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// A blank page nobody has typed on has no unsaved change to report. Saying it
// does trains the owner to ignore the one line that tells them whether their
// words are safe.
it("opens a new article idle rather than claiming unsaved changes", async () => {
  stubFetch();
  renderEditor(null);
  await advance(0);
  expect(screen.getByRole("status")).toHaveTextContent("草稿");
  expect(screen.getByRole("status")).not.toHaveTextContent("有改动未保存");
  await advance(20_000);
  expect(screen.getByRole("status")).not.toHaveTextContent("有改动未保存");
  expect(calls.filter((call) => call.method !== "GET")).toEqual([]);

  // Reading the preview and coming back is not editing either.
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "预览" }));
  });
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "继续编辑" }));
  });
  await advance(20_000);
  expect(screen.getByRole("status")).not.toHaveTextContent("有改动未保存");
  expect(calls.filter((call) => call.method !== "GET")).toEqual([]);
});

/**
 * Let the clock and the promises catch up.
 *
 * Testing Library's `waitFor` polls on a real interval, and every timer in
 * this file is a fake one -- so the poll would never run and every wait would
 * be a five second timeout. Advancing the fake clock inside `act` does the
 * same job deterministically: what is asserted afterwards is the state at a
 * known instant rather than whatever had arrived by the time a poll looked.
 */
async function advance(ms = 0) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

const putCalls = () => calls.filter((call) => call.method === "PUT");

beforeEach(() => {
  calls = [];
  onPut = null;
  onPost = null;
  // floating-ui, which the selection menu positions itself with, asks for one.
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  vi.useFakeTimers({
    toFake: [
      "setTimeout",
      "clearTimeout",
      "setInterval",
      "clearInterval",
      "Date",
    ],
    now: new Date("2026-09-25T02:01:00.000Z"),
  });
  stubFetch();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("opening a draft", () => {
  it("shows the title and the body the server is holding", async () => {
    renderEditor(DRAFT_ID);
    await advance();
    expect(titleField().value).toBe("一条河流的十年");
    expect(screen.getByText("上游的水是清的")).toBeInTheDocument();
  });

  it("says when the copy on the server was written", async () => {
    renderEditor(DRAFT_ID);
    await advance();
    expect(screen.getByRole("status")).toHaveTextContent("草稿 · 1分钟前保存");
  });

  it("does not save the draft it has only just read", async () => {
    renderEditor(DRAFT_ID);
    await advance();
    await advance(20_000);
    expect(putCalls()).toHaveLength(0);
  });

  it("lists the drafts the server keeps, not this browser's", async () => {
    renderEditor(DRAFT_ID);
    await advance();
    // The row that opens it, not the one that deletes it: the delete button
    // names the draft too.
    expect(
      screen.getByRole("button", { name: /^一条河流的十年/ }),
    ).toBeInTheDocument();
    expect(
      calls.some(
        (call) =>
          call.method === "GET" && call.url === "/api/v1/owner/articles/drafts",
      ),
    ).toBe(true);
  });
});

describe("saving while the owner writes", () => {
  it("waits for the typing to stop instead of saving per keystroke", async () => {
    renderEditor(DRAFT_ID);
    await advance();

    await typeTitle("一条河流的十");
    await advance(500);
    await typeTitle("一条河流的");
    await advance(1_500);
    expect(putCalls()).toHaveLength(0);

    await advance(700);
    expect(putCalls()).toHaveLength(1);
    expect(putCalls()[0].body).toMatchObject({
      title: "一条河流的",
      expected_updated_at: "2026-09-25T02:00:00.000Z",
    });
  });

  it("saves anyway once the work has been unsaved for long enough", async () => {
    renderEditor(DRAFT_ID);
    await advance();

    // Typing that never goes idle: without a ceiling nothing would ever save.
    for (let step = 0; step < 10; step += 1) {
      await typeTitle(`一条河流的十年 ${step}`);
      await advance(1_500);
      if (putCalls().length > 0) break;
    }
    expect(putCalls()).toHaveLength(1);
  });

  it("carries the revision it read, so the server can refuse a stale save", async () => {
    renderEditor(DRAFT_ID);
    await advance();
    await typeTitle("改过的标题");
    await advance(2_100);
    expect(putCalls()).toHaveLength(1);

    // The answer carried a newer revision; the next save has to quote that one.
    await typeTitle("再改一次");
    await advance(2_100);
    expect(putCalls()).toHaveLength(2);
    expect(putCalls()[1].body).toMatchObject({
      expected_updated_at: "2026-09-25T02:05:00.000Z",
    });
  });
});

describe("when the same draft was written from somewhere else", () => {
  it("stops, says so, and offers the owner both ways out", async () => {
    onPut = () =>
      failure("ARTICLE_DRAFT_STALE", 409, {
        id: DRAFT_ID,
        updated_at: "2026-09-25T02:09:00.000Z",
      });
    renderEditor(DRAFT_ID);
    await advance();

    await typeTitle("我这边的标题");
    await advance(2_100);
    expect(screen.getByRole("alert")).toHaveTextContent("这份草稿在别处改过了");
    expect(
      screen.getByRole("button", { name: "用这里的版本覆盖" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "读回服务器上的版本" }),
    ).toBeInTheDocument();
  });

  it("never quietly writes over the other copy", async () => {
    onPut = () =>
      failure("ARTICLE_DRAFT_STALE", 409, { id: DRAFT_ID, updated_at: "x" });
    renderEditor(DRAFT_ID);
    await advance();

    await typeTitle("我这边的标题");
    await advance(2_100);
    expect(putCalls()).toHaveLength(1);

    // Typing continues; the autosave does not resume until the owner answers.
    await typeTitle("还在写");
    await advance(30_000);
    expect(putCalls()).toHaveLength(1);
    expect(screen.getByRole("status")).toHaveTextContent("别处改过了");
  });
});

describe("a draft that has never been saved", () => {
  it("creates one on the first save and puts it in the address", async () => {
    renderEditor(null);
    await advance();
    await typeTitle("新的一篇");
    await advance(2_100);
    expect(calls.some((call) => call.method === "POST")).toBe(true);
    expect(window.location.pathname).toBe(`/compose/article/${DRAFT_ID}`);
  });

  it("says what the limit is when the site is already holding it", async () => {
    onPost = () => failure("ARTICLE_DRAFT_LIMIT", 422, { limit: 50 });
    renderEditor(null);
    await advance();
    await typeTitle("第五十一篇");
    await advance(2_100);
    expect(screen.getByRole("status")).toHaveTextContent(
      "草稿已经有 50 份，删掉一些再新建。",
    );
  });
});

describe("what the toolbar writes into the document", () => {
  async function pressAndSave(label: string) {
    renderEditor(DRAFT_ID);
    await advance();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: label }));
    });
    await advance(2_100);
    expect(putCalls()).toHaveLength(1);
    return putCalls()[0].body;
  }

  it("writes a table with no column widths, which the server would refuse", async () => {
    const saved = await pressAndSave("表格");
    const serialised = JSON.stringify(saved);
    expect(serialised).toContain('"tableHeader"');
    // The whole reason the table extension is rebuilt: `resizable: false`
    // hides the drag handles and leaves `colwidth` in the schema.
    expect(serialised).not.toContain("colwidth");
    expect(serialised).not.toContain('"align"');
  });

  it("writes a list with no list type, which the server would refuse", async () => {
    const saved = await pressAndSave("有序列表");
    const serialised = JSON.stringify(saved);
    expect(serialised).toContain('"orderedList"');
    // Tiptap 3 carries `type` for `<ol type="a">`; the allowlist has no such
    // attribute, and a list starting at one carries no attributes at all.
    expect(serialised).toContain('{"type":"orderedList","content"');
  });

  it("keeps the one paragraph it started with", async () => {
    const saved = await pressAndSave("引用");
    expect(JSON.stringify(saved)).toContain("上游的水是清的");
  });
});
