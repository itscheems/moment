import i18n, { t } from "../../../i18n";
import { articleDraftIdFromPath } from "../../../app/routes";
import "@testing-library/jest-dom/vitest";
import { ThemeProvider } from "../../../theme/ThemeProvider";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SiteIdentity } from "../../../api/public";
import { NavigationProvider, useNavigation } from "../../../app/navigation";
import { FeedbackProvider } from "../../../components/Feedback";
import { applyReadingColor } from "../reader/readingAnnotations";
import type { Editor } from "@tiptap/core";

const uploadMedia = vi.fn();
const expireIfInvalidSession = vi.fn().mockReturnValue(false);

// Exercise the editor's upload/save boundary independently of bitmap decoding.
vi.mock("./components/CoverCropDialog", () => ({
  CoverCropDialog: ({
    file,
    busy,
    onCancel,
    onApply,
  }: {
    file: File;
    busy?: boolean;
    onCancel: () => void;
    onApply: (file: File) => void;
  }) => (
    <div role="dialog" aria-label="编辑封面图">
      <button onClick={onCancel}>取消裁切</button>
      <button disabled={busy} onClick={() => onApply(file)}>
        应用裁切
      </button>
    </div>
  ),
}));

vi.mock("../../auth/SessionProvider", () => ({
  useSession: () => ({
    api: { uploadMedia },
    session: { csrfToken: "csrf", expiresAt: "", issuedAt: 0, version: "1" },
    state: "signed-in",
    account: null,
    limits: null,
    expireIfInvalidSession,
  }),
}));

vi.mock("../../../api/media", () => ({ mediaApi: { uploadMedia } }));

const { ArticleEditor } = await import("./ArticleEditor");

const site: SiteIdentity = {
  profileUserId: "00000000000000000000000000000000",
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
let onPut: (() => Response | Promise<Response>) | null = null;
let onPost: (() => Response | Promise<Response>) | null = null;
let onRead: (() => Response | Promise<Response>) | null = null;

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
      if (url.endsWith("/publish") || url.startsWith("/api/v1/posts/"))
        return Promise.resolve(success(publishedRow()));
      if (url === "/api/v1/articles/drafts" && method === "GET") {
        return Promise.resolve(success([draftRow()]));
      }
      if (url === "/api/v1/articles/drafts" && method === "POST") {
        return Promise.resolve(onPost ? onPost() : success(draftRow(), 201));
      }
      if (method === "PUT") {
        return Promise.resolve(
          onPut
            ? onPut()
            : success(draftRow({ updated_at: "2026-09-25T02:05:00.000Z" })),
        );
      }
      return Promise.resolve(onRead ? onRead() : success(draftRow()));
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

function renderEditor(draftId: string | null) {
  return render(
    <FeedbackProvider>
      <ThemeProvider>
        <NavigationProvider>
          <ArticleEditor draftId={draftId} site={site} />
        </NavigationProvider>
      </ThemeProvider>
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

it("disables publishing until both title and meaningful body are present", async () => {
  renderEditor(null);
  await advance();
  const publishButton = screen.getByRole("button", {
    name: "发布",
  });
  expect(publishButton).toBeDisabled();
  await typeTitle("新的文章");
  expect(publishButton).toBeDisabled();
  const editable = document.querySelector<HTMLElement & { editor: Editor }>(
    '.tiptap.ProseMirror[contenteditable="true"]',
  );
  if (!editable) throw new Error("missing editor");
  await act(async () => {
    editable.editor.commands.insertContent("   ");
  });
  expect(publishButton).toBeDisabled();
  await act(async () => {
    editable.editor.commands.insertContent("开始记录。");
  });
  expect(publishButton).toBeEnabled();
  await typeTitle("");
  expect(publishButton).toBeDisabled();
});

it("keeps preview annotations out of draft content, save status and persistence", async () => {
  await import("../reader/ArticleReadingContent");
  stubFetch();
  renderEditor(DRAFT_ID);
  await advance(0);
  const status = screen.getByRole("status").textContent;
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: "预览" })),
  );
  await advance(400);
  const reader = Array.from(
    document.querySelectorAll<HTMLElement & { editor?: Editor }>(
      "[data-content-renderer]",
    ),
  ).find((element) => !element.closest("[hidden]") && element.editor)?.editor;
  expect(reader).toBeDefined();
  if (!reader) throw new Error("Preview editor did not mount");
  const before = reader.getJSON();
  const stored = vi.spyOn(Storage.prototype, "setItem");
  await act(async () => {
    applyReadingColor(reader, { from: 1, to: 5 }, "highlight", "blue");
    applyReadingColor(reader, { from: 2, to: 4 }, "text", "red");
    reader.commands.setTextSelection({ from: 1, to: 5 });
  });
  expect(screen.getByRole("button", { name: "分享摘录" })).toBeInTheDocument();
  await advance(20_000);
  expect(reader.getJSON()).toEqual(before);
  expect(screen.getByRole("status")).toHaveTextContent(status!);
  expect(calls.filter((call) => call.method !== "GET")).toEqual([]);
  expect(stored).not.toHaveBeenCalled();
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: "继续编辑" })),
  );
  await advance(20_000);
  expect(document.querySelector("[data-reading-annotation]")).toBeNull();
  expect(screen.queryByRole("button", { name: "分享摘录" })).toBeNull();
  expect(screen.getByRole("status")).toHaveTextContent(status!);
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

