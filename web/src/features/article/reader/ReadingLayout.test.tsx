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
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Editor } from "@tiptap/core";
import type { ArticleDocument } from "../../../api/public";
import { NavigationProvider } from "../../../app/navigation";
import { MentionProvider } from "../../../app/mentions";
import { ArticleSheet } from "../../../components/content/ArticleSheet";
import { HeaderActionsContext } from "../../../components/headerActions";
import { ArticleReadingContent } from "./ArticleReadingContent";
import { ReadingLayoutSettings } from "./ReadingLayoutSettings";
import {
  DEFAULT_READING_LAYOUT,
  ReadingLayoutProvider,
  READING_LAYOUT_STORAGE_KEY,
  readReadingLayout,
} from "../ReadingLayout";
import { applyReadingColor } from "./readingAnnotations";

let slot: HTMLElement;
beforeEach(() => {
  window.localStorage.clear();
  slot = document.createElement("div");
  document.body.append(slot);
  vi.spyOn(window, "scrollBy").mockImplementation(() => {});
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  slot.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
function settings() {
  return render(
    <ReadingLayoutProvider>
      <ReadingLayoutSettings />
    </ReadingLayoutProvider>,
  );
}
async function open() {
  fireEvent.click(screen.getByRole("button", { name: "阅读排版" }));
  return screen.findByRole("dialog", { name: "阅读排版设置" });
}

it("starts with existing defaults and validates persisted preferences", () => {
  expect(readReadingLayout()).toEqual(DEFAULT_READING_LAYOUT);
  for (const value of [
    "{",
    "null",
    JSON.stringify({ width: "full", fontSize: 16, lineHeight: "normal" }),
    JSON.stringify({ width: "wide", fontSize: 15, lineHeight: "normal" }),
    JSON.stringify({ width: "wide", fontSize: 26, lineHeight: "normal" }),
  ]) {
    window.localStorage.setItem(READING_LAYOUT_STORAGE_KEY, value);
    expect(readReadingLayout()).toEqual(DEFAULT_READING_LAYOUT);
  }
  for (const width of ["standard", "wide", "wider"]) {
    for (const fontSize of [14, 16, 18, 20, 22, 24]) {
      for (const lineHeight of ["normal", "compact", "relaxed"]) {
        const value = { width, fontSize, lineHeight };
        window.localStorage.setItem(
          READING_LAYOUT_STORAGE_KEY,
          JSON.stringify(value),
        );
        expect(readReadingLayout()).toEqual(value);
      }
    }
  }
});

it("hydrates before opening, persists choices across mounts, and restores only its own storage key", async () => {
  window.localStorage.setItem("moment.theme.v1", "dim");
  window.localStorage.setItem(
    READING_LAYOUT_STORAGE_KEY,
    JSON.stringify({ width: "wide", fontSize: 18, lineHeight: "relaxed" }),
  );
  const mounted = settings();
  let panel = await open();
  expect(within(panel).getByRole("radio", { name: "宽" })).toBeChecked();
  expect(within(panel).getByLabelText("当前字号")).toHaveTextContent("18px");
  fireEvent.click(within(panel).getByRole("radio", { name: "更宽" }));
  fireEvent.click(within(panel).getByRole("button", { name: "增大字号" }));
  fireEvent.click(within(panel).getByRole("radio", { name: "紧凑" }));
  mounted.unmount();
  settings();
  panel = await open();
  expect(within(panel).getByRole("radio", { name: "更宽" })).toBeChecked();
  expect(within(panel).getByLabelText("当前字号")).toHaveTextContent("20px");
  expect(within(panel).getByRole("radio", { name: "紧凑" })).toBeChecked();
  fireEvent.click(within(panel).getByRole("button", { name: "恢复默认" }));
  expect(readReadingLayout()).toEqual(DEFAULT_READING_LAYOUT);
  expect(window.localStorage.getItem(READING_LAYOUT_STORAGE_KEY)).toBeNull();
  expect(window.localStorage.getItem("moment.theme.v1")).toBe("dim");
});

it("bounds font adjustment and keeps controls usable when storage is blocked", async () => {
  vi.spyOn(window.localStorage, "getItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  vi.spyOn(window.localStorage, "removeItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  settings();
  const panel = await open();
  const decrease = within(panel).getByRole("button", { name: "减小字号" });
  const increase = within(panel).getByRole("button", { name: "增大字号" });
  fireEvent.click(decrease);
  expect(decrease).toBeDisabled();
  expect(within(panel).getByLabelText("当前字号")).toHaveTextContent("14px");
  for (let i = 0; i < 5; i++) fireEvent.click(increase);
  expect(increase).toBeDisabled();
  expect(within(panel).getByLabelText("当前字号")).toHaveTextContent("24px");
  fireEvent.click(within(panel).getByRole("button", { name: "恢复默认" }));
  expect(within(panel).getByLabelText("当前字号")).toHaveTextContent("16px");
});

it("automatically adapts width on mobile without losing the stored desktop choice", async () => {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query === "(max-width: 719px)",
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
  }));
  window.localStorage.setItem(
    READING_LAYOUT_STORAGE_KEY,
    JSON.stringify({ width: "wider", fontSize: 16, lineHeight: "normal" }),
  );
  settings();
  const panel = await open();
  expect(within(panel).getByText("自动适配屏幕")).toBeInTheDocument();
  const width = within(panel).getByRole("radiogroup", { name: "正文宽度" });
  for (const option of within(width).getAllByRole("radio"))
    expect(option).toBeDisabled();
  expect(within(width).getByRole("radio", { name: "更宽" })).toBeChecked();
  fireEvent.click(within(panel).getByRole("button", { name: "增大字号" }));
  expect(readReadingLayout().width).toBe("wider");
});

