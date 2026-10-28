import { afterEach, expect, it, vi } from "vitest";
import {
  credentialFetch,
  registerCredentialConfirmation,
} from "./credentialConfirmation";

let unregister: (() => void) | undefined;
afterEach(() => {
  unregister?.();
  unregister = undefined;
});
function failure(code = "REAUTHENTICATION_REQUIRED") {
  return new Response(
    JSON.stringify({
      error: {
        code,
        message: "rejected",
        details: {},
        request_id: "req_confirmation",
      },
    }),
    { status: 403, headers: { "Content-Type": "application/json" } },
  );
}
it("does not prompt for background reads, unload cleanup, or unrelated failures", async () => {
  const confirm = vi.fn(async () => true);
  unregister = registerCredentialConfirmation(confirm);
  for (const init of [{ method: "GET" }, { method: "POST", keepalive: true }]) {
    const request = vi.fn(async () => failure());
    expect((await credentialFetch("/operation", init, request)).status).toBe(
      403,
    );
    expect(request).toHaveBeenCalledOnce();
  }
  const request = vi.fn(async () => failure("LAST_CREDENTIAL"));
  await expect(
    credentialFetch("/operation", { method: "POST" }, request),
  ).rejects.toMatchObject({ code: "LAST_CREDENTIAL" });
  expect(request).toHaveBeenCalledOnce();
  expect(confirm).not.toHaveBeenCalled();
});
it("retries at most once when the server challenges again", async () => {
  const confirm = vi.fn(async () => true),
    request = vi.fn(async () => failure());
  unregister = registerCredentialConfirmation(confirm);
  const response = await credentialFetch(
    "/operation",
    { method: "POST" },
    request,
  );
  expect(response.status).toBe(403);
  expect(confirm).toHaveBeenCalledOnce();
  expect(request).toHaveBeenCalledTimes(2);
});
it("leaves the request unexecuted when confirmation is dismissed", async () => {
  const confirm = vi.fn(async () => false),
    request = vi.fn(async () => failure());
  unregister = registerCredentialConfirmation(confirm);
  expect(
    (await credentialFetch("/operation", { method: "DELETE" }, request)).status,
  ).toBe(403);
  expect(request).toHaveBeenCalledOnce();
});
