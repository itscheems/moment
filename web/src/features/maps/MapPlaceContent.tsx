import { ActionTooltip } from "../../components/TimelineInteractions";
import { Icon } from "../../components/icons";
import { googleMapUrl } from "./geometry";
import type { MapPlace } from "./types";
import styles from "./Maps.module.css";

export function PlaceContent({ place }: { place: MapPlace }) {
  return (
    <>
      <div className={styles.placeText}>
        <strong>{place.name}</strong>
        {place.displayName !== place.name && <span>{place.displayName}</span>}
      </div>
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
        >
          <Icon name="open" />
        </a>
      </ActionTooltip>
    </>
  );
}
