import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { CredentialConfirmation } from "./CredentialConfirmation";
import { credentialFetch } from "../../api/credentialConfirmation";
import { notify } from "../../components/Feedback";
import { accountsApi } from "../../api/accounts";

const auth = {
  account: { userId: "confirmation-account" },
  session: { csrfToken: "session-csrf" } as { csrfToken: string } | null,
  api: {
    loginProviders: async () => [] as Array<{ name: string; label: string }>,
  },
  expireIfInvalidSession: vi.fn(() => false),
};
vi.mock("../auth/SessionProvider", () => ({ useSession: () => auth }));
vi.mock("../../components/Feedback", () => ({ notify: vi.fn() }));
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
  let confirmed = false;
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/protected")
        return confirmed ? json({ saved: true }) : json(null, 403);
      if (path === "/api/v1/account/security")
        return json({
          methods: passwordEnabled ? ["password"] : ["token"],
          password_enabled: passwordEnabled,
        });
      if (path === "/api/v1/account/identities") return json([]);
      if (path === "/api/v1/account/reauthentication") {
        if (init?.method !== "POST") {
          confirmed = true;
          return json({
            confirmed: true,
            expires_at: new Date(Date.now() + 3600000).toISOString(),
          });
        }
        if (reject) return json(null, 403);
        confirmed = true;
        return json({
          expires_at: new Date(Date.now() + 3600000).toISOString(),
        });
      }
      throw new Error("unexpected request: " + path);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
function action(signal?: AbortSignal) {
  return credentialFetch("/protected", {
    method: "POST",
    body: "unchanged-action",
    signal,
  });
}
function startAction(signal?: AbortSignal) {
  let result!: Promise<Response>;
  act(() => {
    result = action(signal);
  });
  return result;
}
beforeEach(() => {
  auth.session = { csrfToken: "session-csrf" };
  window.localStorage.clear();
  window.sessionStorage.clear();
  vi.clearAllMocks();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("keeps the original operation while confirming through a linked provider", async () => {
  install(false);
  vi.spyOn(auth.api, "loginProviders").mockResolvedValue([
    { name: "github", label: "GitHub" },
  ]);
  vi.spyOn(accountsApi, "accountIdentities").mockResolvedValue([
    {
      provider: "github",
      displayName: "",
      email: "",
      linkedAt: "2026-10-28T00:00:00Z",
      lastLoginAt: "2026-10-28T00:00:00Z",
    },
  ]);
  const popup = vi.spyOn(window, "open").mockReturnValue(window);
  const close = vi.spyOn(window, "close").mockImplementation(() => undefined);
  render(<CredentialConfirmation />);
  const result = startAction();
  fireEvent.click(
    await screen.findByRole("button", { name: "使用 GitHub 验证身份" }),
  );
  expect((await result).ok).toBe(true);
  expect(popup).toHaveBeenCalledWith(
    expect.stringContaining("confirm=1"),
    "moment-credential-confirmation",
    "popup,width=520,height=720",
  );
  expect(close).toHaveBeenCalled();
  expect(notify).toHaveBeenCalledWith(
    "身份验证成功",
    expect.objectContaining({ placement: "corner", timeout: 1800 }),
  );
});

it("opens only on demand, retries the same operation, and announces success for 1.8 seconds", async () => {
  const fetcher = install(true);
  render(<CredentialConfirmation />);
  expect(screen.queryByRole("button", { name: "确认身份" })).toBeNull();
  expect(fetcher).not.toHaveBeenCalled();
  const result = startAction();
  const input = await screen.findByLabelText("当前密码");
  await waitFor(() =>
    expect(screen.getByText(/1 小时无需重复输入/)).toBeVisible(),
  );
  fireEvent.change(input, { target: { value: "current-private-password" } });
  fireEvent.submit(input.closest("form")!);
  expect((await result).ok).toBe(true);
  expect(notify).toHaveBeenCalledWith("身份验证成功", {
    id: "credential-confirmation",
    placement: "corner",
    timeout: 1800,
  });
  const request = fetcher.mock.calls.find(
    ([path]) => path === "/api/v1/account/reauthentication",
  );
  expect(JSON.parse(String(request?.[1]?.body))).toEqual({
    password: "current-private-password",
  });
  expect(new Headers(request?.[1]?.headers).get("X-CSRF-Token")).toBe(
    "session-csrf",
  );
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/protected"),
  ).toHaveLength(2);
  await act(async () => {
    expect((await action()).ok).toBe(true);
  });
  expect(
    fetcher.mock.calls.filter(
      ([path]) => path === "/api/v1/account/reauthentication",
    ),
  ).toHaveLength(1);
  expect(window.localStorage.length).toBe(0);
  expect(window.sessionStorage.length).toBe(0);
  await waitFor(() => expect(screen.queryByLabelText("当前密码")).toBeNull());
});

it("clears wrong proof without retrying the operation, and cancellation leaves it unexecuted", async () => {
  const fetcher = install(true, true);
  render(<CredentialConfirmation />);
  const result = startAction();
  const input = await screen.findByLabelText("当前密码");
  fireEvent.change(input, { target: { value: "wrong-private-password" } });
  fireEvent.submit(input.closest("form")!);
  expect(await screen.findByRole("alert")).toHaveTextContent("验证失败");
  expect(input).toHaveValue("");
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/protected"),
  ).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "关闭身份确认" }));
  expect((await result).status).toBe(403);
  expect(notify).not.toHaveBeenCalled();
});

it("accepts token proof for passwordless accounts", async () => {
  const fetcher = install(false);
  render(<CredentialConfirmation />);
  const result = startAction();
  const input = await screen.findByLabelText("当前 API 令牌");
  fireEvent.change(input, { target: { value: "private-api-credential" } });
  fireEvent.submit(input.closest("form")!);
  expect((await result).ok).toBe(true);
  const request = fetcher.mock.calls.find(
    ([path]) => path === "/api/v1/account/reauthentication",
  );
  expect(JSON.parse(String(request?.[1]?.body))).toEqual({
    token: "private-api-credential",
  });
});

it("shares one prompt between concurrent operations and does not resume an aborted request", async () => {
  const fetcher = install(true);
  render(<CredentialConfirmation />);
  const controller = new AbortController();
  const first = startAction(controller.signal),
    second = startAction();
  const input = await screen.findByLabelText("当前密码");
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  controller.abort();
  expect((await first).status).toBe(403);
  fireEvent.change(input, { target: { value: "current-private-password" } });
  fireEvent.submit(input.closest("form")!);
  expect((await second).ok).toBe(true);
  expect(
    fetcher.mock.calls.filter(([path]) => path === "/protected"),
  ).toHaveLength(3);
});

it("cancels pending actions when the session is lost", async () => {
  install(true);
  const view = render(<CredentialConfirmation />);
  const result = startAction();
  fireEvent.change(await screen.findByLabelText("当前密码"), {
    target: { value: "discard-on-expiry" },
  });
  auth.session = null;
  view.rerender(<CredentialConfirmation />);
  expect((await result).status).toBe(403);
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});
