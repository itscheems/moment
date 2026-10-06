import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { CodeBlockCopy } from "./CodeBlockCopy";
import { CodeCopyFeedback } from "./CodeCopyFeedback";

afterEach(() => {
  act(() => toast.dismiss());
  cleanup();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function setup(writeText = vi.fn().mockResolvedValue(undefined)) {
  vi.useFakeTimers();
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  render(
    <>
      <CodeCopyFeedback />
      <CodeBlockCopy source={'const river = "<河流>&";\n  river();\n'} />
    </>,
  );
  return writeText;
}

it("copies exact text and resets its icon and independent toast after 1.2 seconds", async () => {
  const writeText = setup();
  const button = screen.getByRole("button", { name: "复制代码" });
  expect(button).toHaveTextContent("");
  await act(async () => fireEvent.click(button));
  await act(async () => vi.advanceTimersByTimeAsync(0));
  expect(writeText).toHaveBeenCalledWith(
    'const river = "<河流>&";\n  river();\n',
  );
  expect(button).toHaveAccessibleName("已复制");
  expect(button).toHaveAttribute("data-copied", "true");
  expect(screen.queryByRole("tooltip")).toBeNull();
  const notice = document.querySelector("[data-sonner-toast]");
  expect(notice).toHaveTextContent("已复制到剪贴板");
  expect(notice).not.toHaveAttribute("data-removed", "true");
  expect(notice?.querySelector("button")).toBeNull();

  await act(async () => vi.advanceTimersByTimeAsync(1_199));
  expect(button).toHaveAccessibleName("已复制");
  expect(notice).not.toHaveAttribute("data-removed", "true");
  await act(async () => vi.advanceTimersByTimeAsync(1));
  expect(button).toHaveAccessibleName("复制代码");
  expect(button).not.toHaveAttribute("data-copied");
  expect(notice).toHaveAttribute("data-removed", "true");
});

it("restarts native copied feedback on a second click without disabling the button", async () => {
  const writeText = setup();
  const button = screen.getByRole("button", { name: "复制代码" });
  await act(async () => fireEvent.click(button));
  await act(async () => vi.advanceTimersByTimeAsync(0));
  await act(async () => vi.advanceTimersByTimeAsync(900));
  expect(button).toBeEnabled();
  await act(async () => fireEvent.click(button));
  await act(async () => vi.advanceTimersByTimeAsync(0));
  expect(writeText).toHaveBeenCalledTimes(2);
  await act(async () => vi.advanceTimersByTimeAsync(1_199));
  expect(button).toHaveAccessibleName("已复制");
  const latest = document.querySelectorAll("[data-sonner-toast]");
  expect(latest[latest.length - 1]).not.toHaveAttribute("data-removed", "true");
  await act(async () => vi.advanceTimersByTimeAsync(1));
  expect(button).toHaveAccessibleName("复制代码");
});

it("replaces the copy notice with an error when the clipboard rejects the write", async () => {
  setup(vi.fn().mockRejectedValue(new Error("Clipboard denied")));
  const button = screen.getByRole("button", { name: "复制代码" });
  await act(async () => fireEvent.click(button));
  await act(async () => vi.advanceTimersByTimeAsync(50));
  expect(button).not.toHaveAttribute("data-copied");
  expect(screen.getByText("暂时无法复制代码")).toBeVisible();
  const success = screen
    .getByText("已复制到剪贴板")
    .closest("[data-sonner-toast]");
  expect(success).toHaveAttribute("data-removed", "true");
});