function pressSave(
  target: HTMLElement = document.body,
  modifier: "metaKey" | "ctrlKey" = "metaKey",
) {
  const key = { key: "s", code: "KeyS", [modifier]: true };
  const allowed = fireEvent.keyDown(target, key);
  fireEvent.keyUp(target, key);
  return allowed;
}

beforeEach(() => {
  uploadMedia.mockReset();
  calls = [];
  onPut = null;
  onPost = null;
  onRead = null;
  // floating-ui, which the selection menu positions itself with, asks for one.
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  vi.stubGlobal("PointerEvent", MouseEvent);
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

async function selectCover() {
  const input = document.querySelector('input[type="file"]');
  if (!input) throw new Error("missing cover input");
  fireEvent.change(input, {
    target: {
      files: [new File(["cover"], "cover.png", { type: "image/png" })],
    },
  });
  await advance();
}

describe("cover crop and upload lifecycle", () => {
  it("cancels local selection without uploading or changing the saved cover", async () => {
    onRead = () =>
      success(draftRow({ cover_media_id: "01K2ABCDEF0123456789ABCDE1" }));
    renderEditor(DRAFT_ID);
    await advance();
    await selectCover();
    expect(uploadMedia).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "取消裁切" }));
    await advance(20000);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(uploadMedia).not.toHaveBeenCalled();
    expect(putCalls()).toEqual([]);
    expect(
      screen.getByRole("button", { name: "更换封面图" }),
    ).toBeInTheDocument();
  });

  it("retains the previous cover and crop after failure, then saves the retried upload", async () => {
    onRead = () =>
      success(draftRow({ cover_media_id: "01K2ABCDEF0123456789ABCDE1" }));
    uploadMedia
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce({ id: "01K2ABCDEF0123456789ABCDE2" });
    renderEditor(DRAFT_ID);
    await advance();
    await selectCover();
    fireEvent.click(screen.getByRole("button", { name: "应用裁切" }));
    await advance();
    expect(
      screen.getByRole("dialog", { name: "编辑封面图" }),
    ).toBeInTheDocument();
    expect(document.querySelector("figure img")).toHaveAttribute(
      "src",
      expect.stringContaining("01K2ABCDEF0123456789ABCDE1"),
    );
    expect(putCalls()).toEqual([]);
    fireEvent.click(screen.getByRole("button", { name: "应用裁切" }));
    await advance();
    await advance(2000);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(putCalls().at(-1)?.body).toMatchObject({
      cover_media_id: "01K2ABCDEF0123456789ABCDE2",
    });
    expect(uploadMedia.mock.calls[0][3]).toBeInstanceOf(AbortSignal);
  });

  it("aborts a cancelled upload and ignores its late result", async () => {
    let resolveUpload!: (value: { id: string }) => void;
    uploadMedia.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveUpload = resolve;
        }),
    );
    renderEditor(DRAFT_ID);
    await advance();
    await selectCover();
    fireEvent.click(screen.getByRole("button", { name: "应用裁切" }));
    await advance();
    const signal = uploadMedia.mock.calls[0][3] as AbortSignal;
    fireEvent.click(screen.getByRole("button", { name: "取消裁切" }));
    expect(signal.aborted).toBe(true);
    await act(async () => resolveUpload({ id: "01K2ABCDEF0123456789ABCDE2" }));
    await advance(20000);
    expect(putCalls()).toEqual([]);
    expect(
      screen.getByRole("button", { name: "添加封面图" }),
    ).toBeInTheDocument();
  });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

