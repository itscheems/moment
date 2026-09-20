import { useCallback, useEffect, useState } from "react";

import { Icon } from "../../components/icons";
import { ConfirmDialog } from "./ConfirmDialog";
import { dropKeptDraft, type KeptDraft, listKeptDrafts } from "./drafts";
import styles from "./Owner.module.css";

function preview(contentMd: string): string {
  const flattened = contentMd.replace(/\s+/g, " ").trim();
  return flattened.length > 140 ? `${flattened.slice(0, 140)}…` : flattened;
}

/**
 * The 草稿 view inside the compose dialog: the text the owner chose to keep
 * rather than publish, newest first, each one a press away from being
 * finished.
 */
export function ComposeDrafts({
  instanceId,
  onOpenDraft,
}: {
  instanceId: string | null;
  onOpenDraft: (draft: KeptDraft) => void;
}) {
  const [drafts, setDrafts] = useState<KeptDraft[]>(() =>
    listKeptDrafts(instanceId),
  );
  const refresh = useCallback(
    () => setDrafts(listKeptDrafts(instanceId)),
    [instanceId],
  );

  // Another tab may have kept or published one while this view was open.
  useEffect(() => {
    window.addEventListener("storage", refresh);
    return () => window.removeEventListener("storage", refresh);
  }, [refresh]);

  if (drafts.length === 0) {
    return (
      <p className={styles.draftsEmpty}>
        这台浏览器里还没有草稿。退出发布时选择「保留草稿并退出」，文字就会留在这里。
      </p>
    );
  }

  return (
    <ol className={styles.draftsList}>
      {drafts.map((draft) => (
        <li key={draft.id}>
          {/* Two controls side by side rather than one inside the other:
              a delete button nested in a row button is not a thing a
              browser can dispatch unambiguously. */}
          <button
            className={styles.draftsOpen}
            onClick={() => onOpenDraft(draft)}
            type="button"
          >
            <span>{preview(draft.contentMd)}</span>
            <time dateTime={draft.savedAt}>
              {new Date(draft.savedAt).toLocaleString("zh-CN")}
            </time>
          </button>
          {/* Deleting was one press with nothing between it and the only
              copy of that text. */}
          <ConfirmDialog
            confirmLabel="删除草稿"
            danger
            description="这段文字只存在这台浏览器里，删除后无法找回。"
            onConfirm={() => {
              dropKeptDraft(instanceId, draft.id);
              refresh();
            }}
            title="删除这份草稿？"
            trigger={
              <button
                aria-label={`删除草稿：${preview(draft.contentMd).slice(0, 24)}`}
                className={styles.draftsDelete}
                type="button"
              >
                <Icon name="delete" />
              </button>
            }
          />
        </li>
      ))}
    </ol>
  );
}
