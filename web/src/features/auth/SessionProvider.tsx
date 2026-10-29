import { useTranslation } from "react-i18next";
import { type AccountPreferences } from "../../api/preferences";
import { accountCacheScope } from "./accountScope";
import { authApi, type AccountLimits, type AuthSession } from "../../api/auth";
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

import { type AuthApi } from "../../api/auth";
import { PublicApiError, type Account } from "../../api/public";
import { notify } from "../../components/Feedback";
import { clearComposeDraft } from "../compose/draft";
import { clearKeptDrafts } from "../compose/drafts";
import { providerLabel, PROVIDER_NAME_PATTERN } from "./providers";

const SESSION_KEY = "moment.auth.session.v2";
const SESSION_CHANNEL = "moment.auth.session.sync.v2";
const VERSION_PATTERN = /^[a-z0-9-]{8,80}$/;
const REQUEST_PATTERN = /^[a-z0-9-]{8,80}$/;

export interface BrowserSession extends AuthSession {
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
      session: BrowserSession;
    }
  | { type: "clear-session"; marker: SessionMarker };

/** Authentication state is independent of account roles and resource ownership. */
export type SessionState =
  "checking" | "guest" | "signed-in" | "expired" | "unavailable";

export class RegistrationSessionRestoreError extends Error {
  readonly accountCreated = true;

  constructor(readonly username: string) {
    super("account was created but its browser session could not be confirmed");
    this.name = "RegistrationSessionRestoreError";
  }
}

interface AuthOutcome {
  kind: "ok" | "linked";
  provider: string | null;
}

interface SessionContextValue {
  api: AuthApi;
  state: SessionState;
  session: BrowserSession | null;

  account: Account | null;
  /**
   * The bounds the server enforces on this account, as it published them.
   *
   * Travels with the account because it arrives in the same response, and is
   * null for exactly as long as the account is: a caller that has somebody to
   * render controls for has the numbers those controls need.
   */
  limits: AccountLimits | null;
  preferences: AccountPreferences | null;
  applyPreferences: (
    userId: string,
    csrfToken: string,
    next: AccountPreferences,
  ) => boolean;
  login: (
    credential: string,
    method?: "password" | "token",
    username?: string,
  ) => Promise<void>;
  register: (username: string, password: string) => Promise<Account>;
  logout: (instanceId: string | null) => Promise<void>;
  /**
   * Drop this browser's session without asking the server to end it, for when
   * the server has already ended it -- signing every device out, for instance.
   */
  forgetSession: (instanceId: string | null) => void;
  applyAccount: (account: Account) => void;
  expireIfInvalidSession: (error: unknown) => boolean;
  retryStatus: () => void;
}

const SessionContext = createContext<SessionContextValue | null>(null);

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

