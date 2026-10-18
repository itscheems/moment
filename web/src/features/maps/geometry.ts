import gcoord from "gcoord";
import greatCircle from "@turf/great-circle";
import type { MapCardData, MapPlace } from "./types";

/** Render identity is independent of list order and object property order. */
export function mapPlaceIdentity(place: MapPlace): string {
  return JSON.stringify([
    place.name,
    place.latitude,
    place.longitude,
    place.coordinateSystem,
  ]);
}

export function mapCoordinate(place: MapPlace): [number, number] {
  const point: [number, number] = [place.longitude, place.latitude];
  return place.coordinateSystem === "gcj02"
    ? gcoord.transform(point, gcoord.GCJ02, gcoord.WGS84)
    : point;
}

/** Smallest longitude interval, including journeys crossing the date line. */
export function mapBounds(
  places: MapPlace[],
): [[number, number], [number, number]] {
  const points = places.map(mapCoordinate);
  const longs = points
    .map(([lng]) => ((lng % 360) + 360) % 360)
    .sort((a, b) => a - b);
  let largestGap = -1;
  let start = 0;
  for (let i = 0; i < longs.length; i++) {
    const gap =
      (i + 1 < longs.length ? longs[i + 1] : longs[0] + 360) - longs[i];
    if (gap > largestGap) {
      largestGap = gap;
      start = (i + 1) % longs.length;
    }
  }
  let west = longs[start];
  if (west > 180) west -= 360;
  const east = west + 360 - largestGap;
  const lats = points.map(([, lat]) => Math.max(-85, Math.min(85, lat)));
  return [
    [west, Math.min(...lats)],
    [east, Math.max(...lats)],
  ];
}

export function mapConnections(card: MapCardData) {
  return {
    type: "FeatureCollection" as const,
    features: card.connectPoints
      ? card.places.slice(1).flatMap((place, index) => {
          const from = mapCoordinate(card.places[index]);
          const to = mapCoordinate(place);
          if (from[0] === to[0] && from[1] === to[1]) return [];
          // Antipodes have no unique shortest arc; retain a deterministic simple
          // segment instead of letting the upstream arc constructor reject the map.
          if (
            Math.abs(Math.abs(from[0] - to[0]) - 180) < 1e-7 &&
            Math.abs(from[1] + to[1]) < 1e-7
          ) {
            return [
              {
                type: "Feature" as const,
                properties: {},
                geometry: {
                  type: "LineString" as const,
                  coordinates: [from, to],
                },
              },
            ];
          }
          return [greatCircle(from, to, { npoints: 64 })];
        })
      : [],
  };
}

export function googleMapUrl(place: MapPlace): string {
  const [lng, lat] = mapCoordinate(place);
  return `https://www.google.com/maps/search/?${new URLSearchParams({ api: "1", query: `${lat},${lng}` })}`;
}
