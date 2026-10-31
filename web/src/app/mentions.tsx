import { interactionsApi, type SubjectKind } from "../api/interactions";
import {
  createContext,
  useCallback,
  useEffect,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  PublicApi,
  type PublicAccount,
  type SiteIdentity,
} from "../api/public";

type MentionIdentity = PublicAccount | SiteIdentity;
const NO_IDENTITIES: readonly MentionIdentity[] = [];

interface MentionResolver {
  /** Whether this handle is known to name an account. */
  resolves: (handle: string) => boolean;
  /** Ask about handles not yet known. Safe to call with what is already known. */
  consider: (handles: readonly string[]) => void;
  identity: (handle: string) => MentionIdentity | undefined;
}

const MentionContext = createContext<MentionResolver | null>(null);

/**
 * The number of unknown handles one batch will ask about.
 *
 * Content is author-supplied, so a single post can name a great many handles
 * that resolve to nothing. Each is one request, and refusing to link is the
 * safe answer, so the excess is simply left as text rather than queued.
 */
const MAX_HANDLES_PER_BATCH = 16;

export function MentionProvider({
  children,
  known = [],
  identities = NO_IDENTITIES,
}: {
  children: ReactNode;

  known?: readonly string[];
  identities?: readonly MentionIdentity[];
}) {
  const [resolved, setResolved] = useState<ReadonlyMap<string, PublicAccount>>(
    new Map(),
  );
  // Asked-about handles, kept in a ref because they must not cause a render:
  // the answer does, the question does not.
  const asked = useRef(new Set<string>());

  const knownSet = useMemo(
    () => new Set(known.map((handle) => handle.toLocaleLowerCase())),
    [known],
  );
  const knownIdentities = useMemo(
    () =>
      new Map(
        identities.map((identity) => [
          identity.username.toLowerCase(),
          identity,
        ]),
      ),
    [identities],
  );

  const consider = useCallback(
    (handles: readonly string[]) => {
      const fresh = handles
        .map((handle) => handle.toLocaleLowerCase())
        .filter(
          (handle) =>
            !asked.current.has(handle) && !knownIdentities.has(handle),
        )
        .slice(0, MAX_HANDLES_PER_BATCH);
      if (fresh.length === 0) return;
      for (const handle of fresh) asked.current.add(handle);
      for (const handle of fresh) {
        void new PublicApi(fetch.bind(window))
          .user(handle)
          .then((identity) => {
            setResolved((current) => {
              const next = new Map(current);
              next.set(handle, identity);
              return next;
            });
          })
          .catch(() => {
            // An unanswered question is not a "no". The handle stays text, and
            // stays asked: retrying on every render would turn one unreachable
            // server into a request per paint.
          });
      }
    },
    [knownIdentities],
  );

  const value = useMemo<MentionResolver>(
    () => ({
      resolves: (handle) => {
        const normalized = handle.toLocaleLowerCase();
        return (
          knownSet.has(normalized) ||
          knownIdentities.has(normalized) ||
          resolved.has(normalized)
        );
      },
      consider,
      identity: (handle) =>
        knownIdentities.get(handle.toLowerCase()) ??
        resolved.get(handle.toLowerCase()),
    }),
    [consider, knownSet, knownIdentities, resolved],
  );

  return (
    <MentionContext.Provider value={value}>{children}</MentionContext.Provider>
  );
}

/**
 * The resolver, or one that resolves nothing.
 *
 * Rendered markdown appears in places that have no provider above them -- a
 * share preview, a test -- and refusing to link is the answer that cannot be
 * wrong there.
 */
export function useMentionResolver(): MentionResolver {
  return useContext(MentionContext) ?? unresolved;
}

const unresolved: MentionResolver = {
  resolves: () => false,
  consider: () => {},
  identity: () => undefined,
};

/** New sources resolve saved account IDs; unresolved or imported identities stay text. */
export function SourceMentionProvider({
  kind,
  id,
  revision,
  children,
}: {
  kind: SubjectKind;
  id: string;
  revision: string;
  children: ReactNode;
}) {
  const inherited = useMentionResolver();
  const key = kind + ":" + id + ":" + revision;
  const [state, setState] = useState<{
    key: string;
    bindings: ReadonlyMap<string, PublicAccount | null> | null;
  } | null>(null);
  const requested = useRef<{ key: string; controller: AbortController } | null>(
    null,
  );
  useEffect(
    () => () => {
      if (requested.current?.key === key) {
        requested.current.controller.abort();
        requested.current = null;
      }
    },
    [key],
  );
  const consider = useCallback(
    (handles: readonly string[]) => {
      if (handles.length === 0) return;
      if (state?.key === key) {
        inherited.consider(
          state.bindings === null
            ? handles
            : handles.flatMap((handle) => {
                const account = state.bindings?.get(handle.toLowerCase());
                return account ? [account.username] : [];
              }),
        );
        return;
      }
      if (requested.current?.key === key) return;
      requested.current?.controller.abort();
      const controller = new AbortController();
      requested.current = { key, controller };
      void interactionsApi
        .boundMentions(kind, id, controller.signal)
        .then((bindings) => {
          if (
            controller.signal.aborted ||
            requested.current?.controller !== controller
          )
            return;
          setState({
            key,
            bindings: bindings.length
              ? new Map(bindings.map((row) => [row.handle, row.account]))
              : null,
          });
        })
        .catch(() => {
          if (!controller.signal.aborted)
            setState({ key, bindings: new Map() });
        });
    },
    [kind, id, key, state, inherited],
  );
  const value = useMemo<MentionResolver>(() => {
    if (state?.key === key && state.bindings === null) return inherited;
    const bindings = state?.key === key ? state.bindings : null;
    return {
      consider,
      resolves: (handle) => Boolean(bindings?.get(handle.toLowerCase())),
      identity: (handle) => {
        const bound = bindings?.get(handle.toLowerCase());
        if (!bound) return undefined;
        const full = inherited.identity(bound.username);
        const userId = full
          ? "userId" in full
            ? full.userId
            : full.profileUserId
          : null;
        return userId === bound.userId ? full : bound;
      },
    };
  }, [state, key, inherited, consider]);
  return (
    <MentionContext.Provider value={value}>{children}</MentionContext.Provider>
  );
}
