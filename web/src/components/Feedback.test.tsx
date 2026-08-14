import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { FeedbackProvider, feedback, notify } from "./Feedback";

afterEach(() => {
  act(() => feedback.close());
  cleanup();
  vi.clearAllTimers();
  vi.useRealTimers();
});

function renderFeedback() {
  render(
    <FeedbackProvider>
      <button type="button">页面操作</button>
    </FeedbackProvider>,
  );
}

describe("Feedback", () => {
  it("offers an action and an explicit close control", () => {
    const onAction = vi.fn();
    renderFeedback();

    act(() => {
      notify("Moment 已发布", {
        action: { label: "查看 Moment", onAction },
      });
    });

    const toast = screen.getByRole("dialog", { name: "Moment 已发布" });
    expect(toast).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "查看 Moment" }));
    expect(onAction).toHaveBeenCalledTimes(1);
    expect(toast).toHaveAttribute("data-ending-style");

    act(() => notify("设置已保存"));
    const nextToast = screen.getByRole("dialog", { name: "设置已保存" });
    fireEvent.click(
      screen.getAllByRole("button", { name: "关闭通知" }).at(-1)!,
    );
    expect(nextToast).toHaveAttribute("data-ending-style");
  });

  it("keeps ordinary success visible for about four seconds", () => {
    vi.useFakeTimers();
    renderFeedback();
    act(() => notify("设置已保存"));
    const toast = screen.getByRole("dialog", { name: "设置已保存" });

    act(() => vi.advanceTimersByTime(3_999));
    expect(toast).not.toHaveAttribute("data-ending-style");
    act(() => vi.advanceTimersByTime(1));
    expect(toast).toHaveAttribute("data-ending-style");
  });

  it("keeps explanatory and error feedback visible for about six seconds", () => {
    vi.useFakeTimers();
    renderFeedback();
    act(() => {
      notify("没有保存", {
        description: "内容仍在，可以安全重试。",
        type: "error",
      });
    });
    const toast = document.querySelector('[role="alertdialog"]');
    expect(screen.getByRole("alert")).toHaveTextContent(
      "没有保存内容仍在，可以安全重试。",
    );

    act(() => vi.advanceTimersByTime(5_999));
    expect(toast).not.toHaveAttribute("data-ending-style");
    act(() => vi.advanceTimersByTime(1));
    expect(toast).toHaveAttribute("data-ending-style");
  });

  it("pauses the timeout while the viewport is hovered", () => {
    vi.useFakeTimers();
    renderFeedback();
    act(() => notify("悬停查看"));
    const toast = screen.getByRole("dialog", { name: "悬停查看" });
    const viewport = screen.getByRole("region", { name: "Notifications" });

    fireEvent.mouseEnter(viewport);
    fireEvent.mouseMove(viewport);
    act(() => vi.advanceTimersByTime(5_000));
    expect(toast).not.toHaveAttribute("data-ending-style");

    fireEvent.mouseLeave(viewport);
    act(() => vi.advanceTimersByTime(4_000));
    expect(toast).toHaveAttribute("data-ending-style");
  });

  it("pauses the timeout while a toast action has keyboard focus", () => {
    vi.useFakeTimers();
    renderFeedback();
    act(() => {
      notify("可聚焦操作", {
        action: { label: "查看 Moment", onAction: vi.fn() },
      });
    });
    const toast = screen.getByRole("dialog", { name: "可聚焦操作" });
    const action = screen.getByRole("button", { name: "查看 Moment" });

    fireEvent.keyDown(document, { key: "Tab" });
    action.focus();
    expect(action).toHaveFocus();
    act(() => vi.advanceTimersByTime(5_000));
    expect(toast).not.toHaveAttribute("data-ending-style");
  });
});
