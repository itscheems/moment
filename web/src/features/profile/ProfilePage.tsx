import { useCallback, useEffect, useRef, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { SiteAvatar, SiteCover } from "../../components/SiteIdentity";
import { ShareMenu } from "../../components/TimelineInteractions";
import { useOwner } from "../owner/OwnerProvider";
import { PostPresentation } from "../posts/PostPresentation";
import { PublicError, TimelineLoading } from "../timeline/PublicStates";
import styles from "./ProfilePage.module.css";
import { ProfileEditor } from "./ProfileEditor";

export function ProfilePage({
  api,
  site,
  onSiteSaved,
}: {
  api: PublicApi;
  site: SiteIdentity;
  onSiteSaved: (site: SiteIdentity) => void;
}) {
  const owner = useOwner();
  const [posts, setPosts] = useState<PublicPost[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const active = useRef<AbortController | null>(null);
  const title = site.title.trim() || "Moment";

  const load = useCallback(async () => {
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    setLoading(true);
    setError(null);
    try {
      const page = await api.timeline({ signal: controller.signal });
      if (!controller.signal.aborted) {
        setPosts(page.items);
        setNextCursor(page.nextCursor);
      }
    } catch (nextError) {
      if (!controller.signal.aborted) setError(nextError);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    void load();
    return () => active.current?.abort();
  }, [load]);

  const more = async () => {
    if (!nextCursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const page = await api.timeline({ cursor: nextCursor });
      setPosts((current) => [...current, ...page.items]);
      setNextCursor(page.nextCursor);
    } catch (nextError) {
      setError(nextError);
    } finally {
      setLoadingMore(false);
    }
  };

  return (
    <section aria-label={`${title} 的个人资料`} className={styles.profile}>
      <div className={styles.cover}>
        <SiteCover eager site={site} />
      </div>
      <div className={styles.identity}>
        <div className={styles.avatar}>
          <SiteAvatar eager site={site} />
        </div>
        <div className={styles.profileActions}>
          <ShareMenu
            baseUrl={site.publicBaseUrl}
            href="/profile"
            title={`${title} 的个人资料`}
            trigger={
              <button aria-label="分享个人资料" type="button">
                <Icon name="share" />
              </button>
            }
          />
          {owner.state === "owner" ? (
            <ProfileEditor onSaved={onSiteSaved} site={site} />
          ) : null}
        </div>
        <h2>{title}</h2>
        {site.bio.trim() ? <p>{site.bio.trim()}</p> : null}
        <div className={styles.profileMeta}>
          <span>
            <Icon name="archive" /> {site.timezone}
          </span>
          <InternalLink href="/archive">查看归档</InternalLink>
        </div>
        <nav aria-label="个人资料内容" className={styles.tabs}>
          <span aria-current="page">Moment</span>
          <InternalLink href="/archive">媒体与归档</InternalLink>
        </nav>
      </div>

      {loading ? <TimelineLoading /> : null}
      {error ? (
        <PublicError
          kind={
            error instanceof PublicApiError &&
            error.code === "API_RESPONSE_INVALID"
              ? "malformed"
              : "timeline"
          }
          onRetry={load}
        />
      ) : null}
      {!loading && !error ? (
        posts.length > 0 ? (
          <div>
            {posts.map((post) => (
              <PostPresentation
                key={post.id}
                owner={owner.state === "owner"}
                post={post}
                site={site}
              />
            ))}
            {nextCursor ? (
              <div className={styles.moreRow}>
                <button disabled={loadingMore} onClick={() => void more()}>
                  {loadingMore ? "正在加载…" : "加载更早的 Moment"}
                </button>
              </div>
            ) : null}
          </div>
        ) : (
          <p className={styles.empty}>还没有 Moment</p>
        )
      ) : null}
    </section>
  );
}
