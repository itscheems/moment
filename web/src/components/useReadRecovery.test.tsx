import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PublicApiError } from "../api/public";
import { useReadRecovery } from "./useReadRecovery";

type State = {
  error: unknown;
  loading: boolean;
  enabled?: boolean;
  scope?: string;
};
function mount(initialProps: State) {
  const onRetry = vi.fn();
  const hook = renderHook(
    (state: State) => useReadRecovery({ ...state, onRetry }),
    { initialProps },
  );
  return { ...hook, onRetry };
}
beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
  vi.spyOn(navigator, "onLine", "get").mockReturnValue(true);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

it("retries reads with backoff, pauses in flight, and resets after success", () => {
  const { onRetry, rerender } = mount({
    error: new TypeError("offline"),
    loading: false,
  });
  act(() => vi.advanceTimersByTime(500));
  expect(onRetry).toHaveBeenCalledTimes(1);
  rerender({ error: null, loading: true });
  act(() => vi.advanceTimersByTime(30_000));
  expect(onRetry).toHaveBeenCalledTimes(1);
  rerender({ error: new TypeError("still offline"), loading: false });
  act(() => vi.advanceTimersByTime(500));
  expect(onRetry).toHaveBeenCalledTimes(1);
  act(() => vi.advanceTimersByTime(1_000));
  expect(onRetry).toHaveBeenCalledTimes(2);
  rerender({ error: null, loading: false });
  act(() => vi.advanceTimersByTime(30_000));
  expect(onRetry).toHaveBeenCalledTimes(2);
  rerender({ error: new TypeError("a later interruption"), loading: false });
  act(() => vi.advanceTimersByTime(500));
  expect(onRetry).toHaveBeenCalledTimes(3);
});

it("waits while hidden and resumes when the reader returns", () => {
  const visibility = vi
    .spyOn(document, "visibilityState", "get")
    .mockReturnValue("hidden");
  const { onRetry } = mount({
    error: new TypeError("offline"),
    loading: false,
  });
  act(() => vi.advanceTimersByTime(60_000));
  expect(onRetry).not.toHaveBeenCalled();
  visibility.mockReturnValue("visible");
  act(() => {
    document.dispatchEvent(new Event("visibilitychange"));
    vi.advanceTimersByTime(0);
  });
  expect(onRetry).toHaveBeenCalledTimes(1);
});

it("resumes on reconnection and coalesces simultaneous wake-up events", () => {
  const online = vi.spyOn(navigator, "onLine", "get").mockReturnValue(false);
  const { onRetry } = mount({
    error: new TypeError("offline"),
    loading: false,
  });
  act(() => vi.advanceTimersByTime(60_000));
  expect(onRetry).not.toHaveBeenCalled();
  online.mockReturnValue(true);
  act(() => {
    window.dispatchEvent(new Event("online"));
    window.dispatchEvent(new Event("focus"));
    vi.advanceTimersByTime(0);
  });
  expect(onRetry).toHaveBeenCalledTimes(1);
});

it("respects server rate-limit delays even when focused", () => {
  const { onRetry } = mount({
    error: new PublicApiError("wait", {
      code: "RATE_LIMITED",
      status: 429,
      retryAfterSeconds: 10,
    }),
    loading: false,
  });
  act(() => {
    vi.advanceTimersByTime(500);
    window.dispatchEvent(new Event("focus"));
    vi.advanceTimersByTime(0);
  });
  expect(onRetry).not.toHaveBeenCalled();
  act(() => vi.advanceTimersByTime(9_500));
  expect(onRetry).toHaveBeenCalledTimes(1);
});

it.each([401, 403, 404, 400])(
  "does not retry authorization, missing resources, or invalid input (%s)",
  (status) => {
    const { onRetry } = mount({
      error: new PublicApiError("not recoverable", {
        code: "INVALID_ARGUMENT",
        status,
        details: { field: "from" },
      }),
      loading: false,
    });
    act(() => {
      window.dispatchEvent(new Event("focus"));
      vi.advanceTimersByTime(60_000);
    });
    expect(onRetry).not.toHaveBeenCalled();
  },
);

it("cancels scheduled recovery when disabled or unmounted", () => {
  const error = new TypeError("offline");
  const { onRetry, rerender, unmount } = mount({ error, loading: false });
  rerender({ error, loading: false, enabled: false });
  act(() => vi.advanceTimersByTime(60_000));
  expect(onRetry).not.toHaveBeenCalled();
  rerender({ error, loading: false, enabled: true });
  unmount();
  act(() => vi.advanceTimersByTime(60_000));
  expect(onRetry).not.toHaveBeenCalled();
});
