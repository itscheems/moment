import { useCallback, useEffect, useRef, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
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
}: {
  api: PublicApi;
  id: string;
  site: SiteIdentity;
  owner?: boolean;
}) {
  const { navigate } = useNavigation();
  const [post, setPost] = useState<PublicPost | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const activeRequest = useRef<AbortController | null>(null);

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

  if (loading) return <TimelineLoading />;
  if (error) {
    return <PublicError kind={detailErrorKind(error)} onRetry={load} />;
  }
  return post ? (
    <>
      <PostPresentation
        detail
        onDeleted={() => navigate("/")}
        owner={owner}
        post={post}
        site={site}
      />
      <CommentThread api={api} postId={post.id} site={site} />
    </>
  ) : null;
}
