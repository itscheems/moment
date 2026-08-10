import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";

import { OwnerApi, type OwnerSession } from "../../api/owner";
import { PublicApiError } from "../../api/public";
import { clearComposeDraft } from "./draft";

const SESSION_KEY = "moment.owner.session.v1";

export type OwnerState = "checking" | "guest" | "owner" | "unavailable";

interface OwnerContextValue {
  api: OwnerApi;
  state: OwnerState;
  session: OwnerSession | null;
  login: (token: string) => Promise<void>;
  logout: (instanceId: string | null) => Promise<void>;
  expireIfUnauthorized: (error: unknown) => boolean;
  retryStatus: () => void;
}

const OwnerContext = createContext<OwnerContextValue | null>(null);

function readSession(): OwnerSession | null {
  try {
    const raw = window.sessionStorage.getItem(SESSION_KEY);
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (
      typeof value !== "object" ||
      value === null ||
      Array.isArray(value) ||
      typeof (value as Record<string, unknown>).csrf_token !== "string" ||
      typeof (value as Record<string, unknown>).expires_at !== "string"
    ) {
      throw new Error("invalid owner session");
    }
    const expiresAt = (value as Record<string, unknown>).expires_at as string;
    const expiresTime = Date.parse(expiresAt);
    if (!Number.isFinite(expiresTime) || expiresTime <= Date.now()) {
      throw new Error("expired owner session");
    }
    return {
      csrfToken: (value as Record<string, unknown>).csrf_token as string,
      expiresAt,
    };
  } catch {
    try {
      window.sessionStorage.removeItem(SESSION_KEY);
    } catch {
      // Session storage is an optional convenience; cookie auth still fails closed.
    }
    return null;
  }
}

function writeSession(session: OwnerSession | null): void {
  try {
    if (!session) {
      window.sessionStorage.removeItem(SESSION_KEY);
      return;
    }
    window.sessionStorage.setItem(
      SESSION_KEY,
      JSON.stringify({
        csrf_token: session.csrfToken,
        expires_at: session.expiresAt,
      }),
    );
  } catch {
    // The in-memory session remains usable for this page when storage is denied.
  }
}

export function OwnerProvider({ children }: { children: ReactNode }) {
  const api = useMemo(() => new OwnerApi(), []);
  const [session, setSession] = useState<OwnerSession | null>(readSession);
  const [state, setState] = useState<OwnerState>("checking");
  const [statusAttempt, setStatusAttempt] = useState(0);

  const forgetSession = useCallback(() => {
    writeSession(null);
    setSession(null);
    setState("guest");
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setState("checking");
    void api
      .status(controller.signal)
      .then(() => {
        if (!controller.signal.aborted) setState(session ? "owner" : "guest");
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof PublicApiError && error.status === 401) {
          forgetSession();
        } else {
          setState("unavailable");
        }
      });
    return () => controller.abort();
  }, [api, forgetSession, session, statusAttempt]);

  const login = useCallback(
    async (token: string) => {
      const next = await api.exchange(token);
      writeSession(next);
      setSession(next);
      setState("owner");
    },
    [api],
  );

  const logout = useCallback(
    async (instanceId: string | null) => {
      if (!session) {
        forgetSession();
        clearComposeDraft(instanceId);
        return;
      }
      try {
        await api.logout(session.csrfToken);
      } catch (error) {
        if (!(error instanceof PublicApiError && error.status === 401)) {
          throw error;
        }
      }
      forgetSession();
      clearComposeDraft(instanceId);
    },
    [api, forgetSession, session],
  );

  const expireIfUnauthorized = useCallback(
    (error: unknown) => {
      if (error instanceof PublicApiError && error.status === 401) {
        forgetSession();
        return true;
      }
      return false;
    },
    [forgetSession],
  );

  const value = useMemo<OwnerContextValue>(
    () => ({
      api,
      state,
      session,
      login,
      logout,
      expireIfUnauthorized,
      retryStatus: () => setStatusAttempt((value) => value + 1),
    }),
    [api, expireIfUnauthorized, login, logout, session, state],
  );

  return (
    <OwnerContext.Provider value={value}>{children}</OwnerContext.Provider>
  );
}

export function useOwner(): OwnerContextValue {
  const value = useContext(OwnerContext);
  if (!value) throw new Error("useOwner must be used within OwnerProvider");
  return value;
}
