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
import { PostPresentation } from "../posts/PostPresentation";
import { CommentThread } from "../comments/CommentThread";
import { useNavigation } from "../../app/navigation";
import { PublicError, TimelineLoading } from "./PublicStates";

function detailErrorKind(error: unknown): "detail" | "missing" | "malformed" {
  if (error instanceof PublicApiError) {
    if (error.status === 404 || error.code === "POST_NOT_FOUND")
      return "missing";
    if (error.code === "API_RESPONSE_INVALID") return "malformed";
  }
  return "detail";
}

export function DetailPage({
  api,
  id,
  site,
  owner = false,
  onSiteChanged,
}: {
  api: PublicApi;
  id: string;
  site: SiteIdentity;
  owner?: boolean;
  onSiteChanged?: (site: SiteIdentity) => void;
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
      setPost(await api.post(id, controller.signal));
    } catch (nextError) {
      if (!controller.signal.aborted) setError(nextError);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [api, id]);

  useEffect(() => {
    void load();
    return () => activeRequest.current?.abort();
  }, [load]);

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

  if (loading) return <TimelineLoading />;
  if (error) {
    return <PublicError kind={detailErrorKind(error)} onRetry={load} />;
  }
  if (!post) return null;
  const openReply = (event: MouseEvent<HTMLButtonElement>) => {
    replyTriggerRef.current = event.currentTarget;
    setReplyDialogOpen(true);
  };
  // Articles are read at the post detail address, so the route is shared and
  // only the surface differs: an article page leads with its title and cover
  // where a moment leads with its prose.
  return (
    <>
      {isReadArticlePost(post) ? (
        <ArticlePage
          onDeleted={() => navigate("/")}
          onReply={openReply}
          onReposted={setPost}
          onSiteChanged={onSiteChanged}
          owner={owner}
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
          owner={owner}
          post={post}
          site={site}
        />
      )}
      <CommentThread
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
