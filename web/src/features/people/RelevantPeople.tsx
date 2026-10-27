import { useCallback, useEffect, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import type {
  CommentContext,
  PublicAccount,
  PublicApi,
  PublicPost,
} from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { useRailSlot } from "../../components/railSlot";
import { SiteAvatar } from "../../components/SiteIdentity";
import { SiteIdentityPreview } from "../../components/TimelineInteractions";
import {
  RAIL_VIEWPORT_QUERY,
  useMediaQuery,
} from "../../components/useMediaQuery";
import { useReadRecovery } from "../../components/useReadRecovery";
import { MentionLink } from "../../components/content/MentionLink";
import { loadRelevantPeople } from "./loadRelevantPeople";
import styles from "./RelevantPeople.module.css";

function biography(text: string): ReactNode[] {
  return text
    .split(/(https?:\/\/[^\s]+|@[a-z][a-z0-9_]{2,29})/gi)
    .map((part, index) => {
      if (/^https?:\/\//i.test(part))
        return (
          <a href={part} key={index} rel="noreferrer" target="_blank">
            {part}
          </a>
        );
      if (/^@[a-z][a-z0-9_]{2,29}$/i.test(part))
        return (
          <MentionLink handle={part.slice(1)} key={index}>
            {part}
          </MentionLink>
        );
      return part;
    });
}

function Person({ person }: { person: PublicAccount }) {
  const { navigate } = useNavigation();
  return (
    <li
      className={styles.person}
      tabIndex={0}
      onClick={(event) => {
        if (
          event.button !== 0 ||
          event.metaKey ||
          event.ctrlKey ||
          event.shiftKey ||
          event.altKey ||
          !(event.target instanceof Element) ||
          !event.currentTarget.contains(event.target) ||
          event.target.closest("a") ||
          window.getSelection()?.toString()
        )
          return;
        navigate(person.profileUrl);
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter" && event.target === event.currentTarget)
          navigate(person.profileUrl);
      }}
    >
      <SiteIdentityPreview site={person}>
        <InternalLink
          aria-hidden="true"
          tabIndex={-1}
          href={person.profileUrl}
          className={styles.avatar}
        >
          <SiteAvatar eager site={person} size={40} />
        </InternalLink>
      </SiteIdentityPreview>
      <div className={styles.description}>
        <SiteIdentityPreview site={person}>
          <InternalLink className={styles.name} href={person.profileUrl}>
            <span>{person.displayName}</span>
          </InternalLink>
        </SiteIdentityPreview>
        <InternalLink
          tabIndex={-1}
          className={styles.handle}
          href={person.profileUrl}
        >
          @{person.username}
        </InternalLink>
        {person.bio ? (
          <div className={styles.bio} dir="auto">
            {biography(person.bio)}
          </div>
        ) : null}
      </div>
    </li>
  );
}

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
    <section aria-label="相关的人" className={styles.card}>
      <h2>相关的人</h2>
      {error ? (
        <div className={styles.error} role="status">
          <span>暂时无法读取相关的人。</span>
          <button type="button" onClick={retry}>
            重试
          </button>
        </div>
      ) : (
        <ul>
          {people?.map((person) => (
            <Person key={person.userId} person={person} />
          ))}
        </ul>
      )}
    </section>,
    slot,
  );
}
