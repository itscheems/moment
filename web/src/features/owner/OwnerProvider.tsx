import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { OwnerApi, type OwnerSession } from "../../api/owner";
import { PublicApiError } from "../../api/public";
import { clearComposeDraft } from "./draft";

const SESSION_KEY = "moment.owner.session.v1";
const SESSION_CHANNEL = "moment.owner.session.sync.v1";
const VERSION_PATTERN = /^[a-z0-9-]{8,80}$/;
const REQUEST_PATTERN = /^[a-z0-9-]{8,80}$/;

export interface BrowserOwnerSession extends OwnerSession {
  issuedAt: number;
  version: string;
}

interface SessionMarker {
  issuedAt: number;
  version: string;
}

type SyncMessage =
  | { type: "request-session"; requestId: string }
  | {
      type: "offer-session";
      requestId: string | null;
      session: BrowserOwnerSession;
    }
  | { type: "clear-session"; marker: SessionMarker };

export type OwnerState =
  "checking" | "guest" | "owner" | "expired" | "unavailable";

interface OwnerContextValue {
  api: OwnerApi;
  state: OwnerState;
  session: BrowserOwnerSession | null;
  login: (token: string) => Promise<void>;
  logout: (instanceId: string | null) => Promise<void>;
  expireIfInvalidSession: (error: unknown) => boolean;
  retryStatus: () => void;
}

const OwnerContext = createContext<OwnerContextValue | null>(null);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(
  record: Record<string, unknown>,
  keys: string[],
): boolean {
  const actual = Object.keys(record).sort();
  const expected = [...keys].sort();
  return (
    actual.length === expected.length &&
    actual.every((value, index) => value === expected[index])
  );
}

function markerOf(session: BrowserOwnerSession | null): SessionMarker {
  return session
    ? { issuedAt: session.issuedAt, version: session.version }
    : { issuedAt: 0, version: "legacy" };
}

function compareMarker(left: SessionMarker, right: SessionMarker): number {
  if (left.issuedAt !== right.issuedAt) return left.issuedAt - right.issuedAt;
  return left.version.localeCompare(right.version);
}

function isValidExpiry(value: string): boolean {
  const expiresAt = Date.parse(value);
  return Number.isFinite(expiresAt) && expiresAt > Date.now();
}

function parseSession(value: unknown): BrowserOwnerSession | null {
  if (!isRecord(value)) return null;
  const keys = Object.keys(value);
  const legacy = !keys.includes("issued_at") && !keys.includes("version");
  if (
    !hasExactKeys(
      value,
      legacy
        ? ["csrf_token", "expires_at"]
        : ["csrf_token", "expires_at", "issued_at", "version"],
    ) ||
    typeof value.csrf_token !== "string" ||
    value.csrf_token.length === 0 ||
    typeof value.expires_at !== "string" ||
    !isValidExpiry(value.expires_at)
  ) {
    return null;
  }
  if (legacy) {
    return {
      csrfToken: value.csrf_token,
      expiresAt: value.expires_at,
      issuedAt: 0,
      version: "legacy",
    };
  }
  if (
    !Number.isSafeInteger(value.issued_at) ||
    Number(value.issued_at) < 0 ||
    Number(value.issued_at) > Date.now() + 60_000 ||
    typeof value.version !== "string" ||
    !VERSION_PATTERN.test(value.version)
  ) {
    return null;
  }
  return {
    csrfToken: value.csrf_token,
    expiresAt: value.expires_at,
    issuedAt: Number(value.issued_at),
    version: value.version,
  };
}

function parseMarker(value: unknown): SessionMarker | null {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["issued_at", "version"]) ||
    !Number.isSafeInteger(value.issued_at) ||
    Number(value.issued_at) < 0 ||
    Number(value.issued_at) > Date.now() + 60_000 ||
    typeof value.version !== "string" ||
    !VERSION_PATTERN.test(value.version)
  ) {
    return null;
  }
  return { issuedAt: Number(value.issued_at), version: value.version };
}

