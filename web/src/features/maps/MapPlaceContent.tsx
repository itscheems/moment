import { ActionTooltip } from "../../components/TimelineInteractions";
import { motion } from "motion/react";
import { Icon } from "../../components/icons";
import { googleMapUrl } from "./geometry";
import type { MapPlace } from "./types";
import styles from "./Maps.module.css";

export function PlaceContent({
  place,
  onSelect,
  disabled = false,
}: {
  place: MapPlace;
  onSelect?: (place: MapPlace) => void;
  disabled?: boolean;
}) {
  const text = (
    <>
      <strong>{place.name}</strong>
      {place.displayName !== place.name && <span>{place.displayName}</span>}
    </>
  );
  return (
    <>
      {onSelect ? (
        <motion.button
          type="button"
          disabled={disabled}
          className={`${styles.placeText} ${styles.placeTarget}`}
          aria-label={`前往${place.name}`}
          onTap={(event) => {
            if (event.isTrusted) onSelect(place);
          }}
          onClick={(event) => {
            event.stopPropagation();
            // Pointer taps use Motion's drag-aware press lifecycle. Native
            // keyboard and assistive activation keep the button's click path.
            if (event.detail === 0) onSelect(place);
          }}
        >
          {text}
        </motion.button>
      ) : (
        <div className={styles.placeText}>{text}</div>
      )}
      <ActionTooltip label="在 Google Maps 打开">
        <a
          href={googleMapUrl(place)}
          target="_blank"
          rel="noopener noreferrer"
          aria-label={`在 Google Maps 打开${place.name}`}
          onMouseDown={(event) => event.stopPropagation()}
          onTouchStart={(event) => event.stopPropagation()}
          onPointerDown={(event) => event.stopPropagation()}
          onContextMenu={(event) => event.stopPropagation()}
          onClick={(event) => event.stopPropagation()}
        >
          <Icon name="open" />
        </a>
      </ActionTooltip>
    </>
  );
}
