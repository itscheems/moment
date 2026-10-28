import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { CredentialConfirmation } from "./CredentialConfirmation";

const auth = {
  account: { userId: "confirmation-account" },
  session: { csrfToken: "session-csrf" } as { csrfToken: string } | null,
  api: { loginProviders: async () => [] },
  expireIfInvalidSession: vi.fn(() => false),
};
vi.mock("../auth/SessionProvider", () => ({ useSession: () => auth }));

function json(data: unknown, status = 200) {
  return new Response(
    JSON.stringify(
      status === 200
        ? { data, meta: { request_id: "req_confirmation" } }
        : {
            error: {
              code: "REAUTHENTICATION_REQUIRED",
              message: "confirmation required",
              details: {},
              request_id: "req_confirmation",
            },
          },
    ),
    { status, headers: { "Content-Type": "application/json" } },
  );
}
function install(passwordEnabled: boolean, reject = false) {
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/v1/account/security")
        return json({
          methods: passwordEnabled ? ["password"] : ["token"],
          password_enabled: passwordEnabled,
        });
      if (path === "/api/v1/account/identities") return json([]);
      if (path === "/api/v1/account/reauthentication") {
        if (init?.method === "POST")
          return reject
            ? json(null, 403)
            : json({ expires_at: new Date(Date.now() + 300000).toISOString() });
        return json({ confirmed: false, expires_at: null });
      }
      throw new Error("unexpected request");
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
beforeEach(() => {
  auth.session = { csrfToken: "session-csrf" };
  window.localStorage.clear();
  window.sessionStorage.clear();
  auth.expireIfInvalidSession.mockClear();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("confirms a password with CSRF and keeps the secret out of browser storage", async () => {
  const fetcher = install(true);
  render(<CredentialConfirmation />);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "确认身份" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认身份" }));
  expect(
    await screen.findByRole("button", { name: "关闭身份确认" }),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /^关闭$/ }),
  ).not.toBeInTheDocument();
  const input = await screen.findByLabelText("当前密码");
  fireEvent.change(input, { target: { value: "current-private-password" } });
  fireEvent.submit(input.closest("form")!);
  await screen.findByRole("button", { name: "身份已确认" });
  const request = fetcher.mock.calls.find(
    ([, init]) => init?.method === "POST",
  );
  expect(request?.[0]).toBe("/api/v1/account/reauthentication");
  expect(JSON.parse(String(request?.[1]?.body))).toEqual({
    password: "current-private-password",
  });
  expect(new Headers(request?.[1]?.headers).get("X-CSRF-Token")).toBe(
    "session-csrf",
  );
  expect(window.localStorage.length).toBe(0);
  expect(window.sessionStorage.length).toBe(0);
  await waitFor(() =>
    expect(screen.queryByLabelText("当前密码")).not.toBeInTheDocument(),
  );
});

it("clears rejected proof and keeps the current session available", async () => {
  install(true, true);
  render(<CredentialConfirmation />);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "确认身份" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认身份" }));
  const input = await screen.findByLabelText("当前密码");
  fireEvent.change(input, { target: { value: "wrong-private-password" } });
  fireEvent.submit(input.closest("form")!);
  expect(await screen.findByRole("alert")).toHaveTextContent("凭据确认失败");
  expect(input).toHaveValue("");
  expect(auth.expireIfInvalidSession).toHaveBeenCalledWith(
    expect.objectContaining({ code: "REAUTHENTICATION_REQUIRED" }),
  );
});

it("uses an explicit token confirmation for a passwordless browser session", async () => {
  const fetcher = install(false);
  render(<CredentialConfirmation />);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "确认身份" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认身份" }));
  const input = await screen.findByLabelText("当前 API 令牌");
  fireEvent.change(input, { target: { value: "private-api-credential" } });
  fireEvent.submit(input.closest("form")!);
  await screen.findByRole("button", { name: "身份已确认" });
  const request = fetcher.mock.calls.find(
    ([, init]) => init?.method === "POST",
  );
  expect(JSON.parse(String(request?.[1]?.body))).toEqual({
    token: "private-api-credential",
  });
});

it("dismisses credential confirmation when its session is lost", async () => {
  install(true);
  const view = render(<CredentialConfirmation />);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "确认身份" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "确认身份" }));
  fireEvent.change(await screen.findByLabelText("当前密码"), {
    target: { value: "discard-on-expiry" },
  });
  auth.session = null;
  view.rerender(<CredentialConfirmation />);
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});