describe("opening a draft", () => {
  it("previews with the publication renderer and returns to the retained editor without saving", async () => {
    renderEditor(DRAFT_ID);
    await advance();
    const textbox = screen.getByRole("textbox", { name: "文章正文" });
    fireEvent.click(screen.getByRole("button", { name: "预览" }));
    expect(screen.queryByRole("textbox", { name: "文章正文" })).toBeNull();
    const reading = document.querySelector(
      "[data-article-sheet] [data-content-renderer]",
    );
    expect(reading).toHaveTextContent("上游的水是清的");
    expect(reading?.querySelector("[contenteditable]")).toBeNull();
    await advance(20_000);
    expect(document.querySelector("[data-content-renderer]")).toBe(reading);
    fireEvent.click(screen.getByRole("button", { name: "继续编辑" }));
    expect(screen.getByRole("textbox", { name: "文章正文" })).toBe(textbox);
    expect(putCalls()).toHaveLength(0);
  });
  it("uses only reading headings for the inline TOC while retaining the editor", async () => {
    onRead = () =>
      success(
        draftRow({
          body: {
            ...body,
            content: [
              {
                type: "heading",
                attrs: { level: 1, id: "chapter", "data-toc-id": "chapter" },
                content: [{ type: "text", text: "章节" }],
              },
              { type: "tocNode" },
            ],
          },
        }),
      );
    renderEditor(DRAFT_ID);
    await advance();
    fireEvent.click(screen.getByRole("button", { name: "预览" }));
    await advance(1);
    expect(
      document.querySelectorAll(
        "[data-content-renderer] .tiptap-table-of-contents-item",
      ),
    ).toHaveLength(1);
    expect(
      document.querySelectorAll(
        "[data-content-renderer] .tiptap-table-of-contents-item",
      )[0],
    ).toHaveAttribute("href", "#chapter");
    expect(
      fireEvent.keyDown(document.body, {
        key: "f",
        code: "KeyF",
        metaKey: true,
      }),
    ).toBe(false);
    fireEvent.change(screen.getByRole("textbox", { name: "查找" }), {
      target: { value: "章节" },
    });
    expect(screen.queryByRole("textbox", { name: "替换" })).toBeNull();
    expect(screen.getByText("区分大小写")).toBeInTheDocument();
    await advance(300);
    expect(screen.getByText("1 / 1")).toBeInTheDocument();
    expect(document.querySelectorAll(".moment-article-search")).toHaveLength(1);
    expect(putCalls()).toHaveLength(0);
  });

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
    expect(
      screen.queryByRole("button", { name: /^一条河流的十年/ }),
    ).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "文章" }));
    // The row that opens it, not the one that deletes it: the delete button
    // names the draft too.
    expect(
      screen.getByRole("button", { name: /^一条河流的十年/ }),
    ).toBeInTheDocument();
    expect(
      calls.some(
        (call) =>
          call.method === "GET" && call.url === "/api/v1/articles/drafts",
      ),
    ).toBe(true);
  });
});

