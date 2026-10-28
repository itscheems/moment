import { AccountName } from "../../components/AccountName";
import type { CSSProperties } from "react";

import type { MentionSuggestion, SiteIdentity } from "../../api/public";
import { SiteAvatar } from "../../components/SiteIdentity";
import styles from "../../components/Forms.module.css";

export function MentionSuggestions({
  active,
  items,
  onPick,
  position,
}: {
  active: number;
  items: MentionSuggestion[];
  onPick: (item: MentionSuggestion) => void;
  position?: {
    left: number;
    top: number;
    width: number;
  };
}) {
  if (items.length === 0) return null;
  const style = position
    ? ({
        insetBlockStart: `${position.top}px`,
        insetInlineStart: `${position.left}px`,
        inlineSize: `${position.width}px`,
      } satisfies CSSProperties)
    : undefined;
  return (
    <div
      aria-label="提及用户建议"
      className={`${styles.mentionSuggestions} ${position ? styles.mentionSuggestionsCaret : ""}`}
      data-placement={position ? "caret" : "form"}
      role="listbox"
      style={style}
    >
      {items.map((item, index) => {
        const site = {
          avatarMediaId: item.avatarMediaId,
          displayName: item.displayName,
        } as SiteIdentity;
        return (
          <button
            aria-selected={index === active}
            className={styles.mentionSuggestion}
            key={item.userId}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => onPick(item)}
            role="option"
            type="button"
          >
            <SiteAvatar site={site} />
            <span className={styles.mentionIdentity}>
              <strong>
                <AccountName identity={item} />
              </strong>
              <small>@{item.username}</small>
            </span>
            <em>提及</em>
          </button>
        );
      })}
    </div>
  );
}
