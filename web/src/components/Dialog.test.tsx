import "@testing-library/jest-dom/vitest";
import { useRef } from "react";
import { afterEach, expect, it } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";

import { Dialog } from "./Dialog";

afterEach(cleanup);

function Harness({ input = false }: { input?: boolean }) {
  const field = useRef<HTMLInputElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  return (
    <Dialog.Root>
      <Dialog.Trigger>打开</Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Popup ref={popup} initialFocus={input ? field : undefined}>
          <Dialog.Title>测试窗口</Dialog.Title>
          <Dialog.Close>关闭</Dialog.Close>
          <input ref={field} aria-label="输入内容" />
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

it("opens without selecting a control and keeps upstream Escape and return focus", async () => {
  render(<Harness />);
  const trigger = screen.getByRole("button", { name: "打开" });
  trigger.focus();
  fireEvent.click(trigger);
  const popup = await screen.findByRole("dialog", { name: "测试窗口" });
  await waitFor(() => expect(popup).toHaveFocus());
  expect(screen.getByRole("button", { name: "关闭" })).not.toHaveFocus();
  fireEvent.keyDown(popup, { key: "Escape" });
  await waitFor(() => expect(popup).not.toBeInTheDocument());
  await waitFor(() => expect(trigger).toHaveFocus());
});

it("retains explicitly requested input focus", async () => {
  render(<Harness input />);
  fireEvent.click(screen.getByRole("button", { name: "打开" }));
  await waitFor(() => expect(screen.getByRole("textbox")).toHaveFocus());
});
