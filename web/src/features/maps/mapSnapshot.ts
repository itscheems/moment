import { Map, setWorkerUrl, type ErrorEvent } from "maplibre-gl";
import workerUrl from "maplibre-gl/dist/maplibre-gl-worker.mjs?worker&url";
import "maplibre-gl/dist/maplibre-gl.css";
import { mapBounds, mapConnections, mapCoordinate } from "./geometry";
import {
  applyMapWaterColor,
  MAP_CONNECTION_PAINT,
  MAP_STYLE,
} from "./mapAppearance";
import type { MapCardData } from "./types";

setWorkerUrl(workerUrl);

export async function renderMapSnapshot(
  card: MapCardData,
  width: number,
  height: number,
  signal?: AbortSignal,
): Promise<HTMLCanvasElement> {
  signal?.throwIfAborted();
  const container = document.createElement("div");
  container.setAttribute("aria-hidden", "true");
  container.inert = true;
  Object.assign(container.style, {
    position: "fixed",
    left: "-10000px",
    top: "0",
    width: `${width}px`,
    height: `${height}px`,
    pointerEvents: "none",
  });
  document.body.append(container);
  let map: Map | undefined;
  try {
    const view = new Map({
      container,
      style: MAP_STYLE,
      bounds: mapBounds(card.places),
      fitBoundsOptions: {
        padding: 48,
        maxZoom: card.places.length === 1 ? 14 : 12,
      },
      interactive: false,
      attributionControl: false,
      maxPitch: 0,
      pixelRatio: 2,
      fadeDuration: 0,
      trackResize: false,
      canvasContextAttributes: { preserveDrawingBuffer: true },
    });
    map = view;
    await new Promise<void>((resolve, reject) => {
      const finish = (error?: unknown) => {
        window.clearTimeout(timeout);
        signal?.removeEventListener("abort", abort);
        view.off("idle", ready);
        view.off("error", fail);
        if (error) reject(error);
        else resolve();
      };
      const ready = () => finish();
      const fail = (event: ErrorEvent) => finish(event.error);
      const abort = () => finish(signal?.reason);
      const timeout = window.setTimeout(
        () => finish(new Error("map image timed out")),
        20_000,
      );
      signal?.addEventListener("abort", abort, { once: true });
      view.on("styledata", () => applyMapWaterColor(view));
      view.on("error", fail);
      view.once("load", () => {
        try {
          view.setProjection({ type: "mercator" });
          if (card.connectPoints) {
            view.addSource("share-connections", {
              type: "geojson",
              data: mapConnections(card),
            });
            view.addLayer({
              id: "share-connections",
              type: "line",
              source: "share-connections",
              paint: MAP_CONNECTION_PAINT,
            });
          }
          view.addSource("share-places", {
            type: "geojson",
            data: {
              type: "FeatureCollection",
              features: card.places.map((place) => {
                const [lng, lat] = mapCoordinate(place);
                return {
                  type: "Feature",
                  properties: {},
                  geometry: {
                    type: "Point",
                    coordinates: [lng, Math.max(-85, Math.min(85, lat))],
                  },
                };
              }),
            },
          });
          view.addLayer({
            id: "share-place-halos",
            type: "circle",
            source: "share-places",
            paint: {
              "circle-radius": 8,
              "circle-color": "rgba(255,255,255,0.8)",
              "circle-stroke-color": "rgba(255,255,255,0.9)",
              "circle-stroke-width": 1,
            },
          });
          view.addLayer({
            id: "share-place-centres",
            type: "circle",
            source: "share-places",
            paint: {
              "circle-radius": 5,
              "circle-color": "#327cc7",
            },
          });
          view.once("idle", ready);
        } catch (error) {
          finish(error);
        }
      });
      if (signal?.aborted) abort();
    });
    signal?.throwIfAborted();
    const canvas = document.createElement("canvas");
    canvas.width = width * 2;
    canvas.height = height * 2;
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("this browser has no 2d canvas");
    ctx.drawImage(view.getCanvas(), 0, 0, canvas.width, canvas.height);
    ctx.scale(2, 2);
    ctx.font = "10px system-ui, sans-serif";
    ctx.textBaseline = "bottom";
    const credit = "OpenFreeMap © OpenMapTiles Data from OpenStreetMap";
    const creditWidth = ctx.measureText(credit).width;
    ctx.fillStyle = "rgba(255,255,255,0.85)";
    ctx.fillRect(width - creditWidth - 12, height - 18, creditWidth + 12, 18);
    ctx.fillStyle = "#333";
    ctx.fillText(credit, width - creditWidth - 6, height - 4);
    return canvas;
  } finally {
    map?.remove();
    container.remove();
  }
}
