import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Dialog } from "./Dialog";
import { ShareMenu } from "./TimelineInteractions";
import * as cards from "../features/share/shareCard";

beforeEach(() => {
  vi.spyOn(cards, "renderShareCard").mockResolvedValue(
    new Blob(["png"], { type: "image/png" }),
  );
  vi.stubGlobal(
    "URL",
    class extends URL {
      static createObjectURL = vi.fn(() => "blob:share-preview");
      static revokeObjectURL = vi.fn();
    },
  );
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("share menu focus", () => {
  it("hands focus to the image preview and restores the share trigger when it closes", async () => {
    render(
      <Dialog.Root defaultOpen>
        <Dialog.Portal>
          <Dialog.Popup>
            <Dialog.Title>图片查看</Dialog.Title>
            <ShareMenu
              baseUrl={null}
              href="/m/example"
              title="Moment"
              trigger={<button>分享图片内容</button>}
              card={{
                displayName: "作者",
                username: "writer",
                avatarUrl: null,
                publishedLabel: "2026",
                blocks: [],
                media: [],
                location: null,
                link: null,
              }}
            />
          </Dialog.Popup>
        </Dialog.Portal>
      </Dialog.Root>,
    );
    const trigger = screen.getByRole("button", { name: "分享图片内容" });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: "ArrowDown", code: "ArrowDown" });
    fireEvent.keyUp(trigger, { key: "ArrowDown", code: "ArrowDown" });
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "生成分享图片" }),
    );
    const preview = await screen.findByRole("dialog", { name: "分享图片" });
    await within(preview).findByRole("img", { name: "分享图片预览" });
    await waitFor(() => expect(preview).toHaveFocus());
    fireEvent.click(within(preview).getByRole("button", { name: "关闭" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "分享图片" })).toBeNull(),
    );
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(
      screen.getByRole("dialog", { name: "图片查看" }),
    ).toBeInTheDocument();
  });
});
