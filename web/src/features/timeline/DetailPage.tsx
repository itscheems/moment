import {
  useCallback,
  useEffect,
  useLayoutEffect,
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
import { CommentDetailPage } from "../comments/CommentDetailPage";
import {
  type CommentMutation,
  type DiscussionChange,
} from "../comments/discussionChanges";
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
  active = true,
  commentId = null,
}: {
  api: PublicApi;
  id: string;
  site: SiteIdentity;
  canPublish?: boolean;
  onSiteChanged?: (site: SiteIdentity) => void;
  focusReading?: boolean;
  active?: boolean;
  commentId?: string | null;
}) {
  const navigation = useNavigation();
  const { navigate } = navigation;
  const [post, setPost] = useState<PublicPost | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [replyDialogOpen, setReplyDialogOpen] = useState(false);
  const activeRequest = useRef<AbortController | null>(null);
  const replyTriggerRef = useRef<HTMLButtonElement>(null);
  const [visitedComments, setVisitedComments] = useState<string[]>([]);
  const [change, setChange] = useState<DiscussionChange | null>(null);
  const revision = useRef(0);
  const publishMutation = (source: string, mutation: CommentMutation) =>
    setChange({ ...mutation, source, revision: ++revision.current });
  useEffect(() => {
    if (commentId)
      setVisitedComments((current) =>
        current.includes(commentId) ? current : [...current, commentId],
      );
  }, [commentId]);
  const commentViews =
    commentId && !visitedComments.includes(commentId)
      ? [...visitedComments, commentId]
      : visitedComments;

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
    enabled: active,
  });

  useEffect(() => {
    if (active) void load();
    return () => activeRequest.current?.abort();
  }, [load, active]);

  useEffect(() => {
    setReplyDialogOpen(false);
    replyTriggerRef.current = null;
  }, [focusReading, id, commentId]);

  useLayoutEffect(() => {
    if (active && !commentId && !focusReading && post?.id === id && !loading)
      navigation.restoreCurrentEntry();
  }, [active, commentId, focusReading, post?.id, id, loading, navigation]);

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
      {active && !commentId && isReadArticlePost(post) && !focusReading ? (
        <HeaderActions>
          <ArticleFocusLink
            id={post.id}
            username={postAuthorSite(post, site).username}
          />
        </HeaderActions>
      ) : null}
      {active && !commentId ? (
        isReadArticlePost(post) ? (
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
        )
      ) : null}
      <CommentThread
        key={post.id}
        active={active && !focusReading && !commentId}
        api={api}
        onCountChange={handleCommentCountChange}
        onReplyDialogOpenChange={setReplyDialogOpen}
        postId={post.id}
        post={post}
        replyDialogOpen={replyDialogOpen}
        replyDialogTriggerRef={replyTriggerRef}
        site={site}
        change={change}
        onMutation={(mutation) => publishMutation("post", mutation)}
      />
      {commentViews.map((view) => (
        <CommentDetailPage
          key={view}
          api={api}
          post={post}
          site={site}
          commentId={view}
          active={active && commentId === view}
          change={change}
          onMutation={(mutation) => publishMutation(view + ":direct", mutation)}
          onCountChange={handleCommentCountChange}
        />
      ))}
    </>
  );
}
