import type { ReactNode } from "react";
import type { PublicAccount } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { SiteAvatar } from "../../components/SiteIdentity";
import { SiteIdentityPreview } from "../../components/TimelineInteractions";
import { MentionLink } from "../../components/content/MentionLink";
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

function Person({
  person,
  compact,
}: {
  person: PublicAccount;
  compact: boolean;
}) {
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
        {!compact && person.bio ? (
          <div className={styles.bio} dir="auto">
            {biography(person.bio)}
          </div>
        ) : null}
      </div>
    </li>
  );
}

export function PeopleCard({
  title,
  people,
  compact = false,
  error = false,
  errorMessage = "暂时无法读取账号资料。",
  onRetry,
}: {
  title: string;
  people: readonly PublicAccount[];
  compact?: boolean;
  error?: boolean;
  errorMessage?: string;
  onRetry: () => void;
}) {
  return (
    <section
      aria-label={title}
      className={`${styles.card}${compact ? ` ${styles.compact}` : ""}`}
    >
      <h2>
        <span>{title}</span>
      </h2>
      {error ? (
        <div className={styles.error} role="status">
          <span>{errorMessage}</span>
          <button type="button" onClick={onRetry}>
            重试
          </button>
        </div>
      ) : (
        <ul>
          {people.map((person) => (
            <Person key={person.userId} person={person} compact={compact} />
          ))}
        </ul>
      )}
    </section>
  );
}
