import "@testing-library/jest-dom/vitest";
import { useRef, useState } from "react";
import { createPortal } from "react-dom";
import { afterEach, expect, it } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  act,
} from "@testing-library/react";
import { useEditHistory } from "./useEditHistory";

afterEach(cleanup);

function Editor() {
  const [value, setValue] = useState({ text: "原文", map: true, count: 2 });
  const input = useRef<HTMLTextAreaElement>(null);
  const { reset, ...events } = useEditHistory({
    value,
    signature: JSON.stringify(value),
    group: JSON.stringify([value.map, value.count]),
    input,
    restore: setValue,
  });
  return (
    <section {...events}>
      <textarea
        aria-label="正文"
        ref={input}
        value={value.text}
        onChange={(e) => setValue({ ...value, text: e.target.value })}
      />
      <input aria-label="搜索" />
      <button onClick={() => setValue({ ...value, map: false })}>
        移除地图
      </button>
      <button onClick={() => setValue({ ...value, count: value.count + 1 })}>
        添加地点
      </button>
      <button
        onClick={() => {
          reset();
          setValue({ text: "", map: false, count: 0 });
        }}
      >
        清空
      </button>
      <output>{JSON.stringify(value)}</output>
      {createPortal(
        <div role="dialog" data-map-fullscreen="">
          <button>临时选点</button>
        </div>,
        document.body,
      )}
    </section>
  );
}

it("undoes text and structural edits in order, supports redo and clears the redo branch after a new edit", () => {
  render(<Editor />);
  const input = screen.getByLabelText("正文");
  fireEvent.change(input, { target: { value: "新" } });
  fireEvent.change(input, { target: { value: "新正文" } });
  fireEvent.click(screen.getByText("移除地图"));
  fireEvent.keyDown(input, { key: "z", metaKey: true });
  expect(screen.getByRole("status").textContent).toContain('"map":true');
  expect(input).toHaveValue("新正文");
  fireEvent.keyDown(input, { key: "z", ctrlKey: true });
  expect(input).toHaveValue("原文");
  fireEvent.keyDown(input, { key: "z", ctrlKey: true });
  expect(input).toHaveValue("原文");
  fireEvent.keyDown(input, { key: "z", metaKey: true, shiftKey: true });
  expect(input).toHaveValue("新正文");
  fireEvent.click(screen.getByText("添加地点"));
  fireEvent.keyDown(input, { key: "y", ctrlKey: true });
  expect(screen.getByRole("status").textContent).toContain('"count":3');
  expect(screen.getByRole("status").textContent).toContain('"map":true');
});

it("records an IME composition as one change and clears history after an explicit discard", async () => {
  render(<Editor />);
  const input = screen.getByLabelText("正文");
  fireEvent.compositionStart(input);
  fireEvent.change(input, { target: { value: "中" } });
  fireEvent.change(input, { target: { value: "中文" } });
  fireEvent.compositionEnd(input);
  await act(
    () =>
      new Promise<void>((resolve) => requestAnimationFrame(() => resolve())),
  );
  fireEvent.keyDown(input, { key: "z", metaKey: true });
  expect(input).toHaveValue("原文");
  fireEvent.click(screen.getByText("清空"));
  fireEvent.keyDown(input, { key: "z", ctrlKey: true });
  expect(input).toHaveValue("");
});

it("leaves a nested search field's native undo and IME composition alone", () => {
  render(<Editor />);
  fireEvent.click(screen.getByText("移除地图"));
  fireEvent.keyDown(screen.getByLabelText("搜索"), { key: "z", metaKey: true });
  expect(screen.getByRole("status").textContent).toContain('"map":false');
  fireEvent.keyDown(screen.getByLabelText("正文"), {
    key: "z",
    metaKey: true,
    isComposing: true,
  });
  expect(screen.getByRole("status").textContent).toContain('"map":false');
});

it("does not undo the background draft from an uncommitted map picker portal", () => {
  render(<Editor />);
  fireEvent.click(screen.getByText("移除地图"));
  fireEvent.keyDown(screen.getByText("临时选点"), { key: "z", metaKey: true });
  expect(screen.getByRole("status").textContent).toContain('"map":false');
});

it("keeps a committed IME word when the next click removes a map before the next frame", () => {
  render(<Editor />);
  const input = screen.getByLabelText("正文");
  fireEvent.compositionStart(input);
  fireEvent.change(input, { target: { value: "原文中文" } });
  fireEvent.compositionEnd(input);
  fireEvent.click(screen.getByText("移除地图"));
  fireEvent.keyDown(input, { key: "z", metaKey: true });
  expect(input).toHaveValue("原文中文");
  expect(screen.getByRole("status").textContent).toContain('"map":true');
});

it("resets an unchanged document without consuming its next edit", () => {
  render(<Editor />);
  fireEvent.click(screen.getByText("清空"));
  fireEvent.click(screen.getByText("清空"));
  const input = screen.getByLabelText("正文");
  fireEvent.change(input, { target: { value: "第一笔编辑" } });
  fireEvent.keyDown(input, { key: "z", metaKey: true });
  expect(input).toHaveValue("");
});

it("restores the caret at the original insertion point when undoing the first text edit", async () => {
  render(<Editor />);
  const input = screen.getByLabelText<HTMLTextAreaElement>("正文");
  input.focus();
  input.setSelectionRange(1, 1);
  fireEvent.keyPress(input, { key: "x", charCode: 120, which: 120 });
  fireEvent.change(input, {
    target: { value: "原x文", selectionStart: 2, selectionEnd: 2 },
  });
  fireEvent.keyDown(input, { key: "z", metaKey: true });
  await act(
    () =>
      new Promise<void>((resolve) => requestAnimationFrame(() => resolve())),
  );
  expect(input).toHaveValue("原文");
  expect(input.selectionStart).toBe(1);
});

it("restores the original insertion point after a committed Chinese composition", async () => {
  render(<Editor />);
  const input = screen.getByLabelText<HTMLTextAreaElement>("正文");
  input.focus();
  input.setSelectionRange(1, 1);
  fireEvent.compositionStart(input);
  fireEvent.change(input, {
    target: { value: "原中文文", selectionStart: 3, selectionEnd: 3 },
  });
  fireEvent.compositionEnd(input, { data: "中文" });
  fireEvent.keyDown(input, { key: "z", metaKey: true });
  await act(
    () =>
      new Promise<void>((resolve) => requestAnimationFrame(() => resolve())),
  );
  expect(input).toHaveValue("原文");
  expect(input.selectionStart).toBe(1);
});
