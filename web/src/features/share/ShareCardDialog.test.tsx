import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as cards from "./shareCard";
import * as feedback from "../../components/Feedback";
import { ShareCardDialog } from "./ShareCardDialog";

const input: cards.ShareCardInput = {
  displayName: "作者",
  username: "author",
  avatarUrl: null,
  publishedLabel: "草稿摘录",
  articleTitle: "文章标题",
  blocks: [{ kind: "text", text: "选中的摘录" }],
  media: [],
  location: null,
  link: null,
};

beforeEach(() => {
  vi.spyOn(cards, "renderShareCard").mockResolvedValue(
    new Blob(["png"], { type: "image/png" }),
  );
  vi.spyOn(feedback, "notify").mockImplementation(() => "notification");
  vi.stubGlobal(
    "URL",
    class extends URL {
      static createObjectURL = vi.fn(() => "blob:excerpt");
      static revokeObjectURL = vi.fn();
    },
  );
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("sharing an exact excerpt", () => {
  it("reuses one generated preview across ordinary rerenders", async () => {
    const onOpenChange = vi.fn();
    const show = (open: boolean) => (
      <ShareCardDialog input={input} open={open} onOpenChange={onOpenChange} />
    );
    const mounted = render(show(true));
    expect(
      await screen.findByRole("img", { name: "分享图片预览" }),
    ).toHaveAttribute("src", "blob:excerpt");
    mounted.rerender(show(true));
    expect(cards.renderShareCard).toHaveBeenCalledExactlyOnceWith(
      input,
      expect.any(AbortSignal),
    );
    expect(screen.getByRole("button", { name: "保存到本地" })).toBeEnabled();
    mounted.unmount();
    expect(vi.mocked(cards.renderShareCard).mock.calls[0][1]?.aborted).toBe(
      true,
    );
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:excerpt");
  });

  it("shows the overlong-selection guidance and never enables partial-image export", async () => {
    vi.mocked(cards.renderShareCard).mockRejectedValue(
      new cards.ShareCardTooLongError(),
    );
    render(<ShareCardDialog input={input} open onOpenChange={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("60 行上限");
    expect(screen.getByRole("button", { name: "保存到本地" })).toBeDisabled();
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
  });

  it("retains the existing retry guidance for rendering failures", async () => {
    vi.mocked(cards.renderShareCard).mockRejectedValue(
      new Error("canvas unavailable"),
    );
    render(<ShareCardDialog input={input} open onOpenChange={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "没能生成图片。可以关掉重试。",
    );
  });

  it("reports image-copy failure without closing or claiming success", async () => {
    vi.stubGlobal("ClipboardItem", class {});
    const write = vi.fn().mockRejectedValue(new Error("clipboard denied"));
    vi.stubGlobal(
      "navigator",
      new Proxy(navigator, {
        get: (target, key) =>
          key === "clipboard" ? { write } : Reflect.get(target, key, target),
      }),
    );
    const onOpenChange = vi.fn();
    render(<ShareCardDialog input={input} open onOpenChange={onOpenChange} />);
    await screen.findByRole("img");
    fireEvent.click(screen.getByRole("button", { name: "复制图片" }));
    await waitFor(() =>
      expect(feedback.notify).toHaveBeenCalledWith(
        "这个浏览器不能复制图片，可以先保存再发送",
        { type: "error" },
      ),
    );
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "保存到本地" })).toBeEnabled();
  });

  it("uses the native finalFocus target after closing", async () => {
    const target = document.createElement("button");
    document.body.append(target);
    const onOpenChange = vi.fn();
    const show = (open: boolean) => (
      <ShareCardDialog
        input={input}
        open={open}
        onOpenChange={onOpenChange}
        finalFocus={() => target}
      />
    );
    const mounted = render(show(true));
    await screen.findByRole("img");
    fireEvent.click(screen.getByRole("button", { name: "关闭" }));
    expect(onOpenChange).toHaveBeenCalledWith(
      false,
      expect.objectContaining({ reason: "close-press" }),
    );
    mounted.rerender(show(false));
    await waitFor(() => expect(document.activeElement).toBe(target));
    target.remove();
  });
});
