import { useEffect, useRef, useState } from "react";
import { Menu } from "@base-ui/react/menu";

import type { PublicPost } from "../../api/public";
import { notify } from "../../components/Feedback";
import { Icon } from "../../components/icons";
import { ActionTooltip } from "../../components/TimelineInteractions";
import { useNavigation } from "../../app/navigation";
import postStyles from "../posts/PostPresentation.module.css";
import styles from "./Owner.module.css";
import { useOwner } from "./OwnerProvider";
import { announceOwnerPost } from "./postEvents";

export function OwnerRepostAction({
  onChanged,
  post,
}: {
  onChanged?: (post: PublicPost) => void;
  post: PublicPost;
}) {
  const { navigateOverlay } = useNavigation();
  const { api, session, expireIfInvalidSession } = useOwner();
  const [current, setCurrent] = useState(post);
  const [pending, setPending] = useState(false);
  const pendingRef = useRef(false);

  useEffect(() => setCurrent(post), [post]);

  if (!session) return null;

  const active = current.repostedAt !== null;
  const change = async (nextActive: boolean) => {
    if (pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    try {
      const updated = await api.setReposted(
        session.csrfToken,
        post.id,
        nextActive,
      );
      setCurrent(updated);
      if (onChanged) {
        onChanged(updated);
      } else {
        announceOwnerPost({ kind: "reordered", post: updated });
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
      <ActionTooltip label={active ? "转发选项" : "转发"}>
        <Menu.Trigger
          aria-label="转发这条 Moment"
          aria-pressed={active}
          className={`${postStyles.postAction} ${postStyles.repostAction} ${active ? postStyles.repostActive : ""}`}
          data-focus-key={`${post.id}:${post.timelineItemKind}:repost`}
          disabled={pending}
          onClick={(event) => event.stopPropagation()}
        >
          <Icon name="repost" />
        </Menu.Trigger>
      </ActionTooltip>
      <Menu.Portal>
        <Menu.Positioner
          align="start"
          className={styles.ownerMenuPositioner}
          side="bottom"
          sideOffset={4}
        >
          <Menu.Popup
            aria-label="转发选项"
            className={styles.ownerMenuPopup}
            onClick={(event) => event.stopPropagation()}
          >
            <Menu.Item
              className={styles.ownerMenuItem}
              onClick={() => void change(!active)}
            >
              <Icon name="repost" />
              <span>{active ? "取消转发" : "转发"}</span>
            </Menu.Item>
            <Menu.Item
              className={styles.ownerMenuItem}
              onClick={() =>
                navigateOverlay(
                  `/owner/compose?quote=${encodeURIComponent(post.id)}`,
                )
              }
            >
              <Icon name="edit" />
              <span>引用 Moment</span>
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
