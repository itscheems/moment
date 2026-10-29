import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { PublicApi, type MentionSuggestion } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { LoadMoreSentinel } from "../../components/LoadMoreSentinel";
import { useReadRecovery } from "../../components/useReadRecovery";
import { SearchUserIdentity } from "./RailSearch";
import { rememberUserSearch } from "./recentSearches";
import styles from "./Memory.module.css";

export function PeopleSearchResults({
  api,
  query,
  scope,
}: {
  api: PublicApi;
  query: string;
  scope: string | null;
}) {
  const { t } = useTranslation();
  const { pathname, restoreCurrentEntry } = useNavigation();
  const [users, setUsers] = useState<MentionSuggestion[]>([]);
  const [nextOffset, setNextOffset] = useState<number | null>(null);
  const [loading, setLoading] = useState(Boolean(query));
  const [error, setError] = useState<unknown>(null);
  const request = useRef<AbortController | null>(null);
  const busy = useRef(false);
  const load = useCallback(
    async (offset = 0) => {
      if (!query || busy.current) return;
      const controller = new AbortController();
      request.current = controller;
      busy.current = true;
      setLoading(true);
      setError(null);
      try {
        const page = await api.searchUsers(query, controller.signal, offset);
        if (controller.signal.aborted) return;
        setUsers((current) =>
          offset
            ? [
                ...current,
                ...page.items.filter(
                  (user) =>
                    !current.some((item) => item.userId === user.userId),
                ),
              ]
            : page.items,
        );
        setNextOffset(page.nextOffset);
        restoreCurrentEntry();
      } catch (cause) {
        if (!controller.signal.aborted) setError(cause);
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
          busy.current = false;
        }
      }
    },
    [api, query, restoreCurrentEntry],
  );
  useEffect(() => {
    void load();
    return () => {
      request.current?.abort();
      busy.current = false;
    };
  }, [load]);
  useReadRecovery({
    error,
    loading,
    onRetry: () => load(users.length ? (nextOffset ?? 0) : 0),
    enabled: pathname === "/search",
  });
  if (!query) return null;
  return (
    <section aria-label={t("用户搜索结果")} aria-busy={loading}>
      {users.map((user) => (
        <InternalLink
          className={styles.searchUser}
          key={user.userId}
          href={user.profileUrl}
          onClick={() => rememberUserSearch(scope, user)}
        >
          <SearchUserIdentity user={user} />
        </InternalLink>
      ))}
      {loading && !users.length ? (
        <p className={styles.searchHint} role="status">
          {t("正在搜索…")}
        </p>
      ) : null}
      {!loading && !error && !users.length ? (
        <div className={styles.message} role="status">
          <div>
            <h2>{t("没有找到用户")}</h2>
            <p>{t("试试其他昵称或用户名。")}</p>
          </div>
        </div>
      ) : null}
      {error ? (
        <div className={styles.message} role="status">
          <div>
            <p>{t("无法搜索用户")}</p>
            <button
              onClick={() => void load(users.length ? (nextOffset ?? 0) : 0)}
              type="button"
            >
              {t("重试")}
            </button>
          </div>
        </div>
      ) : null}
      {nextOffset !== null && !error ? (
        <LoadMoreSentinel
          className={styles.moreRow}
          cursor={String(nextOffset)}
          label={t("加载更多用户")}
          loading={loading}
          onLoad={() => void load(nextOffset)}
        />
      ) : null}
    </section>
  );
}
