import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type {
  CommentContext,
  PublicAccount,
  PublicApi,
  PublicPost,
} from "../../api/public";
import { useRailSlot } from "../../components/railSlot";
import {
  RAIL_VIEWPORT_QUERY,
  useMediaQuery,
} from "../../components/useMediaQuery";
import { useReadRecovery } from "../../components/useReadRecovery";
import { loadRelevantPeople } from "./loadRelevantPeople";
import { PeopleCard } from "./PeopleCard";

/** The active detail owns the rail; cached, inactive discussions publish nothing. */
export function RelevantPeople({
  api,
  post,
  context = null,
  active = true,
}: {
  api: PublicApi;
  post: PublicPost;
  context?: CommentContext | null;
  active?: boolean;
}) {
  const slot = useRailSlot();
  const wide = useMediaQuery(RAIL_VIEWPORT_QUERY);
  const enabled = active && wide && slot !== null;
  const [people, setPeople] = useState<PublicAccount[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);
  const [loading, setLoading] = useState(false);
  const retry = useCallback(() => setAttempt((value) => value + 1), []);
  const scope = `${post.id}:${context?.comment.id ?? "post"}`;
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    setPeople(null);
    setError(null);
    setLoading(true);
    void loadRelevantPeople(api, post, context, controller.signal)
      .then((found) => {
        if (!controller.signal.aborted) setPeople(found);
      })
      .catch((failure: unknown) => {
        if (!controller.signal.aborted) setError(failure);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [api, post, context, enabled, attempt]);
  useReadRecovery({ error, loading, onRetry: retry, enabled, scope });
  if (!enabled || (!error && !people?.length)) return null;
  return createPortal(
    <PeopleCard
      title="相关的人"
      people={people ?? []}
      error={!!error}
      errorMessage="暂时无法读取相关的人。"
      onRetry={retry}
    />,
    slot,
  );
}