const body: ArticleDocument = {
  schema_version: 2,
  type: "doc",
  content: [
    {
      type: "paragraph",
      content: [
        {
          type: "text",
          text: "river flows into the sea",
          marks: [{ type: "textStyle", attrs: { color: "red" } }],
        },
      ],
    },
    { type: "paragraph", content: [{ type: "text", text: "another river" }] },
  ],
};
function reading(enabled = true) {
  return render(
    <ReadingLayoutProvider>
      <NavigationProvider>
        <MentionProvider known={[]}>
          <HeaderActionsContext.Provider value={slot}>
            <ArticleSheet
              enableReadingLayout={enabled}
              title="title"
              byline="author"
            >
              <ArticleReadingContent
                document={body}
                enableReadingLayout={enabled}
                enableAnnotations
                enableExcerptPanel
              />
            </ArticleSheet>
          </HeaderActionsContext.Provider>
        </MentionProvider>
      </NavigationProvider>
    </ReadingLayoutProvider>,
  );
}

it("changes only reading presentation while preserving the editor, marks, selection and search", async () => {
  reading();
  const current = () =>
    (
      document.querySelector("[data-content-renderer]") as HTMLElement & {
        editor: Editor;
      }
    ).editor;
  await waitFor(() => expect(current()).toBeDefined());
  const original = current();
  const json = original.getJSON();
  const update = vi.fn();
  original.on("update", update);
  act(() => {
    original.commands.setTextSelection({ from: 2, to: 6 });
    applyReadingColor(original, { from: 2, to: 6 }, "highlight", "yellow");
  });
  fireEvent.click(screen.getByRole("button", { name: "Search" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Search" }), {
    target: { value: "river" },
  });
  await waitFor(() =>
    expect(original.storage.findAndReplace.searchTerm).toBe("river"),
  );
  const ranges = original.storage.readingAnnotations.ranges;
  const selection = original.state.selection;
  const panel = await open();
  fireEvent.click(within(panel).getByRole("radio", { name: "更宽" }));
  fireEvent.click(within(panel).getByRole("button", { name: "增大字号" }));
  fireEvent.click(within(panel).getByRole("radio", { name: "宽松" }));
  expect(current()).toBe(original);
  expect(original.getJSON()).toEqual(json);
  expect(original.storage.readingAnnotations.ranges).toBe(ranges);
  expect(original.state.selection).toEqual(selection);
  expect(original.storage.findAndReplace.searchTerm).toBe("river");
  expect(update).not.toHaveBeenCalled();
  expect(document.querySelector("[data-article-sheet]")).toHaveAttribute(
    "data-reading-line-height",
    "relaxed",
  );
  expect(
    document.querySelector("[data-article-sheet]")?.getAttribute("style"),
  ).toContain("--reading-preferred-width: 68.75rem");
});

it("does not mount settings or apply preferences without the explicit reading opt-in", async () => {
  window.localStorage.setItem(
    READING_LAYOUT_STORAGE_KEY,
    JSON.stringify({ width: "wider", fontSize: 24, lineHeight: "relaxed" }),
  );
  reading(false);
  await screen.findByText("river flows into the sea");
  expect(screen.queryByRole("button", { name: "阅读排版" })).toBeNull();
  expect(document.querySelector("[data-article-sheet]")).not.toHaveAttribute(
    "data-reading-layout",
  );
  expect(document.querySelector("[data-article-sheet]")).not.toHaveAttribute(
    "style",
  );
});
