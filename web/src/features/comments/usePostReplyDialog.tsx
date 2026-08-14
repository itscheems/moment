import {
  type MouseEvent,
  type ReactNode,
  useCallback,
  useRef,
  useState,
} from "react";

import {
  type PublicApi,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { PostReplyDialog } from "./CommentThread";

export function usePostReplyDialog({
  api,
  onCreated,
  site,
}: {
  api: PublicApi;
  onCreated: (comment: PublicComment) => void;
  site: SiteIdentity;
}): {
  openReply: (post: PublicPost, event: MouseEvent<HTMLButtonElement>) => void;
  replyDialog: ReactNode;
} {
  const [open, setOpen] = useState(false);
  const [post, setPost] = useState<PublicPost | null>(null);
  const [content, setContent] = useState("");
  const [guestName, setGuestName] = useState("");
  const triggerRef = useRef<HTMLButtonElement>(null);

  const openReply = useCallback(
    (nextPost: PublicPost, event: MouseEvent<HTMLButtonElement>) => {
      triggerRef.current = event.currentTarget;
      if (post?.id !== nextPost.id) setContent("");
      setPost(nextPost);
      setOpen(true);
    },
    [post?.id],
  );

  const created = useCallback(
    (comment: PublicComment) => {
      setPost((current) =>
        current?.id === comment.postId
          ? { ...current, commentCount: current.commentCount + 1 }
          : current,
      );
      onCreated(comment);
    },
    [onCreated],
  );

  return {
    openReply,
    replyDialog: post ? (
      <PostReplyDialog
        api={api}
        commentsEnabled={site.commentsEnabled}
        content={content}
        guestName={guestName}
        onContentChange={setContent}
        onCreated={created}
        onGuestNameChange={setGuestName}
        onOpenChange={setOpen}
        open={open}
        post={post}
        site={site}
        triggerRef={triggerRef}
      />
    ) : null,
  };
}
