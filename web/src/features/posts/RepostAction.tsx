import { useTranslation } from "react-i18next";
import { postsApi } from "../../api/posts";
import { useEffect, useRef, useState } from "react";
import { Menu } from "@base-ui/react/menu";

import type { PublicPost } from "../../api/public";
import { notify } from "../../components/Feedback";
import { Icon } from "../../components/icons";
import {
  ActionTooltip,
  type ActionTooltipPlacement,
} from "../../components/TimelineInteractions";
import { useNavigation } from "../../app/navigation";
import { COMPOSE_PATH } from "../../app/routes";
import postStyles from "./PostPresentation.module.css";
import styles from "../../components/Forms.module.css";
import { useOptionalSession } from "../auth/SessionProvider";
import { useLoginNavigation } from "../auth/loginNavigation";
import { announcePostChanged } from "./postEvents";

export function RepostAction({
  onChanged,
  post,
  tooltipPlacement,
}: {
  onChanged?: (post: PublicPost) => void;
  post: PublicPost;
  tooltipPlacement?: ActionTooltipPlacement;
}) {
  const { t } = useTranslation();

  const { navigateOverlay } = useNavigation();
  const auth = useOptionalSession();
  const account = auth?.account;
  const { requestLogin } = useLoginNavigation();
  const [open, setOpen] = useState(false);
  const [current, setCurrent] = useState(post);
  const [pending, setPending] = useState(false);
  const pendingRef = useRef(false);

  useEffect(() => setCurrent(post), [post]);

  const isComment = post.commentPostId !== undefined;
  const isAuthor =
    !isComment && Boolean(account && account.userId === post.author?.userId);

  const requireActiveSession = () => {
    if (!auth?.session || auth.state !== "signed-in") {
      requestLogin();
      return null;
    }
    if (account?.status !== "active") {
      notify(t("账号通过审核后即可转发"), { type: "error" });
      return null;
    }
    return auth.session;
  };

  const active = !isComment && current.repostedAt !== null;
  const change = async (nextActive: boolean) => {
    const session = requireActiveSession();
    if (!session || pendingRef.current) return;
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
      notify(nextActive ? t("已转发到时间流顶部") : t("已取消转发"));
    } catch (error) {
      notify(
        auth?.expireIfInvalidSession(error)
          ? t("登录已过期，重新登录后再操作。")
          : t("转发状态没有改变，可以安全重试。"),
        { type: "error" },
      );
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <Menu.Root
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen || requireActiveSession()) setOpen(nextOpen);
      }}
    >
      <ActionTooltip
        label={active ? t("转发选项") : t("转发")}
        placement={tooltipPlacement}
      >
        <Menu.Trigger
          data-post-action="repost"
          aria-label={isComment ? t("转发这条评论") : t("转发这条 Moment")}
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
            aria-label={t("转发选项")}
            className={styles.actionsMenuPopup}
            onClick={(event) => event.stopPropagation()}
          >
            {isAuthor ? (
              <Menu.Item
                className={styles.actionsMenuItem}
                onClick={() => void change(!active)}
              >
                <Icon name="repost" />
                <span>{active ? t("取消转发") : t("转发")}</span>
              </Menu.Item>
            ) : null}
            <Menu.Item
              className={styles.actionsMenuItem}
              onClick={() => {
                if (!requireActiveSession()) return;
                navigateOverlay(
                  isComment
                    ? `${COMPOSE_PATH}?quoteComment=${encodeURIComponent(post.id)}&quotePost=${encodeURIComponent(post.commentPostId!)}`
                    : `${COMPOSE_PATH}?quote=${encodeURIComponent(post.id)}`,
                );
              }}
            >
              <Icon name="edit" />
              <span>{isComment ? t("引用评论") : t("引用 Moment")}</span>
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
