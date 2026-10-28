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
import styles from "./Feedback.module.css";

afterEach(() => {
  act(() => feedback.close());
  cleanup();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function renderFeedback() {
  render(
    <FeedbackProvider>
      <button type="button">页面操作</button>
    </FeedbackProvider>,
  );
}

describe("Feedback", () => {
  it("honors a short timeout for a corner confirmation without changing defaults", () => {
    vi.useFakeTimers();
    renderFeedback();
    act(() => {
      notify("身份验证成功", { placement: "corner", timeout: 1800 });
      notify("设置已保存");
    });
    const confirmed = screen.getByRole("dialog", { name: "身份验证成功" });
    const ordinary = screen.getByRole("dialog", { name: "设置已保存" });
    act(() => vi.advanceTimersByTime(1799));
    expect(confirmed).not.toHaveAttribute("data-ending-style");
    act(() => vi.advanceTimersByTime(1));
    expect(confirmed).toHaveAttribute("data-ending-style");
    expect(ordinary).not.toHaveAttribute("data-ending-style");
  });

  it("limits glass styling and the shorter timeout to login notifications", () => {
    vi.useFakeTimers();
    renderFeedback();
    act(() => {
      notify("普通错误", { type: "error" });
      notify("登录错误", { type: "error", placement: "login" });
    });
    const ordinary = screen
      .getByText("普通错误", { selector: "h2" })
      .closest('[role="alertdialog"]')!;
    const login = screen
      .getByText("登录错误", { selector: "h2" })
      .closest('[role="alertdialog"]')!;
    expect(login).toHaveClass(styles.loginToast);
    expect(ordinary).not.toHaveClass(styles.loginToast);
    expect(login.querySelector(`.${styles.loginGlass}`)).not.toBeNull();
    expect(ordinary.querySelector(`.${styles.loginGlass}`)).toBeNull();
    act(() => vi.advanceTimersByTime(3_000));
    expect(login).toHaveAttribute("data-ending-style");
    expect(ordinary).not.toHaveAttribute("data-ending-style");
    act(() => vi.advanceTimersByTime(3_000));
    expect(ordinary).toHaveAttribute("data-ending-style");
  });

  it("updates one toast and restarts its timeout when its ID is reused", () => {
    vi.useFakeTimers();
    renderFeedback();
    act(() =>
      notify("第一次失败", {
        id: "login-retry",
        type: "error",
        placement: "login",
      }),
    );
    act(() => vi.advanceTimersByTime(2_000));
    act(() =>
      notify("第二次失败", {
        id: "login-retry",
        type: "error",
        placement: "login",
      }),
    );
    expect(screen.getAllByRole("alertdialog", { hidden: true })).toHaveLength(
      1,
    );
    const toast = document.querySelector<HTMLElement>('[role="alertdialog"]')!;
    act(() => vi.advanceTimersByTime(2_999));
    expect(toast).not.toHaveAttribute("data-ending-style");
    act(() => vi.advanceTimersByTime(1));
    expect(toast).toHaveAttribute("data-ending-style");
  });

  it("tracks the visible viewport for login feedback without moving focus", () => {
    const viewport = Object.assign(new EventTarget(), {
      offsetTop: 0,
      offsetLeft: 0,
      width: 390,
    });
    vi.stubGlobal("visualViewport", viewport);
    vi.stubGlobal("innerWidth", 390);
    renderFeedback();
    const control = screen.getByRole("button", { name: "页面操作" });
    control.focus();
    act(() => notify("密码不正确", { type: "error", placement: "login" }));
    expect(control).toHaveFocus();
    viewport.offsetTop = 120;
    act(() => viewport.dispatchEvent(new Event("resize")));
    const region = screen.getByRole("region", { name: "Notifications" });
    expect(region.style.getPropertyValue("--feedback-visual-top")).toBe(
      "120px",
    );
    expect(region.style.getPropertyValue("--feedback-visual-width")).toBe(
      "390px",
    );
  });

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
