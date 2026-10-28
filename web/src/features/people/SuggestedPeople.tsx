import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { PublicAccount, PublicApi } from "../../api/public";
import { useRailSlot } from "../../components/railSlot";
import {
  RAIL_VIEWPORT_QUERY,
  useMediaQuery,
} from "../../components/useMediaQuery";
import { useReadRecovery } from "../../components/useReadRecovery";
import { useSession } from "../auth/SessionProvider";
import { PeopleCard } from "./PeopleCard";
import { loadSuggestedPeople } from "./loadSuggestedPeople";
import { ReadRecoveryToast } from "../timeline/PublicStates";

// Preview builds can select accounts already present in the shared test dataset.
const configuredUsernames: unknown = import.meta.env.VITE_RECOMMENDED_USERNAMES;
const DEFAULT_USERNAMES = [
  ...new Set(
    (typeof configuredUsernames === "string"
      ? configuredUsernames
      : "itscheems,momentdev"
    )
      .split(",")
      .map((name) => name.trim().toLowerCase())
      .filter(Boolean),
  ),
];

export function SuggestedPeople({
  api,
  kind,
  profileUsername = null,
  active = true,
  usernames = DEFAULT_USERNAMES,
}: {
  api: PublicApi;
  kind: "home" | "profile";
  profileUsername?: string | null;
  active?: boolean;
  usernames?: readonly string[];
}) {
  const slot = useRailSlot();
  const wide = useMediaQuery(RAIL_VIEWPORT_QUERY);
  const auth = useSession();
  const sessionReady =
    auth.state === "guest" ||
    auth.state === "signed-in" ||
    auth.state === "expired";
  const enabled =
    active &&
    wide &&
    slot !== null &&
    (kind === "profile" ? !!profileUsername : sessionReady);
  const userId = kind === "home" ? auth.account?.userId : null;
  const excludedUsername = kind === "profile" ? profileUsername : null;
  const scope = `${kind}:${userId ?? excludedUsername ?? "guest"}`;
  const [result, setResult] = useState<{
    scope: string;
    people: PublicAccount[];
  } | null>(null);
  const [failure, setFailure] = useState<{
    scope: string;
    error: unknown;
  } | null>(null);
  const [loading, setLoading] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const retry = useCallback(() => setAttempt((value) => value + 1), []);
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    setLoading(true);
    setFailure(null);
    setResult(null);
    void loadSuggestedPeople(
      api,
      usernames,
      { userId, username: excludedUsername },
      controller.signal,
    )
      .then((people) => {
        if (!controller.signal.aborted) setResult({ scope, people });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setFailure({ scope, error });
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [api, usernames, userId, excludedUsername, scope, enabled, attempt]);
  const people = result?.scope === scope ? result.people : [];
  const error = failure?.scope === scope ? failure.error : null;
  const recovering = useReadRecovery({
    error,
    loading,
    onRetry: retry,
    enabled,
    scope,
  });
  if (recovering)
    return (
      <ReadRecoveryToast onRetry={retry}>
        正在重新获取推荐用户…
      </ReadRecoveryToast>
    );
  if (!enabled || (!error && people.length === 0)) return null;
  return createPortal(
    <PeopleCard
      title={kind === "home" ? "Who to follow" : "You might like"}
      people={people}
      compact
      error={!!error}
      onRetry={retry}
    />,
    slot,
  );
}
