import type { LineLayerSpecification, Map } from "maplibre-gl";

export const MAP_STYLE = "https://tiles.openfreemap.org/styles/liberty";
export const MAP_CONNECTION_PAINT: LineLayerSpecification["paint"] = {
  "line-color": "#327cc7",
  "line-width": 2.5,
  "line-dasharray": [2, 2],
};

export function applyMapWaterColor(map: Map) {
  const colour = "#C7E3EA";
  if (
    !map.getLayer("water") ||
    map.getPaintProperty("water", "fill-color") === colour
  )
    return;
  map.setPaintProperty("water", "fill-color-transition", {
    duration: 0,
    delay: 0,
  });
  map.setPaintProperty("water", "fill-color", colour);
}