describe("saving while the owner writes", () => {
  it("retains the original search-and-replace panel in editing and saves its replacement", async () => {
    renderEditor(DRAFT_ID);
    await advance();
    expect(
      fireEvent.keyDown(document.body, {
        key: "f",
        code: "KeyF",
        metaKey: true,
      }),
    ).toBe(false);
    fireEvent.keyUp(document.body, { key: "f", code: "KeyF", metaKey: true });
    fireEvent.change(screen.getByRole("textbox", { name: "查找" }), {
      target: { value: "水" },
    });
    await advance(300);
    fireEvent.change(screen.getByRole("textbox", { name: "替换" }), {
      target: { value: "河" },
    });
    await advance(300);
    expect(screen.getByRole("dialog", { name: "查找和替换" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "替换全部结果" }));
    expect(screen.getByText("上游的河是清的")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("switch"));
    expect(screen.getByText("试试查找和替换")).toBeVisible();
    await act(async () => {
      expect(pressSave()).toBe(false);
    });
    expect(JSON.stringify(putCalls()[0].body)).toContain("上游的河是清的");
  });

  it.each(["metaKey", "ctrlKey"] as const)(
    "saves immediately from the title with %s+S and suppresses browser save",
    async (modifier) => {
      renderEditor(DRAFT_ID);
      await advance();
      await typeTitle("快捷键保存的标题");
      await act(async () => {
        expect(pressSave(titleField(), modifier)).toBe(false);
      });
      expect(putCalls()).toHaveLength(1);
      expect(putCalls()[0].body).toMatchObject({
        title: "快捷键保存的标题",
        expected_updated_at: "2026-09-25T02:00:00.000Z",
      });
      await act(async () => {
        expect(pressSave(titleField(), modifier)).toBe(false);
      });
      await advance(2_100);
      expect(putCalls()).toHaveLength(1);
      expect(titleField()).toHaveValue("快捷键保存的标题");
    },
  );

  it("saves from the native document and releases the shortcut in preview and after leaving", async () => {
    const view = renderEditor(DRAFT_ID);
    await advance();
    const documentField = screen.getByRole("textbox", { name: "文章正文" });
    await typeTitle("正文里按保存");
    await act(async () => {
      expect(pressSave(documentField)).toBe(false);
    });
    expect(putCalls()).toHaveLength(1);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "预览" }));
    });
    expect(pressSave()).toBe(true);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "继续编辑" }));
      fireEvent.keyDown(document.body, { key: "s", code: "KeyS" });
      fireEvent.keyUp(document.body, { key: "s", code: "KeyS" });
    });
    expect(putCalls()).toHaveLength(1);
    await typeTitle("菜单里按保存");
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "更多" }));
    });
    await act(async () => {
      expect(
        pressSave(screen.getByRole("menuitem", { name: "立即保存" })),
      ).toBe(false);
    });
    expect(putCalls()).toHaveLength(2);
    expect(putCalls()[1].body).toMatchObject({ title: "菜单里按保存" });
    view.unmount();
    expect(pressSave()).toBe(true);
  });

  it("waits for autosave and then saves the latest revision without repeated writes", async () => {
    let finish!: (response: Response) => void;
    onPut = () =>
      new Promise((resolve) => {
        finish = resolve;
      });
    renderEditor(DRAFT_ID);
    await advance();
    await typeTitle("自动保存中的标题");
    await advance(2_000);
    await typeTitle("快捷键要保存的新标题");
    await act(async () => {
      expect(pressSave(titleField())).toBe(false);
      expect(
        fireEvent.keyDown(titleField(), {
          key: "s",
          code: "KeyS",
          metaKey: true,
          repeat: true,
        }),
      ).toBe(false);
      fireEvent.keyUp(titleField(), { key: "s", code: "KeyS", metaKey: true });
    });
    expect(putCalls()).toHaveLength(1);
    onPut = null;
    await act(async () => {
      finish(success(draftRow({ updated_at: "2026-09-25T02:04:00.000Z" })));
    });
    await advance();
    expect(putCalls()).toHaveLength(2);
    expect(putCalls()[1].body).toMatchObject({
      title: "快捷键要保存的新标题",
      expected_updated_at: "2026-09-25T02:04:00.000Z",
    });
    await advance(2_100);
    expect(putCalls()).toHaveLength(2);
  });

  it("keeps failed work and retries with the save shortcut", async () => {
    onPut = () => Promise.reject(new TypeError("offline"));
    renderEditor(DRAFT_ID);
    await advance();
    await typeTitle("断网时的文字");
    await act(async () => {
      expect(pressSave(titleField())).toBe(false);
    });
    expect(titleField()).toHaveValue("断网时的文字");
    expect(putCalls()).toHaveLength(1);
    const unload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(unload);
    expect(unload.defaultPrevented).toBe(true);
    onPut = null;
    await act(async () => {
      expect(pressSave(titleField())).toBe(false);
    });
    expect(putCalls()).toHaveLength(2);
    const savedUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(savedUnload);
    expect(savedUnload.defaultPrevented).toBe(false);
  });

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

    await typeTitle("还在写");
    await advance(30_000);
    await act(async () => {
      expect(pressSave(titleField())).toBe(false);
    });
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
    expect(window.location.pathname).toBe(
      `/compose/articles/drafts/${DRAFT_ID}`,
    );
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
    expect(screen.getByRole("button", { name: "粗体" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "引用" })).toBeEnabled();
    if (label === "Ordered List") {
      await act(async () => {
        fireEvent.pointerDown(
          screen.getByRole("button", { name: "列表选项" }),
          { button: 0, ctrlKey: false, pointerType: "mouse" },
        );
      });
      await act(async () => {
        fireEvent.click(
          screen.getByRole("menuitem", {
            name: new RegExp(`^${t(label)}$`, "i"),
          }),
        );
      });
    } else {
      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: t(label) }));
      });
    }
    await advance(2_100);
    expect(putCalls()).toHaveLength(1);
    return putCalls()[0].body;
  }

  it("retains native table column widths across opening and saving", async () => {
    onRead = () =>
      success(
        draftRow({
          body: {
            ...body,
            content: [
              {
                type: "table",
                content: [
                  {
                    type: "tableRow",
                    content: [
                      {
                        type: "tableCell",
                        attrs: { colwidth: [240], backgroundColor: "#eff1f5" },
                        content: body.content,
                      },
                    ],
                  },
                ],
              },
            ],
          },
        }),
      );
    renderEditor(DRAFT_ID);
    await advance();
    await typeTitle("调整后的表格");
    await advance(2_100);
    expect(putCalls()).toHaveLength(1);
    expect(JSON.stringify(putCalls()[0].body)).toContain('"colwidth":[240]');
    expect(JSON.stringify(putCalls()[0].body)).toContain(
      '"backgroundColor":"#eff1f5"',
    );
  });

  it("saves a list produced by the template dropdown", async () => {
    const saved = await pressAndSave("Ordered List");
    const serialised = JSON.stringify(saved);
    expect(serialised).toContain('"orderedList"');
    expect(serialised).toContain("上游的水是清的");
  });

  it("saves a blockquote produced by the native toolbar", async () => {
    const saved = await pressAndSave("Blockquote");
    expect(JSON.stringify(saved)).toContain('"blockquote"');
    expect(JSON.stringify(saved)).toContain("上游的水是清的");
  });
});

