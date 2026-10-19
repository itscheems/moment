import { useEffect, useMemo, useRef, useState } from "react";
import Map, {
  Marker,
  NavigationControl,
  Popup,
  Source,
  Layer,
  type MapRef,
} from "react-map-gl/maplibre";
import * as maplibre from "maplibre-gl";
import workerUrl from "maplibre-gl/dist/maplibre-gl-worker.mjs?worker&url";
import "maplibre-gl/dist/maplibre-gl.css";
import { mapBounds, mapConnections, mapCoordinate } from "./geometry";
import type { MapCardData } from "./types";
import styles from "./Maps.module.css";

maplibre.setWorkerUrl(workerUrl);

export interface MapCamera {
  longitude: number;
  latitude: number;
  zoom: number;
  bearing: number;
  pitch: number;
}

export default function MapCanvas({
  card,
  globe = false,
  camera,
  onCamera,
  onFailure,
  onPick,
}: {
  card: MapCardData;
  globe?: boolean;
  camera?: MapCamera;
  onCamera?: (camera: MapCamera) => void;
  onFailure: () => void;
  onPick?: (longitude: number, latitude: number) => void;
}) {
  const ref = useRef<MapRef>(null);
  const [loaded, setLoaded] = useState(false);
  const [selected, setSelected] = useState<number | null>(null);
  const bounds = useMemo(() => mapBounds(card.places), [card.places]);
  const connections = useMemo(() => mapConnections(card), [card]);
  const pointsKey = card.places
    .map(
      (point) =>
        `${point.latitude},${point.longitude},${point.coordinateSystem}`,
    )
    .join(";");
  const fittedKey = useRef(camera ? pointsKey : "");
  useEffect(() => {
    if (loaded && !onPick && fittedKey.current !== pointsKey)
      ref.current?.fitBounds(bounds, {
        padding: 48,
        maxZoom: card.places.length === 1 ? 14 : 12,
        duration: 0,
      });
    if (loaded) fittedKey.current = pointsKey;
  }, [pointsKey, loaded, bounds, card.places.length, onPick]);
  useEffect(() => {
    if (loaded) return;
    const timer = window.setTimeout(onFailure, 20_000);
    return () => window.clearTimeout(timer);
  }, [loaded, onFailure]);
  const lastGlobe = useRef(false);
  const flatCamera = useRef<MapCamera | undefined>(undefined);
  useEffect(() => {
    if (!loaded || lastGlobe.current === globe) return;
    const map = ref.current?.getMap();
    if (!map) return;
    if (globe) {
      const center = map.getCenter();
      flatCamera.current = {
        longitude: center.lng,
        latitude: center.lat,
        zoom: map.getZoom(),
        bearing: map.getBearing(),
        pitch: map.getPitch(),
      };
      map.easeTo({ zoom: 1.5, pitch: 0, duration: 500 });
    } else if (flatCamera.current)
      map.easeTo({ ...flatCamera.current, duration: 500 });
    lastGlobe.current = globe;
  }, [globe, loaded]);
  const popup = selected === null ? null : card.places[selected];
  return (
    <Map
      ref={ref}
      mapLib={maplibre}
      initialViewState={
        camera ??
        (onPick
          ? { longitude: 0, latitude: 0, zoom: 1 }
          : {
              bounds,
              fitBoundsOptions: {
                padding: 48,
                maxZoom: card.places.length === 1 ? 14 : 12,
              },
            })
      }
      mapStyle="https://tiles.openfreemap.org/styles/liberty"
      projection={{ type: globe ? "globe" : "mercator" }}
      cooperativeGestures={!globe}
      reuseMaps={false}
      attributionControl={{ compact: true }}
      locale={{
        "Map.Title": "地图",
        "NavigationControl.ZoomIn": "放大地图",
        "NavigationControl.ZoomOut": "缩小地图",
        "AttributionControl.ToggleAttribution": "地图来源",
        "Popup.Close": "关闭地点信息",
        "CooperativeGesturesHandler.WindowsHelpText":
          "按住 Ctrl 并滚动以缩放地图",
        "CooperativeGesturesHandler.MacHelpText": "按住 ⌘ 并滚动以缩放地图",
        "CooperativeGesturesHandler.MobileHelpText": "使用双指移动地图",
      }}
      maxPitch={0}
      onLoad={() => setLoaded(true)}
      onError={onFailure}
      onMoveEnd={(event) => onCamera?.(event.viewState)}
      onClick={
        onPick
          ? (event) => onPick(event.lngLat.wrap().lng, event.lngLat.lat)
          : undefined
      }
      style={{ width: "100%", height: "100%" }}
    >
      <NavigationControl position="top-left" showCompass={false} />
      {card.connectPoints && (
        <Source id="place-connections" type="geojson" data={connections}>
          <Layer
            id="place-connections-line"
            type="line"
            paint={{
              "line-color": "#327cc7",
              "line-width": 2.5,
              "line-dasharray": [2, 2],
            }}
          />
        </Source>
      )}
      {card.places.map((place, index) => {
        const [longitude, latitude] = mapCoordinate(place);
        return (
          <Marker
            key={`${place.name}:${longitude}:${latitude}`}
            longitude={longitude}
            latitude={globe ? latitude : Math.max(-85, Math.min(85, latitude))}
            anchor="bottom"
            onClick={(event) => {
              event.originalEvent.stopPropagation();
              setSelected(index);
            }}
          >
            <button
              type="button"
              className={styles.marker}
              aria-label={`地点 ${index + 1}：${place.name}`}
            >
              {index + 1}
            </button>
          </Marker>
        );
      })}
      {popup && (
        <Popup
          longitude={mapCoordinate(popup)[0]}
          latitude={mapCoordinate(popup)[1]}
          anchor="bottom"
          onClose={() => setSelected(null)}
          closeOnClick={false}
        >
          <div className={styles.popup}>
            <strong>{popup.name}</strong>
            <span>{popup.displayName}</span>
          </div>
        </Popup>
      )}
    </Map>
  );
}