function markerOf(session: BrowserSession | null): SessionMarker {
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

function parseSession(value: unknown): BrowserSession | null {
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

function sessionWire(session: BrowserSession): Record<string, unknown> {
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

function readSession(): BrowserSession | null {
  try {
    const raw = window.sessionStorage.getItem(SESSION_KEY);
    if (!raw) return null;
    const session = parseSession(JSON.parse(raw));
    if (!session) throw new Error("invalid session");
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

function writeSession(session: BrowserSession | null): void {
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

/**
 * What a provider round trip just accomplished, as the callback reports it.
 *
 * The callback can only hand the browser a cookie; the SPA decides it is
 * signed in from its own session storage, so without this read a completed
 * sign-in would land on a page that still believes nobody is here.
 */
function readAuthOutcome(): AuthOutcome | null {
  const params = new URLSearchParams(window.location.search);
  const kind = params.get("auth");
  if (kind !== "ok" && kind !== "linked") return null;
  // A binding says which provider it bound; a sign-in does not, because which
  // account was used to arrive is not a fact worth announcing.
  const provider = params.get("provider");
  return {
    kind,
    provider:
      provider && PROVIDER_NAME_PATTERN.test(provider) ? provider : null,
  };
}

// The outcome is a one-shot fact. Left on the URL it would re-announce itself
// on every reload and on every share of that address.
function stripAuthOutcome(): void {
  const url = new URL(window.location.href);
  if (!url.searchParams.has("auth")) return;
  url.searchParams.delete("auth");
  url.searchParams.delete("provider");
  window.history.replaceState(
    window.history.state,
    "",
    `${url.pathname}${url.search}${url.hash}`,
  );
}

function isInvalidSessionError(error: unknown): boolean {
  return (
    error instanceof PublicApiError &&
    (error.status === 401 ||
      (error.status === 403 && error.code === "CSRF_INVALID"))
  );
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation();

  const api = authApi;
  const [session, setSession] = useState<BrowserSession | null>(readSession);
  const [state, setState] = useState<SessionState>("checking");
  const [account, setAccount] = useState<Account | null>(null);
  const [limits, setLimits] = useState<AccountLimits | null>(null);
  const [preferences, setPreferences] = useState<AccountPreferences | null>(
    null,
  );
  const accountRef = useRef(account);
  accountRef.current = account;
  const [statusAttempt, setStatusAttempt] = useState(0);
  const [authOutcome] = useState(readAuthOutcome);
  const sessionRef = useRef(session);
  const announcedRef = useRef(false);
  const markerRef = useRef(markerOf(session));
  const channelRef = useRef<BroadcastChannel | null>(null);

  const acceptSession = useCallback((next: BrowserSession) => {
    const marker = markerOf(next);
    if (compareMarker(marker, markerRef.current) <= 0) return false;
    markerRef.current = marker;
    sessionRef.current = next;
    writeSession(next);
    setSession(next);
    setState("signed-in");
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
      // Preserve the account while its editing surfaces wait behind
      // reauthentication. Only an explicit sign-out forgets that identity.
      if (nextState === "guest") {
        setAccount(null);
        setLimits(null);
        setPreferences(null);
      }
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

  /**
   * Take a session this browser has just been granted.
   *
   * Every route into a session goes through here so the marker stays strictly
   * increasing and the other tabs hear about it. Skipping the marker would let
   * a sibling's older session win the next exchange and sign this one out.
   */
  const adoptSession = useCallback(
    (granted: AuthSession) => {
      const marker = nextMarker(markerRef.current);
      const next = { ...granted, ...marker };
      acceptSession(next);
      broadcast({ type: "offer-session", requestId: null, session: next });
    },
    [acceptSession, broadcast],
  );

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
        if (acceptSession(message.session)) {
          setAccount(null);
          setLimits(null);
          setPreferences(null);
        }
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

  /**
   * Work out what this browser already is, from the cookie it may be carrying.
   *
   * It has to be this endpoint. `/api/v1/status` answers 401 for a
   * member, so probing with that would sign every member out on every refresh.
   * It is also how a provider callback's cookie becomes a session the SPA can
   * see: the callback can set a cookie but cannot reach into session storage,
   * so `?auth=ok` lands on a page that believes nobody is here until this asks.
   */
  useEffect(() => {
    const controller = new AbortController();
    const markerAtStart = markerRef.current;
    // A login, sign-out, or sibling session can overtake this request. Its
    // response describes the cookie from before that change and must not
    // restore ended credentials or overwrite the newly signed-in account.
    const overtaken = () =>
      compareMarker(markerRef.current, markerAtStart) !== 0;
    setState("checking");
    void authApi
      .accountSession(controller.signal)
      .then((restored) => {
        if (controller.signal.aborted || overtaken()) return;
        setAccount(restored.account);
        setLimits(restored.limits);
        setPreferences(restored.preferences);
        // Cookies are shared across tabs; sessionStorage is not. A callback
        // or another tab may have replaced the cookie while this tab retained
        // its old CSRF. Only keep its marker when both credentials match.
        const current = sessionRef.current;
        if (
          current?.csrfToken === restored.csrfToken &&
          current.expiresAt === restored.expiresAt
        ) {
          setState("signed-in");
        } else {
          adoptSession(restored);
        }
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted || overtaken()) return;
        if (error instanceof PublicApiError && error.status === 401) {
          clearSession("guest");
        } else {
          setState("unavailable");
        }
      });
    return () => controller.abort();
  }, [adoptSession, api, clearSession, statusAttempt]);

  /**
   * Fill in the account behind a session that arrived without one.
   *
   * A sibling tab offers only what fits on the wire, so a tab can be handed a
   * working session and still not know whose it is. Who it is decides what the
   * page may offer, so a lookup that will not answer becomes `unavailable`
   * rather than a browser rendering around an unknown role.
   */
  useEffect(() => {
    if (state !== "signed-in" || account !== null || session === null) return;
    const controller = new AbortController();
    void authApi
      .accountSession(controller.signal)
      .then((restored) => {
        if (!controller.signal.aborted) {
          setAccount(restored.account);
          setLimits(restored.limits);
          setPreferences(restored.preferences);
        }
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        // 401 is an answer: the session a sibling offered has already been
        // ended, so this tab is signed out and its copy of the token is worth
        // dropping. Calling that `unavailable` left a retry button that could
        // never succeed and kept the dead token in storage. The sibling effect
        // above already makes this distinction; this one did not.
        if (isInvalidSessionError(error)) {
          clearSession("guest");
          return;
        }
        setState("unavailable");
      });
    return () => controller.abort();
  }, [account, api, clearSession, session, state]);

  // Announce a completed binding and take the outcome off the URL. The sign-in
  // outcome needs no announcement: the page behind it changes on its own.
  useEffect(() => {
    // Announced once per round trip, not once per effect run: a remount must
    // not repeat a confirmation the reader has already seen.
    if (!authOutcome || announcedRef.current) return;
    announcedRef.current = true;
    stripAuthOutcome();
    if (authOutcome.kind !== "linked") return;
    // Deferred by a microtask on purpose. Effects run child before parent, and
    // the toast host is an ancestor of this provider -- announcing straight
    // from here would hand the message to a manager that has not mounted yet,
    // and it would be dropped on the floor.
    queueMicrotask(() =>
      notify(
        authOutcome.provider
          ? t("已绑定 {{value1}}", {
              value1: providerLabel(authOutcome.provider),
            })
          : t("已绑定新的登录方式"),
      ),
    );
  }, [authOutcome]);

  const login = useCallback(
    async (
      credential: string,
      method: "password" | "token" = "password",
      username?: string,
    ) => {
      const exchanged = await authApi.exchange(credential, method, username);
      try {
        const restored = await authApi.accountSession();
        setAccount(restored.account);
        setLimits(restored.limits);
        setPreferences(restored.preferences);
      } catch (error) {
        setAccount(null);
        setLimits(null);
        setPreferences(null);
        setState("unavailable");
        throw error;
      }
      adoptSession(exchanged);
    },
    [adoptSession, api],
  );

  const register = useCallback(
    async (username: string, password: string) => {
      const created = await authApi.register(username, password);
      let restored: Awaited<ReturnType<AuthApi["accountSession"]>>;
      try {
        restored = await authApi.accountSession();
        if (
          restored.account.username !== created.username ||
          restored.csrfToken !== created.csrfToken
        ) {
          throw new Error("registration session changed before confirmation");
        }
      } catch {
        setAccount(null);
        setLimits(null);
        setPreferences(null);
        setState("unavailable");
        throw new RegistrationSessionRestoreError(created.username);
      }
      setAccount(restored.account);
      setLimits(restored.limits);
      setPreferences(restored.preferences);
      adoptSession({
        csrfToken: created.csrfToken,
        expiresAt: created.expiresAt,
      });
      return restored.account;
    },
    [adoptSession, api],
  );

  const applyPreferences = useCallback(
    (userId: string, csrfToken: string, next: AccountPreferences) => {
      if (
        accountRef.current?.userId !== userId ||
        sessionRef.current?.csrfToken !== csrfToken
      )
        return false;
      setPreferences(next);
      return true;
    },
    [],
  );

  const applyAccount = useCallback((next: Account) => setAccount(next), []);

  const forgetSession = useCallback(
    (instanceId: string | null) => {
      const scope = accountCacheScope(instanceId, account?.userId ?? null);
      const marker = nextMarker(markerRef.current);
      clearSession("guest", marker);
      broadcast({ type: "clear-session", marker });
      clearComposeDraft(scope, { reason: "logout" });
      clearKeptDrafts(scope);
    },
    [account, broadcast, clearSession],
  );

  const logout = useCallback(
    async (instanceId: string | null) => {
      const current = sessionRef.current;
      if (current) {
        try {
          await authApi.logout(current.csrfToken);
        } catch (error) {
          if (!(error instanceof PublicApiError && error.status === 401)) {
            throw error;
          }
        }
      }
      forgetSession(instanceId);
    },
    [api, forgetSession],
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

  const value = useMemo<SessionContextValue>(
    () => ({
      api,
      state,
      session,
      account,
      limits,
      preferences,
      applyPreferences,
      login,
      register,
      logout,
      forgetSession,
      applyAccount,
      expireIfInvalidSession,
      retryStatus,
    }),
    [
      account,
      api,
      applyAccount,
      expireIfInvalidSession,
      forgetSession,
      limits,
      preferences,
      applyPreferences,
      login,
      register,
      logout,
      retryStatus,
      session,
      state,
    ],
  );

  return (
    <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
  );
}

export function isAdministrator(account: Account | null): boolean {
  return account?.role === "admin" && account.status === "active";
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext);
  if (!value) throw new Error("useSession must be used within SessionProvider");
  return value;
}

/** Presentation may render without authentication while never granting author controls. */
export function useOptionalSession(): SessionContextValue | null {
  return useContext(SessionContext);
}
