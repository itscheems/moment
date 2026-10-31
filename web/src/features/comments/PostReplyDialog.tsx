import { useTranslation } from "react-i18next";
import { AccountName } from "../../components/AccountName";
import { useSession } from "../auth/SessionProvider";
import { useLoginNavigation } from "../auth/loginNavigation";
import { Dialog } from "../../components/Dialog";
import {
  type RefObject,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import {
  PublicApi,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { postAuthorSite, SiteAvatar } from "../../components/SiteIdentity";
import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { MarkdownBody } from "../../components/content/MarkdownBody";
import { CommentForm, SignInToReply, useCanComment } from "./CommentComposer";
import styles from "./Comments.module.css";

export function PostReplyDialog({
  api,
  commentsEnabled,
  content,
  onClosed,
  onContentChange,
  onCreated,
  onOpenChange,
  open,
  post,
  comment,
  site,
  triggerRef,
  keepMounted = false,
  formIdPrefix,
}: {
  api: PublicApi;
  commentsEnabled: boolean;
  content: string;
  onClosed?: () => void;
  onContentChange: (value: string) => void;
  onCreated: (comment: PublicComment) => void;
  onOpenChange: (open: boolean) => void;
  open: boolean;
  post: PublicPost;
  comment?: PublicComment;
  site: SiteIdentity;
  triggerRef?: RefObject<HTMLButtonElement | null>;
  keepMounted?: boolean;
  formIdPrefix?: string;
}) {
  const { t } = useTranslation();

  const auth = useSession();
  const { requestLogin } = useLoginNavigation();
  const canCompose = useCanComment();
  const author = postAuthorSite(comment ?? post, site);
  const [closedByServer, setClosedByServer] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const canComment = commentsEnabled && !closedByServer;

  useDocumentScrollLock(open && auth.session !== null);
  useEffect(() => {
    if (open && auth.state === "guest") requestLogin();
  }, [open, auth.state, requestLogin]);

  useEffect(() => setClosedByServer(false), [post.id, commentsEnabled]);

  const initialFocus = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea) return null;
    const caret = textarea.value.length;
    textarea.setSelectionRange(caret, caret);
    return textarea;
  }, []);

  return (
    <Dialog.Root
      onOpenChange={onOpenChange}
      open={open && auth.session !== null}
    >
      <Dialog.Portal keepMounted={keepMounted}>
        <Dialog.Backdrop className={styles.dialogBackdrop} />
        <Dialog.Viewport className={styles.dialogViewport}>
          <Dialog.Popup
            className={styles.replyDialogPopup}
            finalFocus={() => triggerRef?.current ?? true}
            initialFocus={canCompose ? initialFocus : undefined}
          >
            <header className={styles.replyDialogHeader}>
              <Dialog.Close
                aria-label={t("关闭回复窗口")}
                className={styles.replyDialogClose}
              >
                <Icon name="close" />
              </Dialog.Close>
              <Dialog.Title className={styles.srOnly}>
                {t("回复这条 Moment")}
              </Dialog.Title>
            </header>
            <article
              aria-label={t("{{value1}}发布的 Moment", {
                value1: author.displayName,
              })}
              className={styles.replyContext}
            >
              <span aria-hidden="true" className={styles.replyContextAvatar}>
                <SiteAvatar site={author} />
              </span>
              <div>
                <p className={styles.replyContextByline}>
                  <strong>
                    <AccountName identity={author} />
                  </strong>
                  {author.username ? <span>@{author.username}</span> : null}
                </p>
                <MarkdownBody
                  source={{
                    kind: comment ? "comment" : "post",
                    id: comment?.id ?? post.id,
                  }}
                  className={styles.replyContextContent}
                  html={comment?.contentHtml ?? post.contentHtml}
                />
              </div>
            </article>
            {canCompose ? (
              <CommentForm
                replyParticipantLabel={
                  author.username ? "@" + author.username : author.displayName
                }
                api={api}
                commentsEnabled={canComment}
                content={content}
                formId={
                  formIdPrefix
                    ? "dialog-" + formIdPrefix
                    : comment
                      ? "dialog-comment-" + comment.id
                      : "dialog-top-level-comment"
                }
                initialExpanded
                onClosed={() => {
                  setClosedByServer(true);
                  onClosed?.();
                }}
                onCreated={(comment) => {
                  onCreated(comment);
                  onOpenChange(false);
                }}
                parentId={comment ? (comment.parentId ?? comment.id) : null}
                replyTargetId={comment?.id}
                replyTargetAuthor={
                  comment
                    ? author.username
                      ? "@" + author.username
                      : author.displayName
                    : undefined
                }
                postId={post.id}
                setContent={onContentChange}
                site={site}
                textareaRef={textareaRef}
              />
            ) : (
              <SignInToReply />
            )}
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
