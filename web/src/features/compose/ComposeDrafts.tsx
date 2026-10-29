import { getLocale } from "../../i18n";
import { useTranslation } from "react-i18next";
import { useCallback, useEffect, useMemo, useState } from "react";

import { Icon } from "../../components/icons";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { dropKeptDraft, type KeptDraft, listKeptDrafts } from "./drafts";
import styles from "../../components/Forms.module.css";

function preview(contentMd: string): string {
  const flattened = contentMd.replace(/\s+/g, " ").trim();
  return flattened.length > 140 ? `${flattened.slice(0, 140)}…` : flattened;
}

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
  const { t } = useTranslation();

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
    <nav aria-label={t("草稿")} className={styles.draftsTabs}>
      <div role="tablist">
        <button aria-selected="true" role="tab" type="button">
          <span>{t("未发布")}</span>
        </button>
      </div>
    </nav>
  );

  if (drafts.length === 0) {
    return (
      <>
        {tabs}
        <p className={styles.draftsEmpty}>
          {t(
            "这台浏览器里还没有草稿。退出发布时选择「保留草稿并退出」，文字就会留在这里。",
          )}
        </p>
      </>
    );
  }

  return (
    <>
      {tabs}
      <ol className={styles.draftsList}>
        {drafts.map((draft) => (
          <li data-editing={editing || undefined} key={draft.id}>
            {/* One row element in both states, so entering the selection
                widens the checkbox's slot and pushes the text along rather
                than snapping a new layout into place. While editing the row
                *is* the checkbox -- a 22px box in a 68px row is a worse way
                to say the same thing -- and the halo is still the only part
                that answers the pointer on its own. */}
            <button
              aria-checked={editing ? picked.has(draft.id) : undefined}
              className={styles.draftsRow}
              data-editing={editing || undefined}
              onClick={() => {
                if (!editing) {
                  onOpenDraft(draft);
                  return;
                }
                setPicked((current) => {
                  const next = new Set(current);
                  if (next.has(draft.id)) next.delete(draft.id);
                  else next.add(draft.id);
                  return next;
                });
              }}
              role={editing ? "checkbox" : undefined}
              type="button"
            >
              <span aria-hidden="true" className={styles.draftsCheck}>
                <span>
                  <Icon name="audience-check" />
                </span>
              </span>
              <span className={styles.draftsText}>
                <span>
                  {preview(
                    draft.contentMd ||
                      (draft.mapCard
                        ? t("地图：{{value1}}", {
                            value1: draft.mapCard.places
                              .map((place) => place.name)
                              .join("、"),
                          })
                        : draft.quote
                          ? draft.quote.commentPostId
                            ? t("引用评论")
                            : t("引用 Moment")
                          : ""),
                  )}
                </span>
                <time dateTime={draft.savedAt}>
                  {new Date(draft.savedAt).toLocaleString(getLocale())}
                </time>
              </span>
            </button>
            {/* Deleting was one press with nothing between it and the only
                copy of that text. It folds away while editing, where the
                bottom bar's 删除 does the same job for a selection. */}
            <ConfirmDialog
              confirmLabel={t("删除草稿")}
              danger
              description={t("这段文字只存在这台浏览器里，删除后无法找回。")}
              onConfirm={() => {
                dropKeptDraft(instanceId, draft.id);
                refresh();
              }}
              title={t("删除这份草稿？")}
              trigger={
                <button
                  aria-label={t("删除草稿：{{value1}}", {
                    value1: preview(
                      draft.contentMd ||
                        (draft.mapCard
                          ? `地图：${draft.mapCard.places.map((place) => place.name).join("、")}`
                          : ""),
                    ).slice(0, 24),
                  })}
                  className={styles.draftsDelete}
                  disabled={editing}
                  type="button"
                >
                  <Icon name="delete" />
                </button>
              }
            />
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
            {allPicked ? t("取消全选") : t("全选")}
          </button>
          {/* Disabled until something is picked, so it cannot be hovered
              either -- there is nothing for it to do yet. */}
          <ConfirmDialog
            confirmLabel={t("删除")}
            danger
            description={t(
              "这 {{value1}} 段文字只存在这台浏览器里，删除后无法找回。",
              { value1: pickedHere.length },
            )}
            onConfirm={() => {
              for (const one of pickedHere) dropKeptDraft(instanceId, one.id);
              setPicked(new Set());
              refresh();
            }}
            title={t("删除选中的草稿？")}
            trigger={
              <button
                className={styles.draftsDeletePicked}
                disabled={pickedHere.length === 0}
                type="button"
              >
                {t("删除")}
              </button>
            }
          />
        </div>
      ) : null}
    </>
  );
}