function parseSyncMessage(value: unknown): SyncMessage | null {
  if (!isRecord(value) || typeof value.type !== "string") return null;
  if (value.type === "request-session") {
    return hasExactKeys(value, ["type", "request_id"]) &&
      typeof value.request_id === "string" &&
      REQUEST_PATTERN.test(value.request_id)
      ? { type: value.type, requestId: value.request_id }
      : null;
  }
  if (value.type === "offer-session") {
    const session = parseSession(value.session);
    return hasExactKeys(value, ["type", "request_id", "session"]) &&
      (value.request_id === null ||
        (typeof value.request_id === "string" &&
          REQUEST_PATTERN.test(value.request_id))) &&
      session &&
      session.version !== "legacy"
      ? { type: value.type, requestId: value.request_id, session }
      : null;
  }
  if (value.type === "clear-session") {
    const marker = parseMarker(value.marker);
    return hasExactKeys(value, ["type", "marker"]) && marker
      ? { type: value.type, marker }
      : null;
  }
  return null;
}

function sessionWire(session: BrowserOwnerSession): Record<string, unknown> {
  return {
    csrf_token: session.csrfToken,
    expires_at: session.expiresAt,
    issued_at: session.issuedAt,
    version: session.version,
  };
}

function messageWire(message: SyncMessage): Record<string, unknown> {
  if (message.type === "request-session") {
    return { type: message.type, request_id: message.requestId };
  }
  if (message.type === "offer-session") {
    return {
      type: message.type,
      request_id: message.requestId,
      session: sessionWire(message.session),
    };
  }
  return {
    type: message.type,
    marker: {
      issued_at: message.marker.issuedAt,
      version: message.marker.version,
    },
  };
}

function readSession(): BrowserOwnerSession | null {
  try {
    const raw = window.sessionStorage.getItem(SESSION_KEY);
    if (!raw) return null;
    const session = parseSession(JSON.parse(raw));
    if (!session) throw new Error("invalid owner session");
    return session;
  } catch {
    try {
      window.sessionStorage.removeItem(SESSION_KEY);
    } catch {
      // Session storage is an optional convenience; cookie auth still fails closed.
    }
    return null;
  }
}

function writeSession(session: BrowserOwnerSession | null): void {
  try {
    if (!session) {
      window.sessionStorage.removeItem(SESSION_KEY);
      return;
    }
    window.sessionStorage.setItem(
      SESSION_KEY,
      JSON.stringify(sessionWire(session)),
    );
  } catch {
    // The in-memory session remains usable for this page when storage is denied.
  }
}

function newID(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (value) =>
    value.toString(16).padStart(2, "0"),
  ).join("");
}

function nextMarker(current: SessionMarker): SessionMarker {
  return {
    issuedAt: Math.max(Date.now(), current.issuedAt + 1),
    version: newID(),
  };
}

function isInvalidSessionError(error: unknown): boolean {
  return (
    error instanceof PublicApiError &&
    (error.status === 401 ||
      (error.status === 403 && error.code === "CSRF_INVALID"))
  );
}

