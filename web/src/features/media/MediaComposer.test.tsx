import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { MediaComposer } from "./MediaComposer";
import type { ComposerMediaItem } from "./mediaState";

afterEach(cleanup);

function item(key: string): ComposerMediaItem {
  return {
    key,
    file: null,
    objectUrl: null,
    localPreviewUrl: null,
    mediaId: `01K2ABCDEF0123456789ABCD${key}`,
    previewUrl: `/api/v1/media/${key}/preview`,
    originalUrl: `/api/v1/media/${key}`,
    width: null,
    height: null,
    byteSize: null,
    animatedOriginal: false,
    altMode: "missing",
    altText: "",
    state: "ready",
    progress: 100,
    error: null,
  };
}

function FocusHarness({ initial }: { initial: ComposerMediaItem[] }) {
  const [items, setItems] = useState(initial);
  return (
    <MediaComposer
      disabled={false}
      items={items}
      onAdd={vi.fn()}
      onChange={(next) => setItems(next)}
      onRemove={(key) =>
        setItems((current) => current.filter((value) => value.key !== key))
      }
      onRetry={vi.fn()}
    />
  );
}

describe("media editor", () => {
  it("preserves duplicate selections as two ordered usages", () => {
    const onAdd = vi.fn();
    render(
      <MediaComposer
        disabled={false}
        items={[]}
        onAdd={onAdd}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const duplicate = new File(["same"], "same.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("图片文件"), {
      target: { files: [duplicate, duplicate] },
    });
    expect(onAdd).toHaveBeenCalledWith([duplicate, duplicate]);
  });

  it("supports keyboard-usable ordering and explicit alt modes", () => {
    const onChange = vi.fn();
    const items = [item("00"), item("01")];
    render(
      <MediaComposer
        disabled={false}
        items={items}
        onAdd={vi.fn()}
        onChange={onChange}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    fireEvent.click(screen.getAllByRole("button", { name: /下移/ })[0]);
    expect(
      onChange.mock.calls[0][0].map((value: ComposerMediaItem) => value.key),
    ).toEqual(["01", "00"]);
    fireEvent.click(screen.getAllByLabelText("纯装饰")[0]);
    expect(onChange.mock.calls[1][0][0].altMode).toBe("decorative");
  });

  it("shows determinate upload progress and retry without blocking remove", () => {
    render(
      <MediaComposer
        disabled={false}
        items={[
          item("00"),
          { ...item("01"), state: "uploading", progress: 47 },
          { ...item("02"), state: "failed", error: "网络中断" },
        ]}
        onAdd={vi.fn()}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    expect(
      screen.getByRole("progressbar", { name: "第 2 张上传进度" }),
    ).toHaveValue(47);
    expect(screen.getByRole("button", { name: /重试上传/ })).toBeEnabled();
    expect(screen.getAllByRole("button", { name: /移除/ })[1]).toBeEnabled();
  });

  it.each([
    { name: "first", remove: 0, expected: "01" },
    { name: "middle", remove: 1, expected: "02" },
    { name: "last", remove: 2, expected: "01" },
  ])(
    "restores focus after removing the $name item",
    async ({ remove, expected }) => {
      render(<FocusHarness initial={[item("00"), item("01"), item("02")]} />);
      fireEvent.click(screen.getAllByRole("button", { name: /移除/ })[remove]);
      await waitFor(() =>
        expect(document.activeElement).toHaveAttribute(
          "data-media-key",
          expected,
        ),
      );
    },
  );

  it("returns focus to the picker after removing the final item", async () => {
    render(<FocusHarness initial={[item("00")]} />);
    fireEvent.click(screen.getByRole("button", { name: /移除/ }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "选择图片" })).toHaveFocus(),
    );
  });

  it("keeps focus with the moved usage after keyboard reordering", async () => {
    render(<FocusHarness initial={[item("00"), item("01")]} />);
    fireEvent.click(screen.getAllByRole("button", { name: /下移/ })[0]);
    await waitFor(() =>
      expect(document.activeElement).toHaveAttribute("data-media-key", "00"),
    );
  });
});
