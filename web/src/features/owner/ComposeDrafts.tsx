import { useCallback, useEffect, useMemo, useState } from "react";

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
 * finished. 编辑 turns it into a selection -- a checkbox per row, 全选, and a
 * 删除 that stays out of reach until something is picked.
 */
export function ComposeDrafts({
  editing,
  instanceId,
  onEditingChange,
  onOpenDraft,
}: {
  editing: boolean;
  instanceId: string | null;
  onEditingChange: (editing: boolean) => void;
  onOpenDraft: (draft: KeptDraft) => void;
}) {
  const [drafts, setDrafts] = useState<KeptDraft[]>(() =>
    listKeptDrafts(instanceId),
  );
  const [picked, setPicked] = useState<ReadonlySet<string>>(() => new Set());
  const refresh = useCallback(
    () => setDrafts(listKeptDrafts(instanceId)),
    [instanceId],
  );

  // Another tab may have kept or published one while this view was open.
  useEffect(() => {
    window.addEventListener("storage", refresh);
    return () => window.removeEventListener("storage", refresh);
  }, [refresh]);

  // Nothing left to edit; and leaving the selection forgets it.
  useEffect(() => {
    if (drafts.length === 0 && editing) onEditingChange(false);
  }, [drafts.length, editing, onEditingChange]);
  useEffect(() => {
    if (!editing) setPicked(new Set());
  }, [editing]);

  const pickedHere = useMemo(
    () => drafts.filter((draft) => picked.has(draft.id)),
    [drafts, picked],
  );
  const allPicked = drafts.length > 0 && pickedHere.length === drafts.length;

  // The reference splits this row between "Unsent posts" and "Scheduled".
  // Only the first exists here, so it takes the row; a second tab splits it
  // without touching anything else.
  const tabs = (
    <nav aria-label="草稿" className={styles.draftsTabs}>
      <div role="tablist">
        <button aria-selected="true" role="tab" type="button">
          <span>未发布</span>
        </button>
      </div>
    </nav>
  );

  if (drafts.length === 0) {
    return (
      <>
        {tabs}
        <p className={styles.draftsEmpty}>
          这台浏览器里还没有草稿。退出发布时选择「保留草稿并退出」，文字就会留在这里。
        </p>
      </>
    );
  }

  return (
    <>
      {tabs}
      <ol className={styles.draftsList}>
        {drafts.map((draft) => (
          <li key={draft.id}>
            {editing ? (
              // The halo is the only thing that answers the pointer; the box
              // itself keeps its own colour, as the reference's does.
              <label className={styles.draftsCheck}>
                <input
                  checked={picked.has(draft.id)}
                  onChange={(event) =>
                    setPicked((current) => {
                      const next = new Set(current);
                      if (event.target.checked) next.add(draft.id);
                      else next.delete(draft.id);
                      return next;
                    })
                  }
                  type="checkbox"
                />
                <span aria-hidden="true">
                  <Icon name="audience-check" />
                </span>
                <span className={styles.srOnly}>
                  选择草稿：{preview(draft.contentMd).slice(0, 24)}
                </span>
              </label>
            ) : null}
            {/* Two controls side by side rather than one inside the other:
                a delete button nested in a row button is not a thing a
                browser can dispatch unambiguously. */}
            <button
              className={styles.draftsOpen}
              disabled={editing}
              onClick={() => onOpenDraft(draft)}
              type="button"
            >
              <span>{preview(draft.contentMd)}</span>
              <time dateTime={draft.savedAt}>
                {new Date(draft.savedAt).toLocaleString("zh-CN")}
              </time>
            </button>
            {editing ? null : (
              // Deleting was one press with nothing between it and the only
              // copy of that text.
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
            )}
          </li>
        ))}
      </ol>
      {editing ? (
        <div className={styles.draftsSelection}>
          <button
            className={styles.draftsSelectAll}
            onClick={() =>
              setPicked(
                allPicked ? new Set() : new Set(drafts.map((one) => one.id)),
              )
            }
            type="button"
          >
            {allPicked ? "取消全选" : "全选"}
          </button>
          {/* Disabled until something is picked, so it cannot be hovered
              either -- there is nothing for it to do yet. */}
          <ConfirmDialog
            confirmLabel="删除"
            danger
            description={`这 ${pickedHere.length} 段文字只存在这台浏览器里，删除后无法找回。`}
            onConfirm={() => {
              for (const one of pickedHere) dropKeptDraft(instanceId, one.id);
              setPicked(new Set());
              refresh();
            }}
            title="删除选中的草稿？"
            trigger={
              <button
                className={styles.draftsDeletePicked}
                disabled={pickedHere.length === 0}
                type="button"
              >
                删除
              </button>
            }
          />
        </div>
      ) : null}
    </>
  );
}
