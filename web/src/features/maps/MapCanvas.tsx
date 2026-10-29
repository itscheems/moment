import { useTranslation } from "react-i18next";
import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
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
import {
  mapBounds,
  mapConnections,
  mapCoordinate,
  mapFitOptions,
  mapPlaceIdentity,
} from "./geometry";
import type { MapCardData, MapPlace } from "./types";
import {
  applyMapWaterColor,
  MAP_CONNECTION_PAINT,
  MAP_STYLE,
} from "./mapAppearance";
import styles from "./Maps.module.css";

maplibre.setWorkerUrl(workerUrl);

export interface MapCamera {
  longitude: number;
  latitude: number;
  zoom: number;
  bearing: number;
  pitch: number;
}

export interface MapViewport extends MapCamera {
  width: number;
  height: number;
}

export interface MapPlaceFocus {
  identity: string;
  camera: MapCamera;
}

export default function MapCanvas({
  card,
  globe = false,
  camera,
  onCamera,
  onViewport,
  onFailure,
  onPick,
  focusTarget,
  placeFocus,
  fullscreen = false,
}: {
  card: MapCardData;
  globe?: boolean;
  camera?: MapCamera;
  onCamera?: (camera: MapCamera) => void;
  onViewport?: (viewport: MapViewport) => void;
  onFailure: () => void;
  onPick?: (longitude: number, latitude: number) => void;
  focusTarget?: { place: MapPlace } | null;
  placeFocus: RefObject<MapPlaceFocus | null>;
  fullscreen?: boolean;
}) {
  const { t } = useTranslation();

  const ref = useRef<MapRef>(null);
  const [loaded, setLoaded] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const bounds = useMemo(() => mapBounds(card.places), [card.places]);
  const connections = useMemo(() => mapConnections(card), [card]);
  const pointsKey = card.places
    .map(
      (point) =>
        `${point.latitude},${point.longitude},${point.coordinateSystem}`,
    )
    .sort()
    .join(";");
  const fittedKey = useRef(camera ? pointsKey : "");
  useEffect(() => {
    if (loaded && !onPick && fittedKey.current !== pointsKey) {
      placeFocus.current = null;
      ref.current?.fitBounds(bounds, {
        ...mapFitOptions(card.places),
        duration: 0,
      });
    }
    if (loaded) fittedKey.current = pointsKey;
  }, [pointsKey, loaded, bounds, card.places.length, onPick, placeFocus]);
  useEffect(() => {
    if (loaded) return;
    const timer = window.setTimeout(onFailure, 20_000);
    return () => window.clearTimeout(timer);
  }, [loaded, onFailure]);
  const popup = card.places.find(
    (place) => mapPlaceIdentity(place) === selected,
  );
  useEffect(() => {
    setSelected(null);
  }, [pointsKey]);
  useEffect(() => {
    const map = ref.current;
    if (!loaded || !focusTarget || !map) return;
    const identity = mapPlaceIdentity(focusTarget.place);
    const previous = placeFocus.current;
    setSelected(null);
    if (previous?.identity === identity) {
      placeFocus.current = null;
      map.flyTo({
        center: [previous.camera.longitude, previous.camera.latitude],
        zoom: previous.camera.zoom,
        bearing: previous.camera.bearing,
        pitch: previous.camera.pitch,
        duration: 1400,
        curve: 1.42,
      });
      return;
    }
    const center = map.getCenter();
    placeFocus.current = {
      identity,
      camera: previous?.camera ?? {
        longitude: center.lng,
        latitude: center.lat,
        zoom: map.getZoom(),
        bearing: map.getBearing(),
        pitch: map.getPitch(),
      },
    };
    const [longitude, latitude] = mapCoordinate(focusTarget.place);
    const target = new maplibre.LngLat(longitude, latitude);
    const nearest = card.places
      .map((place) => ({
        place,
        distance: target.distanceTo(
          maplibre.LngLat.convert(mapCoordinate(place)),
        ),
      }))
      .filter(({ distance }) => distance > 0)
      .sort((a, b) => a.distance - b.distance)[0];
    const nearbyZoom = nearest
      ? map.cameraForBounds(mapBounds([focusTarget.place, nearest.place]), {
          padding: 48,
          maxZoom: 19,
        })?.zoom
      : undefined;
    map.flyTo({
      center: [
        longitude,
        globe ? latitude : Math.max(-85, Math.min(85, latitude)),
      ],
      // Use nearby spacing to reveal street detail without backing away.
      zoom: Math.max(map.getZoom(), 16, Math.min(18, (nearbyZoom ?? 17) - 1)),
      bearing: 0,
      pitch: 0,
      duration: 1400,
      curve: 1.42,
    });
  }, [loaded, focusTarget, globe, placeFocus, card.places]);
  const reportCamera = (map: maplibre.Map) => {
    const center = map.getCenter();
    onViewport?.({
      longitude: center.lng,
      latitude: center.lat,
      zoom: map.getZoom(),
      bearing: map.getBearing(),
      pitch: map.getPitch(),
      width: map.getCanvas().clientWidth,
      height: map.getCanvas().clientHeight,
    });
  };
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
              fitBoundsOptions: mapFitOptions(card.places),
            })
      }
      mapStyle={MAP_STYLE}
      projection={{ type: globe ? "globe" : "mercator" }}
      cooperativeGestures={!fullscreen && !globe}
      reuseMaps={false}
      attributionControl={{ compact: true }}
      locale={{
        "Map.Title": t("地图"),
        "NavigationControl.ZoomIn": t("放大地图"),
        "NavigationControl.ZoomOut": t("缩小地图"),
        "AttributionControl.ToggleAttribution": t("地图来源"),
        "Popup.Close": t("关闭地点信息"),
        "CooperativeGesturesHandler.WindowsHelpText":
          t("按住 Ctrl 并滚动以缩放地图"),
        "CooperativeGesturesHandler.MacHelpText": t("按住 ⌘ 并滚动以缩放地图"),
        "CooperativeGesturesHandler.MobileHelpText": t("使用双指移动地图"),
      }}
      maxPitch={0}
      onStyleData={({ target }) => applyMapWaterColor(target)}
      onLoad={({ target }) => {
        setLoaded(true);
        reportCamera(target);
      }}
      onError={onFailure}
      onMove={({ target }) => reportCamera(target)}
      onMoveEnd={(event) => onCamera?.(event.viewState)}
      onResize={({ target }) => reportCamera(target)}
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
            paint={MAP_CONNECTION_PAINT}
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
            anchor="center"
            onClick={(event) => {
              event.originalEvent.stopPropagation();
              setSelected(mapPlaceIdentity(place));
            }}
          >
            <button
              type="button"
              className={styles.marker}
              aria-label={t("地点 {{value1}}：{{value2}}", {
                value1: index + 1,
                value2: place.name,
              })}
            ></button>
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
