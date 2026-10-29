import { useState } from "react";
import { useTranslation } from "react-i18next";
import { LanguageDialog } from "../../components/LanguageDialog";
import forms from "../../components/Forms.module.css";
import styles from "./Settings.module.css";

export function LanguageSettings() {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <section aria-label={t("语言")}>
      <header className={styles.sectionHeader}>
        <h2>{t("语言")}</h2>
      </header>
      <button
        className={forms.secondaryButton}
        onClick={() => setOpen(true)}
        type="button"
      >
        {i18n.resolvedLanguage === "en" ? "English" : "简体中文"}
      </button>
      {open ? <LanguageDialog onClose={() => setOpen(false)} /> : null}
    </section>
  );
}
