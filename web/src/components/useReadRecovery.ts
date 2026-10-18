import { useEffect, useRef } from "react";
import { PublicApiError } from "../api/public";

export function canRecoverRead(error: unknown): boolean {
  if (error instanceof PublicApiError) {
    return (
      error.code === "API_RESPONSE_INVALID" ||
      (error.code === "INVALID_ARGUMENT" && error.details.field === "cursor") ||
      [408, 425, 429].includes(error.status) ||
      error.status >= 500
    );
  }
  return error instanceof Error && error.name !== "AbortError";
}

/** Retries reads while their surface is visible; mutations remain deliberate. */
export function useReadRecovery({
  error,
  loading,
  onRetry,
  enabled = true,
  scope = "",
}: {
  error: unknown;
  loading: boolean;
  onRetry: () => void | Promise<unknown>;
  enabled?: boolean;
  scope?: string;
}): boolean {
  const retry = useRef(onRetry);
  retry.current = onRetry;
  const attempts = useRef(0);
  useEffect(() => {
    attempts.current = 0;
  }, [scope]);
  useEffect(() => {
    if (!enabled || loading) return;
    if (!error) {
      attempts.current = 0;
      return;
    }
    if (!canRecoverRead(error)) return;
    const backoff = Math.min(30_000, 500 * 3 ** attempts.current);
    const notBefore =
      Date.now() +
      (error instanceof PublicApiError
        ? (error.retryAfterSeconds ?? 0) * 1_000
        : 0);
    let timer = 0;
    let triggered = false;
    const visible = () =>
      document.visibilityState !== "hidden" && navigator.onLine !== false;
    const queue = (immediate = false) => {
      window.clearTimeout(timer);
      if (triggered || !visible()) return;
      timer = window.setTimeout(
        () => {
          if (triggered || !visible()) return;
          triggered = true;
          attempts.current += 1;
          void retry.current();
        },
        Math.max(immediate ? 0 : backoff, notBefore - Date.now()),
      );
    };
    const resume = () => queue(true);
    queue();
    window.addEventListener("online", resume);
    window.addEventListener("offline", resume);
    window.addEventListener("focus", resume);
    document.addEventListener("visibilitychange", resume);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("online", resume);
      window.removeEventListener("offline", resume);
      window.removeEventListener("focus", resume);
      document.removeEventListener("visibilitychange", resume);
    };
  }, [enabled, error, loading, scope]);
  return enabled && canRecoverRead(error);
}
