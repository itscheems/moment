import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { PublicApiError } from "../../api/public";
import { FeedbackProvider, feedback, notify } from "../../components/Feedback";
import feedbackStyles from "../../components/Feedback.module.css";
import { PublicError, ReadRecoveryToast } from "./PublicStates";

afterEach(() => {
  act(() => feedback.close());
  cleanup();
  vi.clearAllTimers();
  vi.useRealTimers();
});

it("keeps background recovery outside the reading flow until it ends", async () => {
  const page = (recovering: boolean) => (
    <FeedbackProvider>
      <section aria-label="Reading">
        <button type="button">Continue reading</button>
        {recovering ? <ReadRecoveryToast /> : null}
      </section>
    </FeedbackProvider>
  );
  const view = render(page(true));
  const control = screen.getByRole("button", { name: "Continue reading" });
  control.focus();
  const toast = await screen.findByRole("dialog", {
    name: "正在重新获取内容…",
  });
  expect(toast.closest('section[aria-label="Reading"]')).toBeNull();
  expect(toast).toHaveAttribute("aria-busy", "true");
  expect(toast.querySelector(`.${feedbackStyles.spinner}`)).not.toBeNull();
  expect(toast.closest(`.${feedbackStyles.cornerViewport}`)).not.toBeNull();
  expect(control).toHaveFocus();
  vi.useFakeTimers();
  act(() => vi.advanceTimersByTime(30_000));
  expect(toast).not.toHaveAttribute("data-ending-style");
  view.rerender(page(false));
  expect(toast).toHaveAttribute("data-ending-style");
});

it("allows dismissal without suppressing other notifications", async () => {
  render(
    <FeedbackProvider>
      <ReadRecoveryToast />
    </FeedbackProvider>,
  );
  const recovery = await screen.findByRole("dialog", {
    name: "正在重新获取内容…",
  });
  act(() => notify("Saved"));
  const ordinary = screen.getByRole("dialog", { name: "Saved" });
  expect(ordinary.closest(`.${feedbackStyles.cornerViewport}`)).toBeNull();
  fireEvent.click(recovery.querySelector("button")!);
  expect(recovery).toHaveAttribute("data-ending-style");
  expect(ordinary).not.toHaveAttribute("data-ending-style");
});

it("uses floating feedback even when the initial read is recovering", async () => {
  render(
    <FeedbackProvider>
      <section aria-label="Reading">
        <PublicError recovering />
      </section>
    </FeedbackProvider>,
  );
  expect(
    await screen.findByRole("dialog", { name: "正在重新获取内容…" }),
  ).toHaveAttribute("aria-busy", "true");
  expect(screen.getByRole("region", { name: "Reading" })).toBeEmptyDOMElement();
});

it("closes recovery feedback when a cached surface becomes inactive", async () => {
  const page = (active: boolean) => (
    <FeedbackProvider>
      <ReadRecoveryToast active={active} />
    </FeedbackProvider>
  );
  const view = render(page(false));
  expect(screen.queryByRole("dialog")).toBeNull();
  view.rerender(page(true));
  const toast = await screen.findByRole("dialog", {
    name: "正在重新获取内容…",
  });
  view.rerender(page(false));
  expect(toast).toHaveAttribute("data-ending-style");
});

it("reports a terminal read error without an inline row or busy indicator", async () => {
  render(
    <FeedbackProvider>
      <section aria-label="Reading">
        <ReadRecoveryToast
          error={
            new PublicApiError("Denied", { code: "FORBIDDEN", status: 403 })
          }
        />
      </section>
    </FeedbackProvider>,
  );
  const title = await screen.findByText("这部分内容暂时无法访问。", {
    selector: "h2",
  });
  const toast = title.closest('[role="alertdialog"]');
  expect(toast).not.toBeNull();
  expect(screen.getByRole("alert")).toHaveTextContent(
    "这部分内容暂时无法访问。",
  );
  expect(toast).not.toHaveAttribute("aria-busy");
  expect(screen.getByRole("region", { name: "Reading" })).toBeEmptyDOMElement();
});

it("retains an explicit retry action inside the notification", async () => {
  const retry = vi.fn();
  render(
    <FeedbackProvider>
      <ReadRecoveryToast onRetry={retry} />
    </FeedbackProvider>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "重试" }));
  expect(retry).toHaveBeenCalledTimes(1);
});
