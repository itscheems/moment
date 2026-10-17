import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { MediaDescriptionEditor } from "./MediaDescriptionEditor";
import { newMediaItem, type ComposerMediaItem } from "./mediaState";

const picture = (key: string): ComposerMediaItem => ({
  ...newMediaItem(new File([key], key + ".png", { type: "image/png" })),
  key,
  previewUrl: "/" + key + ".png",
});
function Harness({
  initial,
  onChange = vi.fn(),
}: {
  initial: ComposerMediaItem[];
  onChange?: (next: ComposerMediaItem[]) => void;
}) {
  const [items, setItems] = useState(initial);
  return (
    <MediaDescriptionEditor
      items={items}
      disabled={false}
      onChange={(next) => {
        setItems(next);
        onChange(next);
      }}
    />
  );
}
afterEach(cleanup);
const open = () =>
  fireEvent.click(
    screen.getByRole("button", {
      name: /Add descriptions|image descriptions?/,
    }),
  );
const input = () => screen.getByRole("textbox", { name: "Description" });

describe("media descriptions", () => {
  it("opens directly on the first image with an optional text field", () => {
    render(<Harness initial={[picture("a"), picture("b")]} />);
    open();
    expect(
      screen.getByRole("heading", { name: "Edit image description" }),
    ).toBeInTheDocument();
    expect(input()).toHaveValue("");
    expect(screen.getByText("0 / 1,000")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Previous image" }),
    ).toBeDisabled();
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
    expect(
      screen.queryByText(/未描述|纯装饰|上移|下移/),
    ).not.toBeInTheDocument();
  });
  it("keeps each image's draft while paging and saves all descriptions together", async () => {
    const changed = vi.fn();
    render(
      <Harness initial={[picture("a"), picture("b")]} onChange={changed} />,
    );
    open();
    fireEvent.change(input(), { target: { value: "第一张" } });
    fireEvent.click(screen.getByRole("button", { name: "Next image" }));
    expect(input()).toHaveValue("");
    expect(screen.getByRole("button", { name: "Next image" })).toBeDisabled();
    fireEvent.change(input(), { target: { value: "第二张" } });
    fireEvent.click(screen.getByRole("button", { name: "Previous image" }));
    expect(input()).toHaveValue("第一张");
    expect(changed).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(
      changed.mock.calls[0][0].map((item: ComposerMediaItem) => [
        item.key,
        item.altMode,
        item.altText,
      ]),
    ).toEqual([
      ["a", "provided", "第一张"],
      ["b", "provided", "第二张"],
    ]);
    expect(
      screen.getByRole("button", { name: "2 image descriptions" }),
    ).toBeInTheDocument();
    open();
    expect(input()).toHaveValue("第一张");
  });
  it("discards unsaved changes when returning and restores the trigger", async () => {
    const changed = vi.fn();
    render(
      <Harness
        initial={[{ ...picture("a"), altMode: "provided", altText: "已保存" }]}
        onChange={changed}
      />,
    );
    open();
    fireEvent.change(input(), { target: { value: "不要保存" } });
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(changed).not.toHaveBeenCalled();
    open();
    expect(input()).toHaveValue("已保存");
  });
  it("clears a description to missing without changing untouched legacy metadata", () => {
    const changed = vi.fn();
    render(
      <Harness
        initial={[
          { ...picture("a"), altMode: "provided", altText: "清除" },
          { ...picture("b"), altMode: "decorative" },
        ]}
        onChange={changed}
      />,
    );
    open();
    fireEvent.change(input(), { target: { value: "  " } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(changed.mock.calls[0][0][0]).toMatchObject({
      altMode: "missing",
      altText: "",
    });
    expect(changed.mock.calls[0][0][1].altMode).toBe("decorative");
  });
  it("counts and limits Unicode code points rather than splitting emoji", () => {
    render(<Harness initial={[picture("a")]} />);
    open();
    fireEvent.change(input(), { target: { value: "😀".repeat(1001) } });
    expect(input()).toHaveValue("😀".repeat(1000));
    expect(screen.getByText("1000 / 1,000")).toBeInTheDocument();
  });
  it("merges drafts into current upload state instead of restoring stale items", () => {
    const initial = [picture("a")],
      changed = vi.fn();
    const props = { disabled: false, onChange: changed };
    const { rerender } = render(
      <MediaDescriptionEditor {...props} items={initial} />,
    );
    open();
    fireEvent.change(input(), { target: { value: "上传中写的" } });
    rerender(
      <MediaDescriptionEditor
        {...props}
        items={[
          { ...initial[0], state: "ready", progress: 100, mediaId: "uploaded" },
        ]}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(changed.mock.calls[0][0][0]).toMatchObject({
      state: "ready",
      mediaId: "uploaded",
      altText: "上传中写的",
    });
  });
  it("does not mark the composer dirty when saving unchanged descriptions", () => {
    const changed = vi.fn();
    render(<Harness initial={[picture("a")]} onChange={changed} />);
    open();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(changed).not.toHaveBeenCalled();
  });

  it("keeps a single image free of paging controls", () => {
    render(<Harness initial={[picture("a")]} />);
    open();
    expect(
      screen.queryByRole("button", { name: "Next image" }),
    ).not.toBeInTheDocument();
  });
  it("opens and dismisses the alt text help without losing the draft", async () => {
    render(<Harness initial={[picture("a")]} />);
    open();
    fireEvent.change(input(), { target: { value: "保留" } });
    fireEvent.click(screen.getByRole("button", { name: "What is alt text?" }));
    expect(
      await screen.findByRole("heading", { name: "Add descriptions" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Got it" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("heading", { name: "Add descriptions" }),
      ).not.toBeInTheDocument(),
    );
    expect(input()).toHaveValue("保留");
  });
});
