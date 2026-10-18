export const MAX_MAP_PLACES = 20;

export interface MapPlace {
  name: string;
  displayName: string;
  latitude: number;
  longitude: number;
  coordinateSystem: "wgs84" | "gcj02";
}

export interface MapCardData {
  places: MapPlace[];
  connectPoints: boolean;
}

export function mapCardPayload(card: MapCardData): Record<string, unknown> {
  return {
    places: card.places.map((place) => ({
      name: place.name,
      display_name: place.displayName,
      latitude: place.latitude,
      longitude: place.longitude,
      coordinate_system: place.coordinateSystem,
    })),
    connect_points: card.connectPoints,
  };
}

export function parseMapCard(value: unknown): MapCardData {
  const object = (item: unknown): Record<string, unknown> => {
    if (!item || typeof item !== "object" || Array.isArray(item))
      throw new Error("地图数据无效");
    return item as Record<string, unknown>;
  };
  const card = object(value);
  if (
    !Array.isArray(card.places) ||
    card.places.length < 1 ||
    card.places.length > MAX_MAP_PLACES ||
    typeof card.connect_points !== "boolean" ||
    Object.keys(card).some(
      (key) => !["places", "connect_points"].includes(key),
    ) ||
    (card.connect_points && card.places.length < 2)
  )
    throw new Error("地图地点或连线设置无效");
  return {
    connectPoints: card.connect_points,
    places: card.places.map((item) => {
      const place = object(item);
      if (
        Object.keys(place).length !== 5 ||
        typeof place.name !== "string" ||
        !place.name.trim() ||
        place.name !== place.name.trim() ||
        Array.from(place.name).length > 160 ||
        typeof place.display_name !== "string" ||
        !place.display_name.trim() ||
        place.display_name !== place.display_name.trim() ||
        Array.from(place.display_name).length > 500 ||
        typeof place.latitude !== "number" ||
        !Number.isFinite(place.latitude) ||
        Math.abs(place.latitude) > 90 ||
        typeof place.longitude !== "number" ||
        !Number.isFinite(place.longitude) ||
        Math.abs(place.longitude) > 180 ||
        !["wgs84", "gcj02"].includes(String(place.coordinate_system))
      )
        throw new Error("地图地点无效");
      return {
        name: place.name,
        displayName: place.display_name,
        latitude: place.latitude,
        longitude: place.longitude,
        coordinateSystem:
          place.coordinate_system as MapPlace["coordinateSystem"],
      };
    }),
  };
}