export function OwnerProvider({ children }: { children: ReactNode }) {
  const api = useMemo(() => new OwnerApi(), []);
  const [session, setSession] = useState<BrowserOwnerSession | null>(
    readSession,
  );
  const [state, setState] = useState<OwnerState>("checking");
  const [statusAttempt, setStatusAttempt] = useState(0);
  const sessionRef = useRef(session);
  const markerRef = useRef(markerOf(session));
  const channelRef = useRef<BroadcastChannel | null>(null);

  const acceptSession = useCallback((next: BrowserOwnerSession) => {
    const marker = markerOf(next);
    if (compareMarker(marker, markerRef.current) <= 0) return false;
    markerRef.current = marker;
    sessionRef.current = next;
    writeSession(next);
    setSession(next);
    setState("owner");
    return true;
  }, []);

  const clearSession = useCallback(
    (nextState: "guest" | "expired", marker?: SessionMarker) => {
      if (marker && compareMarker(marker, markerRef.current) <= 0) return false;
      if (marker) markerRef.current = marker;
      sessionRef.current = null;
      writeSession(null);
      setSession(null);
      setState(nextState);
      return true;
    },
    [],
  );

  const broadcast = useCallback((message: SyncMessage) => {
    try {
      channelRef.current?.postMessage(messageWire(message));
    } catch {
      // Cross-tab repair is best effort; invalid sessions still fail closed.
    }
  }, []);

  const requestSyncedSession = useCallback(() => {
    broadcast({ type: "request-session", requestId: newID() });
  }, [broadcast]);

  useEffect(() => {
    if (typeof BroadcastChannel === "undefined") return;
    let channel: BroadcastChannel;
    try {
      channel = new BroadcastChannel(SESSION_CHANNEL);
    } catch {
      return;
    }
    channelRef.current = channel;
    const receive = (event: MessageEvent<unknown>) => {
      const message = parseSyncMessage(event.data);
      if (!message) return;
      if (message.type === "request-session") {
        const current = sessionRef.current;
        if (current) {
          broadcast({
            type: "offer-session",
            requestId: message.requestId,
            session: current,
          });
        }
      } else if (message.type === "offer-session") {
        acceptSession(message.session);
      } else {
        clearSession("guest", message.marker);
      }
    };
    channel.addEventListener("message", receive);
    requestSyncedSession();
    return () => {
      channel.removeEventListener("message", receive);
      channel.close();
      if (channelRef.current === channel) channelRef.current = null;
    };
  }, [acceptSession, broadcast, clearSession, requestSyncedSession]);

  useEffect(() => {
    const controller = new AbortController();
    const markerAtStart = markerRef.current;
    setState("checking");
    void api
      .status(controller.signal)
      .then(() => {
        if (!controller.signal.aborted) {
          setState(sessionRef.current ? "owner" : "guest");
        }
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof PublicApiError && error.status === 401) {
          if (
            sessionRef.current &&
            compareMarker(markerRef.current, markerAtStart) > 0
          ) {
            setState("owner");
          } else {
            clearSession("guest");
          }
        } else {
          setState("unavailable");
        }
      });
    return () => controller.abort();
  }, [api, clearSession, statusAttempt]);

  const login = useCallback(
    async (token: string) => {
      const exchanged = await api.exchange(token);
      const marker = nextMarker(markerRef.current);
      const next = { ...exchanged, ...marker };
      acceptSession(next);
      broadcast({ type: "offer-session", requestId: null, session: next });
    },
    [acceptSession, api, broadcast],
  );

  const logout = useCallback(
    async (instanceId: string | null) => {
      const current = sessionRef.current;
      if (current) {
        try {
          await api.logout(current.csrfToken);
        } catch (error) {
          if (!(error instanceof PublicApiError && error.status === 401)) {
            throw error;
          }
        }
      }
      const marker = nextMarker(markerRef.current);
      clearSession("guest", marker);
      broadcast({ type: "clear-session", marker });
      clearComposeDraft(instanceId);
    },
    [api, broadcast, clearSession],
  );

  const expireIfInvalidSession = useCallback(
    (error: unknown) => {
      if (!isInvalidSessionError(error)) return false;
      clearSession("expired");
      requestSyncedSession();
      return true;
    },
    [clearSession, requestSyncedSession],
  );

  const retryStatus = useCallback(
    () => setStatusAttempt((value) => value + 1),
    [],
  );

  const value = useMemo<OwnerContextValue>(
    () => ({
      api,
      state,
      session,
      login,
      logout,
      expireIfInvalidSession,
      retryStatus,
    }),
    [api, expireIfInvalidSession, login, logout, retryStatus, session, state],
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
