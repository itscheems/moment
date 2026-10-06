import { useEffect, type ReactNode } from "react";
import { useMentionResolver } from "../../app/mentions";
import { InternalLink } from "../../app/navigation";
import { SiteIdentityPreview } from "../TimelineInteractions";
import styles from "./Content.module.css";

/** Mentions share the same profile card and navigation as post authors. */
export function MentionLink({
  handle,
  children,
}: {
  handle: string;
  children: ReactNode;
}) {
  const { identity, consider, resolves } = useMentionResolver();
  useEffect(() => consider([handle]), [consider, handle]);
  if (!resolves(handle)) return children;
  const link = (
    <InternalLink
      href={`/${handle.toLowerCase()}`}
      data-moment-mention={handle.toLowerCase()}
      className={styles.mention}
    >
      {children}
    </InternalLink>
  );
  const profile = identity(handle);
  return profile ? (
    <SiteIdentityPreview site={profile}>{link}</SiteIdentityPreview>
  ) : (
    link
  );
}
