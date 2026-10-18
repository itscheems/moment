import { useEffect, useRef, useState } from "react";
import { Dialog } from "@base-ui/react/dialog";
import { locationsApi } from "../../api/locations";
import type { LocationInput } from "../../api/client";
import { Icon } from "../../components/icons";
import { ActionTooltip } from "../../components/TimelineInteractions";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { MapCard } from "./MapCard";
import { MAX_MAP_PLACES, type MapCardData, type MapPlace } from "./types";
import styles from "./Maps.module.css";

function locationLabel(place: LocationInput): string {
  const prefix = [
    place.country || place.countryCode,
    place.district || place.city || place.region,
  ].filter((part) => part && !place.displayName.includes(part));
  return [...new Set([...prefix, place.displayName])].join(" · ");
}

const WORLD_POINT: MapPlace = {
  name: "地图选点",
  displayName: "点击地图选择位置",
  latitude: 0,
  longitude: 0,
  coordinateSystem: "wgs84",
};

export function MapCardEditor({
  value,
  onChange,
  disabled = false,
  triggerOnly = false,
  onOpenChange,
}: {
  value: MapCardData | null;
  onChange: (card: MapCardData | null) => void;
  disabled?: boolean;
  triggerOnly?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const [open, setOpen] = useState(false);
  const lookup = useRef<AbortController | null>(null);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<LocationInput[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [manual, setManual] = useState(false);
  const [picked, setPicked] = useState<MapPlace | null>(null);
  const [manualName, setManualName] = useState("");
  const atLimit = (value?.places.length ?? 0) >= MAX_MAP_PLACES;
  useDocumentScrollLock(open);
  useEffect(() => {
    if (!open || query.trim().length < 2 || manual) {
      setLoading(false);
      if (query.trim().length < 2) setResults([]);
      return;
    }
    const controller = new AbortController();
    lookup.current?.abort();
    lookup.current = controller;
    const timer = window.setTimeout(() => {
      setLoading(true);
      setError("");
      void locationsApi
        .searchLocations(query.trim(), undefined, controller.signal, "global")
        .then((items) => {
          if (!controller.signal.aborted) setResults(items);
        })
        .catch(() => {
          if (!controller.signal.aborted)
            setError("地点搜索暂时不可用，可以重试或在地图上选点。");
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false);
        });
    }, 280);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [query, open, manual]);
  const changeOpen = (next: boolean) => {
    setOpen(next);
    onOpenChange?.(next);
    if (!next) {
      lookup.current?.abort();
      setLoading(false);
    }
  };
  const show = () => {
    lookup.current?.abort();
    setLoading(false);
    setQuery("");
    setResults([]);
    setError("");
    setManual(false);
    setPicked(null);
    setManualName("");
    changeOpen(true);
  };
  const add = (place: MapPlace) => {
    if (disabled || atLimit) return;
    const places = value?.places ?? [];
    const duplicate = places.some(
      (item) =>
        item.name === place.name &&
        item.latitude === place.latitude &&
        item.longitude === place.longitude &&
        item.coordinateSystem === place.coordinateSystem,
    );
    if (duplicate) {
      setError("这个地点已在地图中。");
      return;
    }
    onChange({
      places: [...places, place],
      connectPoints: value?.connectPoints ?? false,
    });
    changeOpen(false);
  };
  const select = (place: LocationInput) => {
    if (
      place.latitude === null ||
      place.longitude === null ||
      place.coordinateSystem === null
    )
      return;
    add({
      name: place.name,
      displayName: Array.from(locationLabel(place)).slice(0, 500).join(""),
      latitude: place.latitude,
      longitude: place.longitude,
      coordinateSystem: place.coordinateSystem,
    });
  };
  const nearby = () => {
    if (!navigator.geolocation) {
      setError("浏览器不支持定位，仍可搜索或地图选点。");
      return;
    }
    const controller = new AbortController();
    lookup.current?.abort();
    lookup.current = controller;
    setLoading(true);
    setError("");
    navigator.geolocation.getCurrentPosition(
      (position) => {
        if (controller.signal.aborted) return;
        const point: MapPlace = {
          name: "当前位置",
          displayName: "当前位置",
          latitude: position.coords.latitude,
          longitude: position.coords.longitude,
          coordinateSystem: "wgs84",
        };
        setPicked(point);
        void locationsApi
          .nearbyLocations(point, controller.signal)
          .then((items) => {
            if (!controller.signal.aborted) setResults(items);
          })
          .catch(() => setError("附近地点暂时不可用，仍可选择当前坐标。"))
          .finally(() => {
            if (!controller.signal.aborted) setLoading(false);
          });
      },
      () => {
        if (controller.signal.aborted) return;
        setLoading(false);
        setError("未取得位置权限或定位超时，仍可搜索其他国家的地点。");
      },
      { timeout: 8000, maximumAge: 300000 },
    );
  };
  const reorder = (index: number, direction: number) => {
    if (!value) return;
    const places = [...value.places];
    [places[index], places[index + direction]] = [
      places[index + direction],
      places[index],
    ];
    onChange({ ...value, places });
  };
  return (
    <>
      {triggerOnly ? (
        <ActionTooltip label="添加地图">
          <button
            type="button"
            aria-label="添加地图"
            className={styles.toolbarButton}
            disabled={disabled || atLimit}
            onMouseDown={(event) => event.preventDefault()}
            onClick={show}
          >
            <Icon name="map" />
          </button>
        </ActionTooltip>
      ) : (
        value && (
          <div data-map-editor="">
            <MapCard card={value} />
            <div className={styles.editActions}>
              <ol>
                {value.places.map((place, index) => (
                  <li key={`${index}:${place.name}`}>
                    <span>
                      {index + 1}. {place.name}
                    </span>
                    <button
                      type="button"
                      disabled={disabled || index === 0}
                      aria-label={`上移${place.name}`}
                      onClick={() => reorder(index, -1)}
                    >
                      ↑
                    </button>
                    <button
                      type="button"
                      disabled={disabled || index === value.places.length - 1}
                      aria-label={`下移${place.name}`}
                      onClick={() => reorder(index, 1)}
                    >
                      ↓
                    </button>
                    <button
                      type="button"
                      disabled={disabled}
                      aria-label={`移除${place.name}`}
                      onClick={() => {
                        const places = value.places.filter(
                          (_, i) => i !== index,
                        );
                        onChange(
                          places.length
                            ? {
                                places,
                                connectPoints:
                                  places.length > 1 && value.connectPoints,
                              }
                            : null,
                        );
                      }}
                    >
                      ×
                    </button>
                  </li>
                ))}
              </ol>
              <button
                className={styles.addButton}
                type="button"
                disabled={disabled || atLimit}
                onClick={show}
              >
                ＋ 添加地点
              </button>
              {value.places.length > 1 && (
                <label>
                  <input
                    type="checkbox"
                    checked={value.connectPoints}
                    disabled={disabled}
                    onChange={(event) =>
                      onChange({
                        ...value,
                        connectPoints: event.target.checked,
                      })
                    }
                  />{" "}
                  连接地点
                </label>
              )}
              <button
                type="button"
                disabled={disabled}
                onClick={() => onChange(null)}
              >
                移除地图
              </button>
            </div>
          </div>
        )
      )}
      <Dialog.Root open={open} onOpenChange={changeOpen}>
        <Dialog.Portal>
          <Dialog.Backdrop className={styles.backdrop} />
          <Dialog.Popup className={styles.picker}>
            <header className={styles.dialogHeader}>
              <Dialog.Title>添加地图地点</Dialog.Title>
              <Dialog.Close aria-label="关闭地点选择">
                <Icon name="close" />
              </Dialog.Close>
            </header>
            <div className={styles.pickerBody}>
              <p className={styles.hint}>
                选择城市、餐馆或景点，也可以分享其他国家去过的地方。
              </p>
              <div className={styles.search}>
                <input
                  aria-label="搜索全球地点"
                  placeholder="搜索城市、餐馆、景点…"
                  value={query}
                  onChange={(event) => {
                    setManual(false);
                    setResults([]);
                    setError("");
                    setLoading(event.target.value.trim().length >= 2);
                    setQuery(event.target.value);
                  }}
                />
                <button type="button" disabled={loading} onClick={nearby}>
                  当前位置
                </button>
              </div>
              {loading && <p role="status">正在查找地点…</p>}
              {error && (
                <p className={styles.error} role="alert">
                  {error}
                </p>
              )}
              <div className={styles.results}>
                {!manual && picked && (
                  <button type="button" onClick={() => add(picked)}>
                    <strong>使用当前坐标</strong>
                    <span>将精确位置加入地图</span>
                  </button>
                )}
                {!manual &&
                  results.map((place, index) => (
                    <button
                      key={`${place.provider}:${place.providerPlaceId}:${index}`}
                      type="button"
                      disabled={
                        place.latitude === null || place.longitude === null
                      }
                      onClick={() => select(place)}
                    >
                      <strong>{place.name}</strong>
                      <span>{locationLabel(place)}</span>
                    </button>
                  ))}
                {!loading &&
                  !manual &&
                  query.trim().length > 1 &&
                  results.length === 0 && (
                    <p className={styles.hint}>
                      没有找到地点。试着加上城市或国家名称，或在地图上选点。
                    </p>
                  )}
                <button
                  type="button"
                  onClick={() => {
                    setManual(true);
                    setError("");
                  }}
                >
                  在地图上选点
                </button>
              </div>
              {manual && (
                <div className={styles.manual}>
                  <MapCard
                    card={{
                      places: [picked ?? WORLD_POINT],
                      connectPoints: false,
                    }}
                    onPick={(longitude, latitude) =>
                      setPicked({ ...WORLD_POINT, longitude, latitude })
                    }
                  />
                  <input
                    aria-label="地点名称"
                    placeholder="给选中的地点起个名字"
                    value={manualName}
                    onChange={(event) => setManualName(event.target.value)}
                    maxLength={160}
                  />
                  <button
                    type="button"
                    disabled={!picked || !manualName.trim()}
                    onClick={() =>
                      picked &&
                      add({
                        ...picked,
                        name: manualName.trim(),
                        displayName: manualName.trim(),
                      })
                    }
                  >
                    添加选中地点
                  </button>
                </div>
              )}
            </div>
          </Dialog.Popup>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}