function publishedRow() {
  return {
    id: DRAFT_ID,
    kind: "article",
    content_md: null,
    article: {
      title: "一条河流的十年",
      excerpt: "上游",
      cover: null,
      body,
      body_schema_version: 1,
    },
    content_html: "<p>上游</p>",
    created_at: "2026-09-25T02:00:00Z",
    updated_at: "2026-09-25T02:00:00Z",
    published_at: "2026-09-25T02:00:00Z",
    reposted_at: null,
    visibility: "only_me",
    timeline_item_kind: "original",
    quoted_post: null,
    comment_count: 0,
    media: [],
    link_cards: [],
  };
}
function RoutedEditor() {
  const navigation = useNavigation();
  if (!navigation.pathname.startsWith("/compose/article"))
    return <p>已离开编辑器</p>;
  return (
    <ArticleEditor
      draftId={articleDraftIdFromPath(navigation.pathname)}
      site={site}
    />
  );
}
function renderRouted(draftId: string | null) {
  window.history.replaceState(
    {},
    "",
    `/compose/article${draftId ? `/${draftId}` : ""}`,
  );
  return render(
    <FeedbackProvider>
      <ThemeProvider>
        <NavigationProvider>
          <RoutedEditor />
        </NavigationProvider>
      </ThemeProvider>
    </FeedbackProvider>,
  );
}

