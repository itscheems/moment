import { SearchField } from "./SearchField";
import { useTranslation } from "react-i18next";
import { Autocomplete } from "@base-ui/react/autocomplete";
import { useEffect, useRef, useState } from "react";

import { PublicApi, type MentionSuggestion } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { AccountName } from "../../components/AccountName";
import { SiteAvatar } from "../../components/SiteIdentity";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { Icon } from "../../components/icons";
import { searchHref, type SearchTab } from "./memoryState";
import {
  clearRecentSearches,
  rememberSearch,
  rememberUserSearch,
  removeRecentSearch,
  recentSearchKey,
  useRecentSearches,
  type RecentSearch,
} from "./recentSearches";
import styles from "./RailSearch.module.css";

function SearchGlyph({ recent = false }: { recent?: boolean }) {
  return recent ? (
    <svg aria-hidden="true" viewBox="0 0 21 21">
      <path d="M9.094 3.095c-3.314 0-6 2.686-6 6s2.686 6 6 6c1.657 0 3.155-.67 4.243-1.757 1.087-1.088 1.757-2.586 1.757-4.243 0-3.314-2.686-6-6-6zm-9 6c0-4.971 4.029-9 9-9s9 4.029 9 9c0 1.943-.617 3.744-1.664 5.215l4.475 4.474-2.122 2.122-4.474-4.475c-1.471 1.047-3.272 1.664-5.215 1.664-4.97-.001-8.999-4.03-9-9z" />
    </svg>
  ) : (
    <Icon name="search" variant="navigation" />
  );
}

export function SearchUserIdentity({ user }: { user: MentionSuggestion }) {
  return (
    <>
      <SiteAvatar site={{ ...user, coverMediaId: null }} size={40} />
      <span className={styles.userText}>
        <strong>
          <AccountName identity={user} />
        </strong>
        <span>@{user.username}</span>
      </span>
    </>
  );
}

export function RailSearch({ scope }: { scope: string | null }) {
  const [api] = useState(() => new PublicApi());
  return <SearchBox api={api} scope={scope} />;
}

type SearchOption = { key: string; entry: RecentSearch; recent: boolean };

