import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type MouseEvent,
} from "react";

import {
  PublicApi,
  PublicApiError,
  isReadArticlePost,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { ArticlePage } from "../article/ArticlePage";
import { ArticleFocusLink } from "../article/ArticleFocusLink";
import { HeaderActions } from "../../components/headerActions";
import { postAuthorSite } from "../../components/SiteIdentity";
import { PostPresentation } from "../posts/PostPresentation";
import { CommentThread } from "../comments/CommentThread";
import { useNavigation } from "../../app/navigation";
import { useReadRecovery } from "../../components/useReadRecovery";
import {
  PublicError,
  ReadRecoveryNotice,
  TimelineLoading,
} from "./PublicStates";

function detailErrorKind(error: unknown): "detail" | "missing" {
  if (error instanceof PublicApiError) {
    if (error.status === 404 || error.code === "POST_NOT_FOUND")
      return "missing";
  }
  return "detail";
}

export function DetailPage({
  api,
  id,
  site,
  canPublish = false,
  onSiteChanged,
  focusReading = false,
}: {
  api: PublicApi;
  id: string;
  site: SiteIdentity;
  canPublish?: boolean;
  onSiteChanged?: (site: SiteIdentity) => void;
  focusReading?: boolean;
}) {
  const { navigate } = useNavigation();
  const [post, setPost] = useState<PublicPost | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [replyDialogOpen, setReplyDialogOpen] = useState(false);
  const activeRequest = useRef<AbortController | null>(null);
  const replyTriggerRef = useRef<HTMLButtonElement>(null);

  const load = useCallback(async () => {
    activeRequest.current?.abort();
    const controller = new AbortController();
    activeRequest.current = controller;
    setLoading(true);
    setError(null);
    try {
      const next = await api.post(id, controller.signal);
      if (!controller.signal.aborted) setPost(next);
    } catch (nextError) {
      if (!controller.signal.aborted) setError(nextError);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [api, id]);
  const recovering = useReadRecovery({
    error,
    loading,
    onRetry: load,
    scope: id,
  });

  useEffect(() => {
    void load();
    return () => activeRequest.current?.abort();
  }, [load]);

  useEffect(() => {
    setReplyDialogOpen(false);
    replyTriggerRef.current = null;
  }, [focusReading, id]);

  // The thread reports how the total moved, not what it is. It only ever holds
  // one page of a conversation now, so counting its rows would replace a real
  // total with the size of whatever happens to be loaded.
  const handleCommentCountChange = useCallback((delta: number) => {
    if (delta === 0) return;
    setPost((current) =>
      current
        ? {
            ...current,
            commentCount: Math.max(0, current.commentCount + delta),
          }
        : current,
    );
  }, []);

  if (loading && post?.id !== id) return <TimelineLoading />;
  if (error && (!recovering || post?.id !== id)) {
    return (
      <PublicError kind={detailErrorKind(error)} recovering={recovering} />
    );
  }
  if (!post || post.id !== id) return null;
  if (focusReading && !isReadArticlePost(post)) {
    return <PublicError kind="missing" />;
  }
  const openReply = (event: MouseEvent<HTMLButtonElement>) => {
    replyTriggerRef.current = event.currentTarget;
    setReplyDialogOpen(true);
  };
  // Articles are read at the post detail address, so the route is shared and
  // only the surface differs: an article page leads with its title and cover
  // where a moment leads with its prose.
  return (
    <>
      {loading || error ? <ReadRecoveryNotice /> : null}
      {isReadArticlePost(post) && !focusReading ? (
        <HeaderActions>
          <ArticleFocusLink
            id={post.id}
            username={postAuthorSite(post, site).username}
          />
        </HeaderActions>
      ) : null}
      {isReadArticlePost(post) ? (
        <ArticlePage
          focusReading={focusReading}
          layout={focusReading ? "document" : "detail"}
          onDeleted={() => navigate("/")}
          onReply={openReply}
          onReposted={setPost}
          onSiteChanged={onSiteChanged}
          canPublish={canPublish}
          post={post}
          site={site}
        />
      ) : (
        <PostPresentation
          detail
          onDeleted={() => navigate("/")}
          onReply={openReply}
          onReposted={setPost}
          onSiteChanged={onSiteChanged}
          canPublish={canPublish}
          post={post}
          site={site}
        />
      )}
      <CommentThread
        key={post.id}
        active={!focusReading}
        api={api}
        onCountChange={handleCommentCountChange}
        onReplyDialogOpenChange={setReplyDialogOpen}
        postId={post.id}
        post={post}
        replyDialogOpen={replyDialogOpen}
        replyDialogTriggerRef={replyTriggerRef}
        site={site}
      />
    </>
  );
}
