import {
  createContext,
  useCallback,
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
