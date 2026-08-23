import { Dialog } from "@base-ui/react/dialog";
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
import { SiteAvatar } from "../../components/SiteIdentity";
import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { MarkdownContent } from "../../components/MarkdownContent";
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
  site,
  triggerRef,
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
  site: SiteIdentity;
  triggerRef?: RefObject<HTMLButtonElement | null>;
}) {
  const canCompose = useCanComment();
  const [closedByServer, setClosedByServer] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const canComment = commentsEnabled && !closedByServer;

  useDocumentScrollLock(open);

  useEffect(() => setClosedByServer(false), [post.id, commentsEnabled]);

  const initialFocus = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea) return null;
    const caret = textarea.value.length;
    textarea.setSelectionRange(caret, caret);
    return textarea;
  }, []);

  return (
    <Dialog.Root onOpenChange={onOpenChange} open={open}>
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.dialogBackdrop} />
        <Dialog.Viewport className={styles.dialogViewport}>
          <Dialog.Popup
            className={styles.replyDialogPopup}
            finalFocus={() => triggerRef?.current ?? true}
            initialFocus={canCompose ? initialFocus : undefined}
          >
            <header className={styles.replyDialogHeader}>
              <Dialog.Close
                aria-label="关闭回复窗口"
                className={styles.replyDialogClose}
              >
                <Icon name="close" />
              </Dialog.Close>
              <Dialog.Title className={styles.srOnly}>
                回复这条 Moment
              </Dialog.Title>
            </header>
            <article
              aria-label={`${site.displayName}发布的 Moment`}
              className={styles.replyContext}
            >
              <span aria-hidden="true" className={styles.replyContextAvatar}>
                <SiteAvatar site={site} />
              </span>
              <div>
                <p className={styles.replyContextByline}>
                  <strong>{site.displayName}</strong>
                  <span>@{site.username}</span>
                </p>
                <MarkdownContent
                  className={styles.replyContextContent}
                  html={post.contentHtml}
                />
                <p className={styles.replyContextTarget}>
                  正在回复 <span>@{site.username}</span>
                </p>
              </div>
            </article>
            {canCompose ? (
              <CommentForm
                api={api}
                commentsEnabled={canComment}
                content={content}
                formId="dialog-top-level-comment"
                initialExpanded
                onClosed={() => {
                  setClosedByServer(true);
                  onClosed?.();
                }}
                onCreated={(comment) => {
                  onCreated(comment);
                  onOpenChange(false);
                }}
                parentId={null}
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
