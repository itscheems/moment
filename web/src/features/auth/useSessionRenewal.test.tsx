import {
  act,
  cleanup,
  fireEvent,
  render,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useSessionRenewal } from "./useSessionRenewal";

const day = 24 * 60 * 60 * 1000;
let now: number;
let expiry: string;
let renewed: ReturnType<typeof vi.fn<(expiresAt: string) => void>>;
let failed: ReturnType<typeof vi.fn<(error: unknown) => void>>;
let fetcher: ReturnType<typeof vi.fn>;
function Probe({ token = "csrf-current" }: { token?: string }) {
  useSessionRenewal(
    { csrfToken: token, expiresAt: expiry },
    true,
    renewed,
    failed,
  );
  return null;
}
beforeEach(() => {
  now = Date.parse("2026-11-01T00:00:00Z");
  vi.spyOn(Date, "now").mockImplementation(() => now);
  expiry = new Date(now + 10 * day).toISOString();
  renewed = vi.fn();
  failed = vi.fn();
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
  fetcher = vi.fn(
    async () =>
      new Response(
        JSON.stringify({
          data: { expires_at: new Date(now + 30 * day).toISOString() },
          meta: {},
        }),
        { status: 200 },
      ),
  );
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("renews a foreground visit with CSRF and throttles repeated input", async () => {
  render(<Probe />);
  await waitFor(() => expect(renewed).toHaveBeenCalledOnce());
  expect(fetcher.mock.calls[0][0]).toBe("/api/v1/account/session/renew");
  expect(
    new Headers(fetcher.mock.calls[0][1].headers).get("X-CSRF-Token"),
  ).toBe("csrf-current");
  fireEvent.keyDown(window, { key: "j" });
  fireEvent.scroll(window);
  expect(fetcher).toHaveBeenCalledTimes(1);
  now += 5 * 60 * 1000;
  fireEvent.scroll(window);
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
});

it("does not renew hidden or unattended tabs, then renews on return", async () => {
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
  render(<Probe />);
  fireEvent.keyDown(window, { key: "j" });
  now += 20 * day;
  await act(async () => {
    await Promise.resolve();
  });
  expect(fetcher).not.toHaveBeenCalled();
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
  fireEvent(document, new Event("visibilitychange"));
  await waitFor(() => expect(fetcher).toHaveBeenCalledOnce());
});

it("does not write again immediately after a fresh login", async () => {
  expiry = new Date(now + 30 * day).toISOString();
  render(<Probe />);
  fireEvent.pointerDown(window);
  expect(fetcher).not.toHaveBeenCalled();
  now += 6 * 60 * 1000;
  fireEvent.pointerDown(window);
  await waitFor(() => expect(fetcher).toHaveBeenCalledOnce());
});

it("ignores a late response after the active credential changes", async () => {
  let finish!: (value: Response) => void;
  fetcher.mockImplementationOnce(
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve;
      }),
  );
  const view = render(<Probe />);
  expiry = new Date(now + 30 * day).toISOString();
  view.rerender(<Probe token="csrf-other" />);
  await act(async () => {
    finish(
      new Response(JSON.stringify({ data: { expires_at: expiry }, meta: {} }), {
        status: 200,
      }),
    );
  });
  expect(renewed).not.toHaveBeenCalled();
});

it("reports invalid credentials without retrying in a loop", async () => {
  fetcher.mockResolvedValue(
    new Response(
      JSON.stringify({
        error: { code: "UNAUTHORIZED", message: "sign-in ended" },
        meta: {},
      }),
      { status: 401 },
    ),
  );
  render(<Probe />);
  await waitFor(() => expect(failed).toHaveBeenCalledOnce());
  fireEvent.pointerDown(window);
  expect(fetcher).toHaveBeenCalledOnce();
  expect(renewed).not.toHaveBeenCalled();
});

it("keeps throttling across expiry updates near the absolute deadline", async () => {
  const view = render(<Probe />);
  await waitFor(() => expect(renewed).toHaveBeenCalledOnce());
  expiry = new Date(now + day).toISOString();
  view.rerender(<Probe />);
  fireEvent.scroll(window);
  await act(async () => {
    await Promise.resolve();
  });
  expect(fetcher).toHaveBeenCalledOnce();
});
