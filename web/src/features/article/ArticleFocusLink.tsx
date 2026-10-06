import { InternalLink } from "../../app/navigation";
import { articleFocusPath } from "../../app/routes";
import { ActionTooltip } from "../../components/TimelineInteractions";
import styles from "./Article.module.css";

export function ArticleFocusLink({
  id,
  username,
}: {
  id: string;
  username: string;
}) {
  return (
    <ActionTooltip label="全屏阅读">
      <InternalLink
        aria-label="全屏阅读"
        className={styles.focusLink}
        data-focus-key={`article-focus-${id}`}
        href={articleFocusPath(username, id)}
      >
        <svg viewBox="0 0 24 24" aria-hidden="true" fill="currentColor">
          <path d="M13 3h8v8h-2V6.41l-5.043 5.05-1.414-1.42L17.586 5H13V3zm-1.543 10.96L6.414 19H11v2H3v-8h2v4.59l5.043-5.05 1.414 1.42z" />
        </svg>
      </InternalLink>
    </ActionTooltip>
  );
}
