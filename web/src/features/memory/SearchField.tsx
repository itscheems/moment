import type { ComponentProps, Ref } from "react";
import { Autocomplete } from "@base-ui/react/autocomplete";
import { useTranslation } from "react-i18next";
import { Icon } from "../../components/icons";
import styles from "./RailSearch.module.css";

export function SearchField({
  inputRef,
  className,
  clearLabel,
  ...input
}: Omit<ComponentProps<typeof Autocomplete.Input>, "ref" | "className"> & {
  inputRef?: Ref<HTMLInputElement>;
  className?: string;
  clearLabel?: string;
}) {
  const { t } = useTranslation();
  return (
    <Autocomplete.InputGroup
      className={[styles.field, className].filter(Boolean).join(" ")}
      data-search-control
    >
      <span className={styles.searchIcon}>
        <Icon name="search" variant="navigation" />
      </span>
      <Autocomplete.Input {...input} className={styles.input} ref={inputRef} />
      <Autocomplete.Clear
        aria-label={clearLabel ?? t("清除搜索内容")}
        className={styles.clearInput}
      >
        <svg aria-hidden="true" viewBox="0 0 24 24">
          <path d="M12 1.75C6.34 1.75 1.75 6.34 1.75 12S6.34 22.25 12 22.25 22.25 17.66 22.25 12 17.66 1.75 12 1.75zm3.71 12.54l-1.42 1.42-2.29-2.3-2.29 2.3-1.42-1.42 2.3-2.29-2.3-2.29 1.42-1.42 2.29 2.3 2.29-2.3 1.42 1.42-2.3 2.29 2.3 2.29z" />
        </svg>
      </Autocomplete.Clear>
    </Autocomplete.InputGroup>
  );
}
