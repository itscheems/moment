import { useRef, useState } from "react";

import type { PublicPost } from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { ConfirmDialog } from "./ConfirmDialog";
import { useOwner } from "./OwnerProvider";
import { announceOwnerPost } from "./postEvents";
import styles from "./Owner.module.css";

export function OwnerPostActions({
  post,
  onDeleted,
}: {
  post: PublicPost;
  onDeleted?: (post: PublicPost) => void;
}) {
  const { api, session, expireIfUnauthorized } = useOwner();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pendingRef = useRef(false);

  if (!session) return null;

  const remove = async () => {
    if (pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      const deleted = await api.delete(session.csrfToken, post.id);
      announceOwnerPost({ kind: "deleted", post: deleted });
      onDeleted?.(deleted);
    } catch (nextError) {
      setError(
        expireIfUnauthorized(nextError)
          ? "登录已过期，重新登录后再删除。"
          : "没有删除这个 Moment，可以安全重试。",
      );
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <div className={styles.postActions}>
      <InternalLink href={`/owner/m/${encodeURIComponent(post.id)}/edit`}>
        编辑
      </InternalLink>
      <ConfirmDialog
        confirmLabel="删除 Moment"
        danger
        description="这段 Moment 会从公开时间流中移除。此操作不能撤销。"
        onConfirm={remove}
        pending={pending}
        title="删除这段 Moment？"
        trigger={<button type="button">删除</button>}
      />
      {error ? <span role="alert">{error}</span> : null}
    </div>
  );
}
