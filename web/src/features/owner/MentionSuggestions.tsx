import type { MentionSuggestion, SiteIdentity } from "../../api/public";
import { SiteAvatar } from "../../components/SiteIdentity";
import styles from "./Owner.module.css";

export function MentionSuggestions({
  active,
  items,
  onPick,
}: {
  active: number;
  items: MentionSuggestion[];
  onPick: (item: MentionSuggestion) => void;
}) {
  if (items.length === 0) return null;
  return (
    <div
      aria-label="提及用户建议"
      className={styles.mentionSuggestions}
      role="listbox"
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
            key={item.ownerId}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => onPick(item)}
            role="option"
            type="button"
          >
            <SiteAvatar site={site} />
            <span>
              <strong>{item.displayName}</strong>
              <small>@{item.username}</small>
            </span>
            <em>提及</em>
          </button>
        );
      })}
    </div>
  );
}
