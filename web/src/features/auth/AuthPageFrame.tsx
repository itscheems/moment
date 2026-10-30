import { useTranslation } from "react-i18next";
import type { ReactNode } from "react";

import { InternalLink, useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import styles from "../../components/Forms.module.css";

export function AuthPageFrame({
  headline,
  subtitle,
  children,
}: {
  headline: string;
  subtitle?: string;
  children: ReactNode;
}) {
  const { t } = useTranslation();

  const { goBackAuth } = useNavigation();
  return (
    <div className={styles.loginScreen}>
      <div aria-hidden="true" className={styles.brandPanel} />
      <InternalLink
        aria-label={t("返回时间流")}
        className={styles.loginBack}
        href="/"
        onClick={(event) => {
          if (
            event.defaultPrevented ||
            event.button !== 0 ||
            event.metaKey ||
            event.ctrlKey ||
            event.altKey ||
            event.shiftKey
          )
            return;
          event.preventDefault();
          goBackAuth("/");
        }}
      >
        <Icon name="back" />
      </InternalLink>
      <div className={styles.loginSplit}>
        <main className={styles.actionPanel}>
          <div className={styles.headlineBlock}>
            <h1 className={styles.displayHeadline}>{headline}</h1>
            {subtitle ? <p className={styles.subHeadline}>{subtitle}</p> : null}
          </div>
          {children}
        </main>
      </div>
    </div>
  );
}
