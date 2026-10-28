import { decodeResponse, PublicApiError } from "./public";

type ConfirmationHandler = () => Promise<boolean>;
let handler: ConfirmationHandler | null = null;
let pending: Promise<boolean> | null = null;

export function registerCredentialConfirmation(next: ConfirmationHandler) {
  handler = next;
  return () => {
    if (handler === next) handler = null;
  };
}

function requestConfirmation(signal?: AbortSignal | null): Promise<boolean> {
  if (!handler || signal?.aborted) return Promise.resolve(false);
  if (!pending) {
    const current = handler();
    pending = current;
    void current.finally(() => {
      if (pending === current) pending = null;
    });
  }
  const confirmation = pending;
  return new Promise((resolve) => {
    const abort = () => resolve(false);
    signal?.addEventListener("abort", abort, { once: true });
    void confirmation.then((confirmed) => {
      signal?.removeEventListener("abort", abort);
      resolve(confirmed && !signal?.aborted);
    });
  });
}

/** Retry only the server's explicit, pre-operation credential challenge. */
export async function withCredentialConfirmation<T>(
  operation: () => Promise<T>,
  signal?: AbortSignal | null,
): Promise<T> {
  try {
    return await operation();
  } catch (error) {
    if (
      !(error instanceof PublicApiError) ||
      error.code !== "REAUTHENTICATION_REQUIRED" ||
      !(await requestConfirmation(signal))
    )
      throw error;
    return operation();
  }
}

export async function credentialFetch(
  input: RequestInfo | URL,
  init?: RequestInit,
  fetcher: typeof window.fetch = (input, init) => window.fetch(input, init),
): Promise<Response> {
  // Keep background reads and page entry free of credential prompts.
  if (
    ["GET", "HEAD"].includes(init?.method?.toUpperCase() ?? "GET") ||
    init?.keepalive
  )
    return fetcher(input, init);
  let challenged: Response | null = null;
  try {
    return await withCredentialConfirmation(async () => {
      const response = await fetcher(input, init);
      if (response.status === 403) {
        challenged = response;
        await decodeResponse(response.clone());
      }
      return response;
    }, init?.signal);
  } catch (error) {
    if (
      challenged &&
      error instanceof PublicApiError &&
      error.code === "REAUTHENTICATION_REQUIRED"
    )
      return challenged;
    throw error;
  }
}

/** Native downloads and OAuth navigation cannot replay a fetch response. */
export async function ensureCredentialConfirmation(): Promise<boolean> {
  const response = await window.fetch("/api/v1/account/reauthentication", {
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  const { data } = await decodeResponse(response);
  return data &&
    typeof data === "object" &&
    "confirmed" in data &&
    data.confirmed === true
    ? true
    : requestConfirmation();
}
