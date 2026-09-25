import { Icon } from "../../../../components/icons";
import { ConfirmDialog } from "../../../owner/ConfirmDialog";
import { formatMomentDate } from "../../../posts/dates";
import type { ArticleDraft } from "../../articleDrafts";
import { MAX_ARTICLE_DRAFTS } from "../../articleDrafts";
import styles from "../Editor.module.css";

/**
 * The drafts this site keeps, newest first.
 *
 * Server-side, not this browser's: an article is written over days and often
 * from more than one machine, and a list held in localStorage would tell the
 * owner they had lost work every time they opened it somewhere else. The
 * moment composer's own 草稿 list is the opposite decision for the opposite
 * reason -- a moment is one sitting -- and the two lists are unrelated.
 */
export function DraftRail({
  drafts,
  currentId,
  loading,
  error,
  timezone,
  onOpen,
  onDiscard,
  onNew,
}: {
  drafts: readonly ArticleDraft[];
  currentId: string | null;
  loading: boolean;
  error: string | null;
  timezone: string;
  onOpen: (draft: ArticleDraft) => void;
  onDiscard: (draft: ArticleDraft) => void;
  onNew: () => void;
}) {
  return (
    <nav aria-label="文章草稿" className={styles.rail}>
      <button className={styles.newDraft} onClick={onNew} type="button">
        <Icon name="compose" />
        <span>新文章</span>
      </button>
      <div className={styles.railHead}>
        <h2 className={styles.railTitle}>草稿</h2>
        <span className={styles.railCount}>
          {drafts.length} / {MAX_ARTICLE_DRAFTS}
        </span>
      </div>
      {error ? <p className={styles.railError}>{error}</p> : null}
      {!error && loading && drafts.length === 0 ? (
        <p className={styles.railEmpty}>正在读取草稿…</p>
      ) : null}
      {!error && !loading && drafts.length === 0 ? (
        <p className={styles.railEmpty}>还没有草稿。写点什么，它会自己保存。</p>
      ) : null}
      <ol className={styles.draftList}>
        {drafts.map((draft) => {
          const title = draft.title.trim();
          const saved = formatMomentDate(draft.updatedAt, timezone);
          return (
            <li
              className={styles.draftRow}
              data-current={draft.id === currentId}
              key={draft.id}
            >
              <button
                aria-current={draft.id === currentId ? "true" : undefined}
                className={styles.draftOpen}
                onClick={() => onOpen(draft)}
                type="button"
              >
                <span
                  className={`${styles.draftName} ${title === "" ? styles.draftUntitled : ""}`}
                >
                  {title === "" ? "无标题" : title}
                </span>
                <span className={styles.draftWhen}>
                  {saved.relative}保存
                  {draft.publishedEntryId === null ? "" : " · 已发布文章的修改"}
                </span>
              </button>
              <ConfirmDialog
                confirmLabel="删除草稿"
                danger
                description="删除后无法找回。"
                onConfirm={() => onDiscard(draft)}
                title={`删除「${title === "" ? "无标题" : title}」？`}
                trigger={
                  <button
                    aria-label={`删除草稿：${title === "" ? "无标题" : title}`}
                    className={styles.draftDiscard}
                    type="button"
                  >
                    <Icon name="delete" />
                  </button>
                }
              />
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