describe("keeping work through asynchronous saves and navigation", () => {
  it("does not reload an older first save over text typed while it was pending", async () => {
    let finish!: (response: Response) => void;
    onPost = () =>
      new Promise((resolve) => {
        finish = resolve;
      });
    renderRouted(null);
    await advance();
    await typeTitle("先写的标题");
    await advance(2_000);
    await typeTitle("请求期间继续写的标题");
    await act(async () => finish(success(draftRow({ title: "先写的标题" }))));
    await advance();
    expect(titleField().value).toBe("请求期间继续写的标题");
    expect(
      calls.filter((c) => c.method === "GET" && c.url.endsWith(DRAFT_ID)),
    ).toHaveLength(0);
    await advance(2_100);
    expect(putCalls().at(-1)?.body).toMatchObject({
      title: "请求期间继续写的标题",
    });
  });

  it("saves immediately before leaving instead of losing the debounce window", async () => {
    renderRouted(DRAFT_ID);
    await advance();
    await typeTitle("还没等到自动保存");
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "返回时间流" })),
    );
    await advance();
    expect(putCalls()[0].body).toMatchObject({ title: "还没等到自动保存" });
    expect(screen.getByText("已离开编辑器")).toBeInTheDocument();
  });

  it("keeps failed saves on screen, warns before unloading, and retries on leaving", async () => {
    onPut = () => Promise.reject(new TypeError("offline"));
    renderRouted(DRAFT_ID);
    await advance();
    await typeTitle("断网也要留下的文字");
    await advance(2_100);
    const unload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(unload);
    expect(unload.defaultPrevented).toBe(true);
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "返回时间流" })),
    );
    await advance();
    expect(titleField().value).toBe("断网也要留下的文字");
    onPut = null;
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "返回时间流" })),
    );
    await advance();
    expect(putCalls().at(-1)?.body).toMatchObject({
      title: "断网也要留下的文字",
    });
    expect(screen.getByText("已离开编辑器")).toBeInTheDocument();
  });

  it("keeps saving edits made during consecutive navigation saves before leaving", async () => {
    let finish!: (response: Response) => void;
    onPut = () =>
      new Promise((resolve) => {
        finish = resolve;
      });
    renderRouted(DRAFT_ID);
    await advance();
    await typeTitle("first");
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "返回时间流" })),
    );
    await typeTitle("second");
    await act(async () => finish(success(draftRow({ title: "first" }))));
    await advance();
    await typeTitle("third");
    await act(async () => finish(success(draftRow({ title: "second" }))));
    await advance();
    expect(titleField().value).toBe("third");
    expect(putCalls().at(-1)?.body).toMatchObject({ title: "third" });
    await act(async () => finish(success(draftRow({ title: "third" }))));
    await advance();
    expect(screen.getByText("已离开编辑器")).toBeInTheDocument();
  });

  it("waits for an in-flight autosave and saves newer text before publishing", async () => {
    let finish!: (response: Response) => void;
    onPut = () =>
      new Promise((resolve) => {
        finish = resolve;
      });
    renderRouted(DRAFT_ID);
    await advance();
    await typeTitle("自动保存的版本");
    await advance(2_000);
    await typeTitle("发布时的最终版本");
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "发布" })),
    );
    expect(calls.some((c) => c.url.endsWith("/publish"))).toBe(false);
    onPut = null;
    await act(async () =>
      finish(success(draftRow({ updated_at: "2026-09-25T02:04:00.000Z" }))),
    );
    await advance();
    expect(putCalls()).toHaveLength(2);
    expect(putCalls()[1].body).toMatchObject({
      title: "发布时的最终版本",
      expected_updated_at: "2026-09-25T02:04:00.000Z",
    });
    expect(calls.some((c) => c.url.endsWith("/publish"))).toBe(true);
  });

  it("keeps a private published article private when publishing its working copy", async () => {
    onRead = () => success(draftRow({ published_entry_id: DRAFT_ID }));
    renderEditor(DRAFT_ID);
    await advance();
    expect(screen.getByRole("button", { name: "选择受众" })).toHaveTextContent(
      "仅自己",
    );
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "发布" })),
    );
    await advance();
    expect(calls.find((c) => c.url.endsWith("/publish"))?.body).toEqual({
      visibility: "only_me",
    });
  });
});

it("keeps the editor DOM and pending text across host focus changes", async () => {
  window.history.replaceState({}, "", `/compose/articles/drafts/${DRAFT_ID}`);
  const toggleFocus = vi.fn();
  const host = (focused: boolean) => (
    <FeedbackProvider>
      <ThemeProvider>
        <NavigationProvider>
          <ArticleEditor
            draftId={DRAFT_ID}
            site={site}
            embedded
            focused={focused}
            onToggleFocus={toggleFocus}
          />
        </NavigationProvider>
      </ThemeProvider>
    </FeedbackProvider>
  );
  const rendered = render(host(false));
  await advance();
  const content = document.querySelector('[contenteditable="true"]');
  expect(content).not.toBeNull();
  const expand = screen.getByRole("button", { name: "全屏编辑" });
  expect(expand).toHaveAttribute("aria-pressed", "false");
  fireEvent.click(expand);
  expect(toggleFocus).toHaveBeenCalledTimes(1);
  await typeTitle("切换全屏前未保存的标题");
  const reads = calls.filter(
    (call) => call.method === "GET" && call.url.endsWith(DRAFT_ID),
  ).length;
  rendered.rerender(host(true));
  await advance();
  const collapse = screen.getByRole("button", { name: "退出全屏编辑" });
  expect(collapse).toHaveAttribute("aria-pressed", "true");
  fireEvent.click(collapse);
  expect(toggleFocus).toHaveBeenCalledTimes(2);
  expect(document.querySelector('[contenteditable="true"]')).toBe(content);
  expect(titleField().value).toBe("切换全屏前未保存的标题");
  rendered.rerender(host(false));
  await advance();
  expect(document.querySelector('[contenteditable="true"]')).toBe(content);
  expect(
    calls.filter(
      (call) => call.method === "GET" && call.url.endsWith(DRAFT_ID),
    ),
  ).toHaveLength(reads);
});

