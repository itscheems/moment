import { useTranslation } from "react-i18next";
import { useCallback, useEffect, useMemo, useState } from "react";
import { PublicApi, type PublicArticlePost } from "../../../../api/public";
import { listArticles } from "../../articleApi";
import { formatMomentDate } from "../../../posts/dates";
import styles from "../Editor.module.css";

export function PublishedArticles({
  onOpen,
  timezone,
}: {
  onOpen: (id: string) => Promise<void>;
  timezone: string;
}) {
  const { t } = useTranslation();

  const api = useMemo(() => new PublicApi(), []);
  const [items, setItems] = useState<PublicArticlePost[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [opening, setOpening] = useState<string | null>(null);
  const load = useCallback(
    async (next: string | null, signal?: AbortSignal) => {
      setLoading(true);
      setError(false);
      try {
        const page = await listArticles(api, {
          cursor: next,
          limit: 20,
          signal,
        });
        if (signal?.aborted) return;
        setItems((previous) =>
          next === null
            ? page.items
            : [
                ...previous,
                ...page.items.filter(
                  (item) => !previous.some((p) => p.id === item.id),
                ),
              ],
        );
        setCursor(page.nextCursor);
      } catch {
        if (!signal?.aborted) setError(true);
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [api],
  );
  useEffect(() => {
    const controller = new AbortController();
    void load(null, controller.signal);
    return () => controller.abort();
  }, [load]);
  return (
    <>
      <ol className={styles.draftList}>
        {items.map((post) => (
          <li className={styles.draftRow} key={post.id}>
            <button
              className={styles.draftOpen}
              type="button"
              disabled={opening !== null}
              onClick={() => {
                setOpening(post.id);
                void onOpen(post.id).finally(() => setOpening(null));
              }}
            >
              <span className={styles.draftName}>{post.article.title}</span>
              <span className={styles.draftWhen}>
                {opening === post.id
                  ? t("正在打开…")
                  : t("{{value1}}发布{{value2}}", {
                      value1: formatMomentDate(post.publishedAt, timezone)
                        .relative,
                      value2:
                        post.visibility === "only_me" ? t(" · 仅自己") : "",
                    })}
              </span>
            </button>
          </li>
        ))}
      </ol>
      {!loading && !error && items.length === 0 ? (
        <p className={styles.railEmpty}>{t("还没有发布过文章。")}</p>
      ) : null}
      {loading ? (
        <p className={styles.railEmpty}>{t("正在读取文章…")}</p>
      ) : null}
      {error ? (
        <p className={styles.railError}>{t("没能读取已发布的文章。")}</p>
      ) : null}
      {(cursor || error) && !loading ? (
        <button
          className={styles.newDraft}
          type="button"
          onClick={() => void load(cursor)}
        >
          {error ? t("重试") : t("加载更多")}
        </button>
      ) : null}
    </>
  );
}
