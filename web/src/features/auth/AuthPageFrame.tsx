import { useTranslation } from "react-i18next";
import type { ReactNode } from "react";
import {
  LayoutGroup,
  MotionConfig,
  motion,
  useReducedMotion,
} from "motion/react";

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
  const reduced = useReducedMotion();
  return (
    <div className={styles.loginScreen}>
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
        <MotionConfig reducedMotion="user">
          <LayoutGroup>
            <motion.main
              className={styles.actionPanel}
              layout="position"
              initial={false}
              transition={{
                layout: {
                  duration: reduced ? 0 : 0.26,
                  ease: [0.4, 0, 0.2, 1],
                },
              }}
            >
              <div className={styles.headlineBlock}>
                <h1 className={styles.displayHeadline}>{headline}</h1>
                {subtitle ? (
                  <p className={styles.subHeadline}>{subtitle}</p>
                ) : null}
              </div>
              {children}
            </motion.main>
          </LayoutGroup>
        </MotionConfig>
      </div>
    </div>
  );
}
