import { Autocomplete } from "@base-ui/react/autocomplete";
import { useRef, useState } from "react";

import { useNavigation } from "../../app/navigation";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { Icon } from "../../components/icons";
import { searchHref } from "./memoryState";
import {
  clearRecentSearches,
  rememberSearch,
  removeRecentSearch,
  useRecentSearches,
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

export function RailSearch({ scope }: { scope: string | null }) {
  const { navigate } = useNavigation();
  const recent = useRecentSearches(scope);
  const [value, setValue] = useState("");
  const [open, setOpen] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const query = value.trim();
  const entries = query ? [query] : recent;
  const search = (text: string) => {
    const trimmed = text.trim();
    if (!trimmed) return;
    rememberSearch(scope, trimmed);
    setOpen(false);
    input.current?.blur();
    navigate(searchHref({ query: trimmed }));
  };

  return (
    <div className={styles.searchHeader}>
      <form
        aria-label="搜索"
        onSubmit={(event) => {
          event.preventDefault();
          search(value);
        }}
      >
        <button hidden type="submit" />
        <Autocomplete.Root
          items={entries}
          mode="none"
          onOpenChange={setOpen}
          onValueChange={setValue}
          open={open && !confirmClear}
          openOnInputClick
          value={value}
        >
          <Autocomplete.InputGroup className={styles.field}>
            <span className={styles.searchIcon}>
              <SearchGlyph />
            </span>
            <Autocomplete.Input
              aria-label="搜索 Moment"
              autoComplete="off"
              className={styles.input}
              data-focus-key="shell:search"
              enterKeyHint="search"
              maxLength={1_000}
              onFocus={() => setOpen(true)}
              ref={input}
              placeholder="搜索"
              spellCheck={false}
            />
            <Autocomplete.Clear
              aria-label="清除搜索内容"
              className={styles.clearInput}
            >
              <svg aria-hidden="true" viewBox="0 0 24 24">
                <path d="M12 1.75C6.34 1.75 1.75 6.34 1.75 12S6.34 22.25 12 22.25 22.25 17.66 22.25 12 17.66 1.75 12 1.75zm3.71 12.54l-1.42 1.42-2.29-2.3-2.29 2.3-1.42-1.42 2.3-2.29-2.3-2.29 1.42-1.42 2.29 2.3 2.29-2.3 1.42 1.42-2.3 2.29 2.3 2.29z" />
              </svg>
            </Autocomplete.Clear>
          </Autocomplete.InputGroup>
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
                    <h2>最近搜索</h2>
                    <button
                      className={styles.clearAll}
                      onClick={() => setConfirmClear(true)}
                      type="button"
                    >
                      全部清除
                    </button>
                  </div>
                ) : null}
                {!query && recent.length === 0 ? (
                  <p className={styles.empty}>尝试搜索关键词</p>
                ) : null}
                <Autocomplete.List aria-label={query ? "搜索建议" : "最近搜索"}>
                  {(item: string) => (
                    <Autocomplete.Item
                      aria-label={item}
                      className={styles.item}
                      key={item}
                      onClick={() => search(item)}
                      value={item}
                    >
                      <span className={styles.itemIcon}>
                        <SearchGlyph recent />
                      </span>
                      <span className={styles.itemText}>{item}</span>
                      {!query ? (
                        <button
                          aria-label={`删除搜索记录：${item}`}
                          className={styles.remove}
                          onClick={(event) => {
                            event.stopPropagation();
                            removeRecentSearch(scope, item);
                            input.current?.focus();
                          }}
                          onPointerDown={(event) => event.stopPropagation()}
                          type="button"
                        >
                          <svg aria-hidden="true" viewBox="0 0 24 24">
                            <path d="M10.59 12L4.54 5.96l1.42-1.42L12 10.59l6.04-6.05 1.42 1.42L13.41 12l6.05 6.04-1.42 1.42L12 13.41l-6.04 6.05-1.42-1.42L10.59 12z" />
                          </svg>
                        </button>
                      ) : null}
                    </Autocomplete.Item>
                  )}
                </Autocomplete.List>
              </Autocomplete.Popup>
            </Autocomplete.Positioner>
          </Autocomplete.Portal>
        </Autocomplete.Root>
      </form>
      <ConfirmDialog
        cancelLabel="取消"
        confirmLabel="清除"
        description="此操作无法撤销，将清除这台设备上当前账号的所有最近搜索。"
        finalFocus={input}
        onConfirm={() => clearRecentSearches(scope)}
        onOpenChange={setConfirmClear}
        open={confirmClear}
        title="清除所有最近搜索？"
      />
    </div>
  );
}
