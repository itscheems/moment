import { postsApi } from "../../api/posts";
import { useEffect, useRef, useState } from "react";
import { Menu } from "@base-ui/react/menu";

import type { PublicPost } from "../../api/public";
import { notify } from "../../components/Feedback";
import { Icon } from "../../components/icons";
import { ActionTooltip } from "../../components/TimelineInteractions";
import { useNavigation } from "../../app/navigation";
import { COMPOSE_PATH } from "../../app/routes";
import postStyles from "./PostPresentation.module.css";
import styles from "../../components/Forms.module.css";
import { useSession } from "../auth/SessionProvider";
import { announcePostChanged } from "./postEvents";

export function RepostAction({
  onChanged,
  post,
  tooltipVariant,
}: {
  onChanged?: (post: PublicPost) => void;
  post: PublicPost;
  tooltipVariant?: "media";
}) {
  const { navigateOverlay } = useNavigation();
  const { account, session, expireIfInvalidSession } = useSession();
  const [current, setCurrent] = useState(post);
  const [pending, setPending] = useState(false);
  const pendingRef = useRef(false);

  useEffect(() => setCurrent(post), [post]);

  if (!session || account?.status !== "active") return null;
  const isComment = post.commentPostId !== undefined;
  const isAuthor = !isComment && account.userId === post.author?.userId;

  const active = !isComment && current.repostedAt !== null;
  const change = async (nextActive: boolean) => {
    if (pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    try {
      const updated = await postsApi.setReposted(
        session.csrfToken,
        post.id,
        nextActive,
      );
      setCurrent(updated);
      if (onChanged) {
        onChanged(updated);
      } else {
        announcePostChanged({ kind: "reordered", post: updated });
      }
      notify(nextActive ? "已转发到时间流顶部" : "已取消转发");
    } catch (error) {
      notify(
        expireIfInvalidSession(error)
          ? "登录已过期，重新登录后再操作。"
          : "转发状态没有改变，可以安全重试。",
        { type: "error" },
      );
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <Menu.Root>
      <ActionTooltip
        label={active ? "转发选项" : "转发"}
        variant={tooltipVariant}
      >
        <Menu.Trigger
          data-post-action="repost"
          aria-label={isComment ? "转发这条评论" : "转发这条 Moment"}
          aria-pressed={active}
          className={`${postStyles.postAction} ${postStyles.repostAction} ${active ? postStyles.repostActive : ""}`}
          data-focus-key={`${post.id}:${post.timelineItemKind}:repost`}
          disabled={pending}
          onClick={(event) => event.stopPropagation()}
        >
          <Icon name="repost" />
          {active ? (
            <span className={postStyles.actionCount} data-repost-count>
              1
            </span>
          ) : null}
        </Menu.Trigger>
      </ActionTooltip>
      <Menu.Portal>
        <Menu.Positioner
          align="start"
          className={styles.actionsMenuPositioner}
          side="bottom"
          sideOffset={4}
        >
          <Menu.Popup
            aria-label="转发选项"
            className={styles.actionsMenuPopup}
            onClick={(event) => event.stopPropagation()}
          >
            {isAuthor ? (
              <Menu.Item
                className={styles.actionsMenuItem}
                onClick={() => void change(!active)}
              >
                <Icon name="repost" />
                <span>{active ? "取消转发" : "转发"}</span>
              </Menu.Item>
            ) : null}
            <Menu.Item
              className={styles.actionsMenuItem}
              onClick={() =>
                navigateOverlay(
                  isComment
                    ? `${COMPOSE_PATH}?quoteComment=${encodeURIComponent(post.id)}&quotePost=${encodeURIComponent(post.commentPostId!)}`
                    : `${COMPOSE_PATH}?quote=${encodeURIComponent(post.id)}`,
                )
              }
            >
              <Icon name="edit" />
              <span>{isComment ? "引用评论" : "引用 Moment"}</span>
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
