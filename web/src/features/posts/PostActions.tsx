import { useTranslation } from "react-i18next";
import { postsApi } from "../../api/posts";
import { Dialog } from "../../components/Dialog";
import { Menu } from "@base-ui/react/menu";
import { useRef, useState } from "react";

import type { PublicPost, SiteIdentity } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { notify } from "../../components/Feedback";
import { Icon } from "../../components/icons";
import { Button } from "../../components/content/notion/components/primitives/button/button";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { isAdministrator, useSession } from "../auth/SessionProvider";
import { articleDraftPath } from "../../app/routes";
import {
  ArticleDraftApi,
  articleDraftErrorMessage,
} from "../article/articleDrafts";
import { announcePostChanged } from "./postEvents";
import styles from "../../components/Forms.module.css";

export function PostActions({
  post,
  site,
  onDeleted,
  onSiteChanged,
  showArticleEdit = false,
}: {
  post: PublicPost;
  site: SiteIdentity;
  onDeleted?: (post: PublicPost) => void;
  onSiteChanged?: (site: SiteIdentity) => void;
  showArticleEdit?: boolean;
}) {
  const { t } = useTranslation();

  const { navigate } = useNavigation();
  const { account, session, expireIfInvalidSession } = useSession();
  const [menuOpen, setMenuOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [pinOpen, setPinOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pendingRef = useRef(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const pinned = site.pinnedPostId === post.id;

  useDocumentScrollLock(Boolean(session) && (deleteOpen || pinOpen));

  const isAuthor =
    account?.userId !== undefined && account.userId === post.author?.userId;
  const canModerate =
    isAdministrator(account) && post.visibility === "everyone";
  if (!session || account?.status !== "active" || (!isAuthor && !canModerate))
    return null;

  const restoreMenuFocus = () => {
    window.requestAnimationFrame(() => triggerRef.current?.focus());
  };

  /**
   * Editing an article is not editing a Moment.
   *
   * A Moment is its Markdown and the edit page changes it in place. An article
   * is a document, and what is edited is a working copy that changes nothing a
   * reader sees until it is published in turn -- so this asks the server for
   * that copy and goes to it, rather than opening a Markdown box over a body
   * that is not Markdown.
   */
  const editArticle = async () => {
    if (pendingRef.current || !session) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      const draft = await new ArticleDraftApi().openPublished(
        session.csrfToken,
        post.id,
      );
      navigate(articleDraftPath(draft.id));
    } catch (failure) {
      expireIfInvalidSession(failure);
      setError(articleDraftErrorMessage(failure));
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  const remove = async () => {
    if (pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      const deleted = await postsApi.delete(session.csrfToken, post.id);
      announcePostChanged({ kind: "deleted", post: deleted });
      onDeleted?.(deleted);
      if (pinned) onSiteChanged?.({ ...site, pinnedPostId: null });
      setDeleteOpen(false);
      notify(t("Moment 已删除"));
    } catch (nextError) {
      setError(
        expireIfInvalidSession(nextError)
          ? t("登录已过期，重新登录后再删除。")
          : t("没有删除这个 Moment，可以安全重试。"),
      );
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  const changePin = async () => {
    if (pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      const updated = pinned
        ? await postsApi.unpinPost(session.csrfToken, post.id)
        : await postsApi.pinPost(session.csrfToken, post.id);
      onSiteChanged?.({
        ...updated,
        instanceId: site.instanceId,
        instanceInitializedAt: site.instanceInitializedAt,
      });
      setPinOpen(false);
      notify(pinned ? t("已取消置顶") : t("已置顶到个人资料"));
    } catch (nextError) {
      setError(
        expireIfInvalidSession(nextError)
          ? t("登录已过期，重新登录后再操作。")
          : t("置顶状态没有改变，可以安全重试。"),
      );
      setPinOpen(false);
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <div
      className={`${styles.postMenu} ${showArticleEdit ? styles.articleActions : ""}`}
      onClick={(event) => event.stopPropagation()}
    >
      {isAuthor && showArticleEdit && post.kind === "article" ? (
        <Button
          data-style="ghost"
          disabled={pending}
          onClick={() => void editArticle()}
          type="button"
        >
          <Icon name="edit" className="tiptap-button-icon" />
          <span className="tiptap-button-text">
            {pending ? t("正在打开…") : t("编辑文章")}
          </span>
        </Button>
      ) : null}
      <Menu.Root onOpenChange={setMenuOpen} open={menuOpen}>
        <Menu.Trigger
          aria-label={t("更多操作")}
          className={styles.actionsMenuTrigger}
          ref={triggerRef}
        >
          <Icon name="ellipsis" />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner
            align="end"
            className={styles.actionsMenuPositioner}
            side="bottom"
            sideOffset={4}
          >
            <Menu.Popup
              aria-label={t("Moment 操作")}
              className={styles.actionsMenuPopup}
            >
              {isAuthor ? (
                <Menu.Item
                  className={styles.actionsMenuItem}
                  disabled={pending}
                  onClick={() => {
                    setMenuOpen(false);
                    if (post.kind === "article") {
                      void editArticle();
                      return;
                    }
                    navigate(`/m/${encodeURIComponent(post.id)}/edit`);
                  }}
                >
                  <Icon name="edit" />
                  <span>
                    {post.kind === "article" ? t("编辑文章") : t("编辑")}
                  </span>
                </Menu.Item>
              ) : null}
              {canModerate ? (
                <Menu.Item
                  className={styles.actionsMenuItem}
                  onClick={() => {
                    setMenuOpen(false);
                    setPinOpen(true);
                  }}
                >
                  <Icon name="pin" />
                  <span>{pinned ? t("取消置顶") : t("置顶到个人资料")}</span>
                </Menu.Item>
              ) : null}
              <Menu.Item
                className={`${styles.actionsMenuItem} ${styles.actionsMenuDanger}`}
                onClick={() => {
                  setMenuOpen(false);
                  setError(null);
                  setDeleteOpen(true);
                }}
              >
                <Icon name="delete" />
                <span>{t("删除")}</span>
              </Menu.Item>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>

      <Dialog.Root
        onOpenChange={(open) => {
          setDeleteOpen(open);
          if (!open) {
            setError(null);
            restoreMenuFocus();
          }
        }}
        open={deleteOpen}
      >
        <Dialog.Portal>
          <Dialog.Backdrop className={styles.dialogBackdrop} />
          <Dialog.Viewport className={styles.dialogViewport}>
            <Dialog.Popup className={styles.dialogPopup}>
              <Dialog.Title className={styles.dialogTitle}>
                {t("删除这段 Moment？")}
              </Dialog.Title>
              <Dialog.Description className={styles.dialogDescription}>
                {t("这段 Moment 会从公开时间流中移除。此操作不能撤销。")}
              </Dialog.Description>
              {error ? (
                <p className={styles.dialogError} role="alert">
                  {error}
                </p>
              ) : null}
              <div className={styles.dialogActions}>
                <Dialog.Close
                  className={styles.secondaryButton}
                  disabled={pending}
                >
                  {t("保留")}
                </Dialog.Close>
                <button
                  className={styles.dangerButton}
                  disabled={pending}
                  onClick={() => void remove()}
                  type="button"
                >
                  {pending
                    ? t("正在处理…")
                    : error
                      ? t("重试删除")
                      : t("删除 Moment")}
                </button>
              </div>
            </Dialog.Popup>
          </Dialog.Viewport>
        </Dialog.Portal>
      </Dialog.Root>

      <Dialog.Root
        onOpenChange={(open) => {
          setPinOpen(open);
          if (!open) restoreMenuFocus();
        }}
        open={pinOpen}
      >
        <Dialog.Portal>
          <Dialog.Backdrop className={styles.dialogBackdrop} />
          <Dialog.Viewport className={styles.dialogViewport}>
            <Dialog.Popup className={styles.dialogPopup}>
              <Dialog.Title className={styles.dialogTitle}>
                {pinned ? t("取消置顶这段 Moment？") : t("置顶这段 Moment？")}
              </Dialog.Title>
              <Dialog.Description className={styles.dialogDescription}>
                {pinned
                  ? t("它会回到个人资料中的原始时间位置。")
                  : site.pinnedPostId
                    ? t("它会显示在个人资料顶部，并替换当前置顶的 Moment。")
                    : t("它会显示在个人资料顶部，时间流顺序不会改变。")}
              </Dialog.Description>
              <div className={styles.dialogActions}>
                <Dialog.Close
                  className={styles.secondaryButton}
                  disabled={pending}
                >
                  {t("取消")}
                </Dialog.Close>
                <button
                  className={styles.primaryButton}
                  disabled={pending}
                  onClick={() => void changePin()}
                  type="button"
                >
                  {pending
                    ? t("正在处理…")
                    : pinned
                      ? t("取消置顶")
                      : t("置顶")}
                </button>
              </div>
            </Dialog.Popup>
          </Dialog.Viewport>
        </Dialog.Portal>
      </Dialog.Root>

      {error && !deleteOpen ? (
        <span className={styles.actionError} role="alert">
          {error}
        </span>
      ) : null}
    </div>
  );
}
