import { lazy, Suspense } from "react";
import { PlaceContent } from "./MapPlaceContent";
import { mapPlaceIdentity } from "./geometry";
import type { MapCardData } from "./types";
import styles from "./Maps.module.css";

const EditablePlaces = lazy(() => import("./EditableMapPlaces"));

export function MapPlaces({
  card,
  onChange,
  disabled = false,
}: {
  card: MapCardData;
  onChange?: (card: MapCardData | null) => void;
  disabled?: boolean;
}) {
  const list = (
    <ol className={styles.places} aria-label="地图地点">
      {card.places.map((place, index) => (
        <li key={`${mapPlaceIdentity(place)}:${index}`}>
          <div className={styles.placeRow}>
            <PlaceContent place={place} />
          </div>
        </li>
      ))}
    </ol>
  );
  return onChange ? (
    <Suspense fallback={list}>
      <EditablePlaces card={card} onChange={onChange} disabled={disabled} />
    </Suspense>
  ) : (
    list
  );
}
