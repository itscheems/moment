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

import {
  OwnerApi,
  type AccountLimits,
  type OwnerSession,
} from "../../api/owner";
import { PublicApiError, type Account } from "../../api/public";
import { notify } from "../../components/Feedback";
import { clearComposeDraft } from "./draft";
import { clearKeptDrafts } from "./drafts";
import { providerLabel, PROVIDER_NAME_PATTERN } from "./providers";

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

/**
 * What this browser's session is, never who it belongs to.
 *
 * `"owner"` is a name from the single-owner era that now means exactly
 * "signed in": a member holds the very same state. Whether the signed-in
 * account owns this site is a fact about the account -- `account.role` --
 * so anything that gates on ownership has to read that and not this.
 */
export type OwnerState =
  "checking" | "guest" | "owner" | "expired" | "unavailable";

interface AuthOutcome {
  kind: "ok" | "linked";
  provider: string | null;
}

interface OwnerContextValue {
  api: OwnerApi;
  state: OwnerState;
  session: BrowserOwnerSession | null;
  /**
   * Who this browser is signed in as, once the server has said so. Null while
   * signed out, and also on an instance that cannot answer for accounts -- so
   * "not the owner" has to be read as `account?.role === "member"`, never as
   * the absence of an owner role.
   */
  account: Account | null;
  /**
   * The bounds the server enforces on this account, as it published them.
   *
   * Travels with the account because it arrives in the same response, and is
   * null for exactly as long as the account is: a caller that has somebody to
   * render controls for has the numbers those controls need.
   */
  limits: AccountLimits | null;
  login: (
    credential: string,
    method?: "password" | "token",
    username?: string,
  ) => Promise<void>;
  logout: (instanceId: string | null) => Promise<void>;
  /**
   * Drop this browser's session without asking the server to end it, for when
   * the server has already ended it -- signing every device out, for instance.
   */
  forgetSession: (instanceId: string | null) => void;
  applyAccount: (account: Account) => void;
  expireIfInvalidSession: (error: unknown) => boolean;
  expireAfterTokenRotation: () => void;
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

export function OwnerProvider({ children }: { children: ReactNode }) {
  const api = useMemo(() => new OwnerApi(), []);
  const [session, setSession] = useState<BrowserOwnerSession | null>(
    readSession,
  );
  const [state, setState] = useState<OwnerState>("checking");
  const [account, setAccount] = useState<Account | null>(null);
  const [limits, setLimits] = useState<AccountLimits | null>(null);
  const [statusAttempt, setStatusAttempt] = useState(0);
  const [authOutcome] = useState(readAuthOutcome);
  const sessionRef = useRef(session);
  const announcedRef = useRef(false);
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
      // An expired session still belongs to the same person, and the pages
      // that offer re-login inline need to keep knowing who that is. Only a
      // real sign-out forgets them.
      if (nextState === "guest") {
        setAccount(null);
        setLimits(null);
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
    (granted: OwnerSession) => {
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

  /**
   * Work out what this browser already is, from the cookie it may be carrying.
   *
   * It has to be this endpoint. `/api/v1/owner/status` answers 401 for a
   * member, so probing with that would sign every member out on every refresh.
   * It is also how a provider callback's cookie becomes a session the SPA can
   * see: the callback can set a cookie but cannot reach into session storage,
   * so `?auth=ok` lands on a page that believes nobody is here until this asks.
   */
  useEffect(() => {
    const controller = new AbortController();
    const markerAtStart = markerRef.current;
    // A sibling tab can hand this one a newer session while the request is in
    // flight; that session outranks whatever the server just said about the
    // older one.
    const overtaken = () =>
      sessionRef.current !== null &&
      compareMarker(markerRef.current, markerAtStart) > 0;
    const signedOut = () => {
      if (overtaken()) setState("owner");
      else clearSession("guest");
    };
    setState("checking");
    void api
      .accountSession(controller.signal)
      .then((restored) => {
        if (controller.signal.aborted) return;
        setAccount(restored.account);
        setLimits(restored.limits);
        // A session this tab already holds is kept as it is: its CSRF token
        // came from the same cookie, and taking a new marker for it would make
        // an ordinary reload look like a fresh login to every other tab.
        if (sessionRef.current) setState("owner");
        else adoptSession(restored);
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        // 401 is an answer -- nobody is signed in. Anything else is the
        // question going unanswered, and guessing at it either strands the
        // owner or hands their controls to a visitor.
        if (error instanceof PublicApiError && error.status === 401) {
          signedOut();
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
    if (state !== "owner" || account !== null || session === null) return;
    const controller = new AbortController();
    void api
      .accountSession(controller.signal)
      .then((restored) => {
        if (!controller.signal.aborted) {
          setAccount(restored.account);
          setLimits(restored.limits);
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
          ? `已绑定 ${providerLabel(authOutcome.provider)}`
          : "已绑定新的登录方式",
      ),
    );
  }, [authOutcome]);

  const login = useCallback(
    async (
      credential: string,
      method: "password" | "token" = "password",
      username?: string,
    ) => {
      const exchanged = await api.exchange(credential, method, username);
      // Read whose session this is before announcing it. Announcing first
      // would put every tab through a frame of "signed in, role unknown", and
      // that frame decides whether owner controls are on screen.
      try {
        const restored = await api.accountSession();
        setAccount(restored.account);
        setLimits(restored.limits);
      } catch {
        // The session is real either way. The restore effect above notices the
        // missing account and reports it rather than stranding the sign-in.
      }
      adoptSession(exchanged);
    },
    [adoptSession, api],
  );

  const applyAccount = useCallback((next: Account) => setAccount(next), []);

  const forgetSession = useCallback(
    (instanceId: string | null) => {
      const marker = nextMarker(markerRef.current);
      clearSession("guest", marker);
      broadcast({ type: "clear-session", marker });
      clearComposeDraft(instanceId, { reason: "logout" });
      clearKeptDrafts(instanceId);
    },
    [broadcast, clearSession],
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

  const expireAfterTokenRotation = useCallback(() => {
    const marker = nextMarker(markerRef.current);
    clearSession("expired", marker);
    broadcast({ type: "clear-session", marker });
  }, [broadcast, clearSession]);

  const retryStatus = useCallback(
    () => setStatusAttempt((value) => value + 1),
    [],
  );

  const value = useMemo<OwnerContextValue>(
    () => ({
      api,
      state,
      session,
      account,
      limits,
      login,
      logout,
      forgetSession,
      applyAccount,
      expireIfInvalidSession,
      expireAfterTokenRotation,
      retryStatus,
    }),
    [
      account,
      api,
      applyAccount,
      expireAfterTokenRotation,
      expireIfInvalidSession,
      forgetSession,
      limits,
      login,
      logout,
      retryStatus,
      session,
      state,
    ],
  );

  return (
    <OwnerContext.Provider value={value}>{children}</OwnerContext.Provider>
  );
}

/**
 * Whether these account facts belong to the account that owns this site.
 *
 * An unknown account is not the owner. Nothing here guesses: a lookup that has
 * not answered leaves the session `checking` or `unavailable`, so the only way
 * to reach an owner-only control is for the server to have said so. Briefly
 * withholding it from the owner costs one reload; the other mistake renders
 * their controls to whoever happens to be reading.
 */
export function ownsSite(account: Account | null): boolean {
  return account?.role === "owner";
}

export function useOwner(): OwnerContextValue {
  const value = useContext(OwnerContext);
  if (!value) throw new Error("useOwner must be used within OwnerProvider");
  return value;
}
