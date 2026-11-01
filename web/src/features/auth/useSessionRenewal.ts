import { useEffect, useRef } from "react";
import { authApi, type AuthSession } from "../../api/auth";

const idleLifetime = 30 * 24 * 60 * 60 * 1000;
const renewalInterval = 5 * 60 * 1000;

// Only foreground visits and user input count as activity. No timer renews an
// unattended tab, and notification polling never reaches this endpoint.
export function useSessionRenewal(
  session: AuthSession | null,
  enabled: boolean,
  renewed: (expiresAt: string) => void,
  failed: (error: unknown) => void,
) {
  const attempt = useRef({ csrfToken: "", at: Number.NEGATIVE_INFINITY });
  useEffect(() => {
    if (!session || !enabled) return;
    const controller = new AbortController();
    let pending = false;
    if (attempt.current.csrfToken !== session.csrfToken) {
      attempt.current = {
        csrfToken: session.csrfToken,
        at: Number.NEGATIVE_INFINITY,
      };
    }
    const renew = () => {
      const now = Date.now();
      if (
        document.visibilityState === "hidden" ||
        pending ||
        now - attempt.current.at < renewalInterval ||
        Date.parse(session.expiresAt) - now > idleLifetime - renewalInterval
      )
        return;
      pending = true;
      attempt.current.at = now;
      void authApi
        .renewSession(session.csrfToken, controller.signal)
        .then((expiresAt) => {
          if (!controller.signal.aborted) renewed(expiresAt);
        })
        .catch((error: unknown) => {
          if (!controller.signal.aborted) failed(error);
        })
        .finally(() => {
          pending = false;
        });
    };
    const events = ["focus", "pointerdown", "keydown", "scroll"] as const;
    for (const event of events)
      window.addEventListener(event, renew, { passive: true });
    document.addEventListener("visibilitychange", renew);
    renew();
    return () => {
      controller.abort();
      for (const event of events) window.removeEventListener(event, renew);
      document.removeEventListener("visibilitychange", renew);
    };
  }, [session, enabled, renewed, failed]);
}
