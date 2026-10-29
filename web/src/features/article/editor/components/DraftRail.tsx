import { useTranslation } from "react-i18next";
import { Icon } from "../../../../components/icons";
import { Tabs } from "@base-ui/react/tabs";
import { ConfirmDialog } from "../../../../components/ConfirmDialog";
import { formatMomentDate } from "../../../posts/dates";
import type { ArticleDraftSummary } from "../../articleDrafts";
import { MAX_ARTICLE_DRAFTS } from "../../articleDrafts";
import styles from "../Editor.module.css";
import { PublishedArticles } from "./PublishedArticles";

export function DraftRail({
  drafts,
  currentId,
  loading,
  error,
  timezone,
  onOpen,
  onDiscard,
  onNew,
  onOpenPublished,
  disabled,
}: {
  drafts: readonly ArticleDraftSummary[];
  currentId: string | null;
  loading: boolean;
  error: string | null;
  timezone: string;
  onOpen: (draft: ArticleDraftSummary) => void;
  onDiscard: (draft: ArticleDraftSummary) => void;
  onNew: () => void;
  onOpenPublished: (id: string) => Promise<void>;
  disabled: boolean;
}) {
  const { t } = useTranslation();

  return (
    <nav aria-label={t("文章草稿")} className={styles.rail}>
      <button
        className={styles.newDraft}
        onClick={onNew}
        disabled={disabled}
        type="button"
      >
        <Icon name="compose" />
        <span>{t("新文章")}</span>
      </button>
      <Tabs.Root defaultValue="drafts">
        <Tabs.List className={styles.railTabs} aria-label={t("文章列表")}>
          <Tabs.Tab className={styles.railTab} value="drafts">
            {t("草稿")}
          </Tabs.Tab>
          <Tabs.Tab className={styles.railTab} value="published">
            {t("已发布")}
          </Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="drafts">
          <div className={styles.railHead}>
            <h2 className={styles.railTitle}>{t("草稿")}</h2>
            <span className={styles.railCount}>
              {drafts.length} / {MAX_ARTICLE_DRAFTS}
            </span>
          </div>
          {error ? <p className={styles.railError}>{error}</p> : null}
          {!error && loading && drafts.length === 0 ? (
            <p className={styles.railEmpty}>{t("正在读取草稿…")}</p>
          ) : null}
          {!error && !loading && drafts.length === 0 ? (
            <p className={styles.railEmpty}>
              {t("还没有草稿。写点什么，它会自己保存。")}
            </p>
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
                    disabled={disabled}
                    onClick={() => onOpen(draft)}
                    type="button"
                  >
                    <span
                      className={`${styles.draftName} ${title === "" ? styles.draftUntitled : ""}`}
                    >
                      {title === "" ? t("无标题") : title}
                    </span>
                    <span className={styles.draftWhen}>
                      {saved.relative}
                      {t("保存")}
                      {draft.publishedEntryId === null
                        ? ""
                        : t(" · 已发布文章的修改")}
                    </span>
                  </button>
                  <ConfirmDialog
                    confirmLabel={t("删除草稿")}
                    danger
                    description={t("删除后无法找回。")}
                    onConfirm={() => onDiscard(draft)}
                    title={t("删除「{{value1}}」？", {
                      value1: title === "" ? t("无标题") : title,
                    })}
                    trigger={
                      <button
                        aria-label={t("删除草稿：{{value1}}", {
                          value1: title === "" ? t("无标题") : title,
                        })}
                        className={styles.draftDiscard}
                        disabled={disabled}
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
        </Tabs.Panel>
        <Tabs.Panel value="published">
          <PublishedArticles onOpen={onOpenPublished} timezone={timezone} />
        </Tabs.Panel>
      </Tabs.Root>
    </nav>
  );
}