describe("switching drafts without reinitializing the sheet", () => {
  const nextId = "01K2ABCDEF0123456789ABCDE1";
  const lastId = "01K2ABCDEF0123456789ABCDE2";
  const host = (draftId: string) => (
    <FeedbackProvider>
      <ThemeProvider>
        <NavigationProvider>
          <ArticleEditor draftId={draftId} site={site} embedded />
        </NavigationProvider>
      </ThemeProvider>
    </FeedbackProvider>
  );

  it("retains the toolbar, empty cover and document while blocking edits during a read", async () => {
    const rendered = render(host(DRAFT_ID));
    await advance();
    const toolbar = screen.getByRole("toolbar", { name: "文章格式" });
    const content = screen.getByRole("textbox", { name: "文章正文" });
    const cover = screen.getByRole("button", { name: "添加封面图" });
    const bold = screen.getByRole("button", { name: /^粗体$/ });
    let finish!: (response: Response) => void;
    onRead = () => new Promise((resolve) => (finish = resolve));
    rendered.rerender(host(nextId));
    await advance();

    expect(screen.getByRole("toolbar", { name: "文章格式" })).toBe(toolbar);
    expect(screen.getByRole("button", { name: /^粗体$/ })).toBe(bold);
    expect(toolbar).toHaveAttribute("inert");
    expect(cover).toBeInTheDocument();
    expect(cover.closest("[inert]")).not.toBeNull();
    expect(content.closest("[inert]")).not.toBeNull();
    expect(titleField()).toBeDisabled();
    expect(titleField().value).toBe("一条河流的十年");
    expect(screen.getByRole("status")).toHaveTextContent("正在读取草稿…");
    expect(screen.getByRole("status").closest("header")).not.toBeNull();
    expect(screen.getByRole("button", { name: "预览" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "返回文章列表" })).toBeEnabled();
    await act(async () => pressSave(content));
    await act(async () => {
      fireEvent.keyDown(document.body, {
        key: "f",
        code: "KeyF",
        metaKey: true,
      });
      fireEvent.keyUp(document.body, {
        key: "f",
        code: "KeyF",
        metaKey: true,
      });
    });
    expect(
      screen.queryByRole("dialog", { name: "查找和替换" }),
    ).not.toBeInTheDocument();
    await advance(20_000);
    expect(calls.filter((call) => call.method !== "GET")).toEqual([]);

    await act(async () =>
      finish(success(draftRow({ id: nextId, title: "下一份草稿" }))),
    );
    await advance();
    expect(screen.getByRole("toolbar", { name: "文章格式" })).toBe(toolbar);
    expect(screen.getByRole("button", { name: /^粗体$/ })).toBe(bold);
    expect(screen.getByRole("textbox", { name: "文章正文" })).toBe(content);
    expect(toolbar).not.toHaveAttribute("inert");
    expect(content.closest("[inert]")).toBeNull();
    expect(titleField().value).toBe("下一份草稿");
    expect(titleField()).toBeEnabled();
    expect(document.querySelector("[data-article-theme]")).toHaveAttribute(
      "aria-busy",
      "false",
    );
    expect(calls.filter((call) => call.method !== "GET")).toEqual([]);
  });

  it("keeps a portaled mobile toolbar inert during a read and resets its open view", async () => {
    vi.stubGlobal("matchMedia", () => ({
      matches: true,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
    const rendered = render(host(DRAFT_ID));
    await advance();
    const toolbar = screen.getByRole("toolbar", { name: "文章格式" });
    expect(toolbar.parentElement).toBe(document.body);
    const format = screen.getByRole("button", { name: "高亮文字" });
    fireEvent.click(format);
    expect(
      screen.getByRole("button", { name: "返回格式工具" }),
    ).toBeInTheDocument();
    let finish!: (response: Response) => void;
    onRead = () => new Promise((resolve) => (finish = resolve));
    rendered.rerender(host(nextId));
    await advance();
    expect(screen.getByRole("toolbar", { name: "文章格式" })).toBe(toolbar);
    expect(toolbar).toHaveAttribute("inert");
    expect(toolbar.parentElement).toBe(document.body);
    expect(
      screen.queryByRole("button", { name: "返回格式工具" }),
    ).not.toBeInTheDocument();
    await act(async () => finish(success(draftRow({ id: nextId }))));
    await advance();
    expect(screen.getByRole("toolbar", { name: "文章格式" })).toBe(toolbar);
    expect(toolbar).not.toHaveAttribute("inert");
  });

  it("keeps the previous cover intact until the selected draft is ready and ignores obsolete reads", async () => {
    const coverId = "01K2ABCDEF0123456789ABCDE3";
    onRead = () => success(draftRow({ cover_media_id: coverId }));
    const rendered = render(host(DRAFT_ID));
    await advance();
    const remove = screen.getByRole("button", { name: "移除封面图" });
    let finishNext!: (response: Response) => void;
    onRead = () => new Promise((resolve) => (finishNext = resolve));
    rendered.rerender(host(nextId));
    await advance();
    fireEvent.click(remove); // The handler is guarded as well as the inert sheet.
    expect(remove).toBeInTheDocument();
    expect(calls.filter((call) => call.method !== "GET")).toEqual([]);

    let finishLast!: (response: Response) => void;
    onRead = () => new Promise((resolve) => (finishLast = resolve));
    rendered.rerender(host(lastId));
    await advance();
    await act(async () =>
      finishLast(success(draftRow({ id: lastId, title: "最后选择的草稿" }))),
    );
    await advance();
    expect(titleField().value).toBe("最后选择的草稿");
    expect(screen.getByRole("button", { name: "添加封面图" })).toBeEnabled();

    await act(async () =>
      finishNext(success(draftRow({ id: nextId, title: "已经跳过的草稿" }))),
    );
    await advance();
    expect(titleField().value).toBe("最后选择的草稿");
    expect(calls.filter((call) => call.method !== "GET")).toEqual([]);
  });

  it("ignores an audience selection from a portal left open during a read", async () => {
    const rendered = render(host(DRAFT_ID));
    await advance();
    fireEvent.click(screen.getByRole("button", { name: "选择受众" }));
    await advance();
    const privateAudience = screen.getByRole("menuitemradio", {
      name: "仅自己",
    });
    let finish!: (response: Response) => void;
    onRead = () => new Promise((resolve) => (finish = resolve));
    rendered.rerender(host(nextId));
    await advance();
    fireEvent.click(privateAudience);
    await advance();
    await act(async () => finish(success(draftRow({ id: nextId }))));
    await advance();
    expect(screen.getByRole("button", { name: "选择受众" })).toHaveAttribute(
      "data-audience",
      "everyone",
    );
    expect(calls.filter((call) => call.method !== "GET")).toEqual([]);
  });

  it("blocks writes after a failed read and recovers when another draft is selected", async () => {
    const rendered = render(host(DRAFT_ID));
    await advance();
    onRead = () => Promise.reject(new TypeError("offline"));
    rendered.rerender(host(nextId));
    await advance();
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(titleField()).toBeDisabled();
    expect(screen.getByRole("button", { name: "发布" })).toBeDisabled();
    await act(async () => pressSave());
    await advance(20_000);
    expect(calls.filter((call) => call.method !== "GET")).toEqual([]);

    onRead = () => success(draftRow({ id: lastId, title: "恢复读取的草稿" }));
    rendered.rerender(host(lastId));
    await advance();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(titleField().value).toBe("恢复读取的草稿");
    expect(titleField()).toBeEnabled();
    expect(screen.getByRole("button", { name: /^粗体$/ })).toBeEnabled();
    expect(calls.filter((call) => call.method !== "GET")).toEqual([]);
  });
});

it("switches interface language without replacing the editor, losing selection, or clearing undo history", async () => {
  renderEditor(DRAFT_ID);
  await advance();
  const surface = screen.getByRole("textbox", { name: "文章正文" });
  const editor = document.querySelector<HTMLElement & { editor: Editor }>(
    '.tiptap.ProseMirror[contenteditable="true"]',
  )?.editor;
  if (!editor) throw new Error("missing editor");
  const before = editor.getJSON();
  await act(async () => {
    editor
      .chain()
      .focus()
      .setTextSelection(2)
      .insertContent("Keep this change")
      .run();
  });
  const edited = editor.getJSON();
  const selection = editor.state.selection.toJSON();
  await act(async () => {
    await i18n.changeLanguage("en");
  });
  expect(screen.getByRole("textbox", { name: "Article body" })).toBe(surface);
  expect(editor.getJSON()).toEqual(edited);
  expect(editor.state.selection.toJSON()).toEqual(selection);
  await act(async () => {
    editor.commands.undo();
  });
  expect(editor.getJSON()).toEqual(before);
  await act(async () => {
    editor.commands.redo();
  });
  expect(editor.getJSON()).toEqual(edited);
  await act(async () => {
    await i18n.changeLanguage("zh-CN");
  });
  expect(screen.getByRole("textbox", { name: "文章正文" })).toBe(surface);
});