export function SearchBox({
  api,
  scope,
  initialValue = "",
  tab = "latest",
  main = false,
}: {
  api: PublicApi;
  scope: string | null;
  initialValue?: string;
  tab?: SearchTab;
  main?: boolean;
}) {
  const { t } = useTranslation();
  const { navigate } = useNavigation();
  const recent = useRecentSearches(scope);
  const [value, setValue] = useState(initialValue);
  const [open, setOpen] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const [response, setResponse] = useState<{
    query: string;
    users: MentionSuggestion[];
    error: boolean;
  } | null>(null);
  const [retry, setRetry] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const query = value.trim();

  useEffect(() => {
    if (!open || !query) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      void api
        .searchUsers(query, controller.signal, 0, 8)
        .then((page) => {
          if (!controller.signal.aborted)
            setResponse({ query, users: page.items, error: false });
        })
        .catch(() => {
          if (!controller.signal.aborted)
            setResponse({ query, users: [], error: true });
        });
    }, 150);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [api, open, query, retry]);

  const matches = recent.filter((entry) =>
    typeof entry === "string"
      ? entry.toLocaleLowerCase().includes(query.toLocaleLowerCase())
      : `${entry.user.displayName} @${entry.user.username}`
          .toLocaleLowerCase()
          .includes(query.replace(/^@/, "").toLocaleLowerCase()),
  );
  const entries: SearchOption[] = (query ? matches : recent).map((entry) => ({
    key: recentSearchKey(entry),
    entry,
    recent: true,
  }));
  if (
    query &&
    !entries.some(
      (item) =>
        typeof item.entry === "string" &&
        item.entry.toLocaleLowerCase() === query.toLocaleLowerCase(),
    )
  )
    entries.unshift({
      key: `query:${query.toLocaleLowerCase()}`,
      entry: query,
      recent: false,
    });
  if (query && response?.query === query) {
    for (const user of response.users) {
      if (
        !entries.some(
          (item) =>
            typeof item.entry !== "string" &&
            item.entry.user.userId === user.userId,
        )
      )
        entries.push({
          key: `user:${user.userId}`,
          entry: { kind: "user", user },
          recent: false,
        });
    }
  }
  // Text suggestions precede identities while a query is being edited.
  if (query)
    entries.sort(
      (a, b) =>
        Number(typeof a.entry !== "string") -
        Number(typeof b.entry !== "string"),
    );

  const search = (text: string) => {
    const trimmed = text.trim();
    if (!trimmed) return;
    rememberSearch(scope, trimmed);
    setOpen(false);
    input.current?.blur();
    navigate(searchHref({ query: trimmed, tab }));
  };
  const select = (entry: RecentSearch) => {
    if (typeof entry === "string") search(entry);
    else {
      rememberUserSearch(scope, entry.user);
      setValue("");
      setOpen(false);
      input.current?.blur();
      navigate(entry.user.profileUrl);
    }
  };

  return (
    <div className={styles.searchHeader} data-main-search={main || undefined}>
      <form
        aria-label={t("搜索")}
        onSubmit={(event) => {
          event.preventDefault();
          search(value);
        }}
      >
        <button hidden type="submit" />
        <Autocomplete.Root
          items={entries}
          filter={null}
          itemToStringValue={(item) =>
            typeof item.entry === "string"
              ? item.entry
              : item.entry.user.displayName
          }
          mode="none"
          onOpenChange={setOpen}
          onValueChange={setValue}
          open={open && !confirmClear && entries.length > 0}
          openOnInputClick
          value={value}
        >
          <SearchField
            aria-label={t("搜索内容或用户")}
            autoComplete="off"
            data-focus-key={main ? "search:query" : "shell:search"}
            id={main ? "moment-search-input" : undefined}
            aria-keyshortcuts={main ? "/" : undefined}
            enterKeyHint="search"
            maxLength={1_000}
            onFocus={() => setOpen(true)}
            inputRef={input}
            placeholder={t("搜索")}
            spellCheck={false}
          />
          <Autocomplete.Portal>
            <Autocomplete.Positioner
              align="start"
              className={styles.positioner}
              collisionAvoidance={{ side: "none", align: "shift" }}
              collisionPadding={8}
              side="bottom"
              sideOffset={0}
            >
              <Autocomplete.Popup className={styles.popup}>
                {!query && recent.length > 0 ? (
                  <div className={styles.recentHeader}>
                    <h2>{t("最近搜索")}</h2>
                    <button
                      className={styles.clearAll}
                      onClick={() => setConfirmClear(true)}
                      type="button"
                    >
                      {t("全部清除")}
                    </button>
                  </div>
                ) : null}
                <Autocomplete.List
                  data-query={Boolean(query) || undefined}
                  aria-label={query ? t("搜索建议") : t("最近搜索")}
                >
                  {(item: SearchOption) => (
                    <Autocomplete.Item
                      aria-label={
                        typeof item.entry === "string"
                          ? item.entry
                          : `${item.entry.user.displayName} @${item.entry.user.username}`
                      }
                      className={styles.item}
                      key={item.key}
                      value={item}
                      onClick={() => select(item.entry)}
                      data-user={typeof item.entry !== "string" || undefined}
                    >
                      {typeof item.entry === "string" ? (
                        <>
                          <span className={styles.itemIcon}>
                            <SearchGlyph recent />
                          </span>
                          <span className={styles.itemText}>{item.entry}</span>
                        </>
                      ) : (
                        <SearchUserIdentity user={item.entry.user} />
                      )}
                      {item.recent ? (
                        <button
                          aria-label={t("删除搜索记录：{{value1}}", {
                            value1:
                              typeof item.entry === "string"
                                ? item.entry
                                : `@${item.entry.user.username}`,
                          })}
                          className={styles.remove}
                          onClick={(event) => {
                            event.stopPropagation();
                            removeRecentSearch(scope, item.entry);
                            input.current?.focus();
                          }}
                          onPointerDown={(event) => {
                            event.stopPropagation();
                            event.preventDefault();
                          }}
                          type="button"
                        >
                          <Icon name="close" />
                        </button>
                      ) : null}
                    </Autocomplete.Item>
                  )}
                </Autocomplete.List>
                {query && response?.query !== query ? (
                  <p className={styles.empty} role="status">
                    {t("正在搜索…")}
                  </p>
                ) : null}
                {query && response?.query === query && response.error ? (
                  <div className={styles.empty} role="status">
                    {t("无法搜索用户")}{" "}
                    <button
                      className={styles.retry}
                      onClick={() => {
                        setResponse(null);
                        setRetry((n) => n + 1);
                      }}
                      type="button"
                    >
                      {t("重试")}
                    </button>
                  </div>
                ) : null}
              </Autocomplete.Popup>
            </Autocomplete.Positioner>
          </Autocomplete.Portal>
        </Autocomplete.Root>
      </form>
      <ConfirmDialog
        cancelLabel={t("取消")}
        confirmLabel={t("清除")}
        description={t(
          "此操作无法撤销，将清除这台设备上当前账号的所有最近搜索。",
        )}
        finalFocus={input}
        onConfirm={() => clearRecentSearches(scope)}
        onOpenChange={setConfirmClear}
        open={confirmClear}
        title={t("清除所有最近搜索？")}
      />
    </div>
  );
}
