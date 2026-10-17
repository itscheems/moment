import { useEffect, type ReactElement } from "react";
import { useMentionResolver } from "../app/mentions";
import { SiteIdentityPreview } from "./TimelineInteractions";

export function AccountIdentityPreview({
  author,
  children,
}: {
  author: { userId: string | null; username: string | null };
  children: ReactElement;
}) {
  const resolver = useMentionResolver();
  useEffect(() => {
    if (author.username) resolver.consider([author.username]);
  }, [author.username, resolver]);
  if (!author.username || !author.userId) return children;
  const identity = resolver.identity(author.username);
  const identityId =
    identity &&
    ("userId" in identity ? identity.userId : identity.profileUserId);
  if (!identity || identityId !== author.userId) return children;
  return <SiteIdentityPreview site={identity}>{children}</SiteIdentityPreview>;
}
