import { Dialog } from "@base-ui/react/dialog";
import { Menu } from "@base-ui/react/menu";
import { useRef, useState } from "react";

import type { PublicPost } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
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
  const { navigate } = useNavigation();
  const { api, session, expireIfInvalidSession } = useOwner();
  const [menuOpen, setMenuOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pendingRef = useRef(false);
  const triggerRef = useRef<HTMLButtonElement>(null);

  if (!session) return null;

  const restoreMenuFocus = () => {
    window.requestAnimationFrame(() => triggerRef.current?.focus());
  };

  const remove = async () => {
    if (pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      const deleted = await api.delete(session.csrfToken, post.id);
      announceOwnerPost({ kind: "deleted", post: deleted });
      onDeleted?.(deleted);
      setDeleteOpen(false);
    } catch (nextError) {
      setError(
        expireIfInvalidSession(nextError)
          ? "登录已过期，重新登录后再删除。"
          : "没有删除这个 Moment，可以安全重试。",
      );
      setDeleteOpen(false);
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <div
      className={styles.ownerPostMenu}
      onClick={(event) => event.stopPropagation()}
    >
      <Menu.Root onOpenChange={setMenuOpen} open={menuOpen}>
        <Menu.Trigger
          aria-label="更多操作"
          className={styles.ownerMenuTrigger}
          ref={triggerRef}
        >
          <Icon name="more" />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner
            align="end"
            className={styles.ownerMenuPositioner}
            side="bottom"
            sideOffset={4}
          >
            <Menu.Popup
              aria-label="Moment 操作"
              className={styles.ownerMenuPopup}
            >
              <Menu.Item
                className={styles.ownerMenuItem}
                onClick={() => {
                  setMenuOpen(false);
                  navigate(`/owner/m/${encodeURIComponent(post.id)}/edit`);
                }}
              >
                <Icon name="edit" />
                <span>编辑</span>
              </Menu.Item>
              <Menu.Item
                className={`${styles.ownerMenuItem} ${styles.ownerMenuDanger}`}
                onClick={() => {
                  setMenuOpen(false);
                  setDeleteOpen(true);
                }}
              >
                <Icon name="delete" />
                <span>删除</span>
              </Menu.Item>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>

      <Dialog.Root
        onOpenChange={(open) => {
          setDeleteOpen(open);
          if (!open) restoreMenuFocus();
        }}
        open={deleteOpen}
      >
        <Dialog.Portal>
          <Dialog.Backdrop className={styles.dialogBackdrop} />
          <Dialog.Viewport className={styles.dialogViewport}>
            <Dialog.Popup className={styles.dialogPopup}>
              <Dialog.Title className={styles.dialogTitle}>
                删除这段 Moment？
              </Dialog.Title>
              <Dialog.Description className={styles.dialogDescription}>
                这段 Moment 会从公开时间流中移除。此操作不能撤销。
              </Dialog.Description>
              <div className={styles.dialogActions}>
                <Dialog.Close
                  className={styles.secondaryButton}
                  disabled={pending}
                >
                  保留
                </Dialog.Close>
                <button
                  className={styles.dangerButton}
                  disabled={pending}
                  onClick={() => void remove()}
                  type="button"
                >
                  {pending ? "正在处理…" : "删除 Moment"}
                </button>
              </div>
            </Dialog.Popup>
          </Dialog.Viewport>
        </Dialog.Portal>
      </Dialog.Root>

      {error ? (
        <span className={styles.ownerActionError} role="alert">
          {error}
        </span>
      ) : null}
    </div>
  );
}
