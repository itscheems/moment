import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { useEffect, useRef, useState } from "react";
import { Dialog } from "../../components/Dialog";
import { Switch } from "@base-ui/react/switch";
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
  get name() {
    return t("地图选点");
  },
  get displayName() {
    return t("点击地图选择位置");
  },
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
  onOpenChangeComplete,
  editorBlock = false,
}: {
  value: MapCardData | null;
  onChange: (card: MapCardData | null) => void;
  disabled?: boolean;
  triggerOnly?: boolean;
  onOpenChange?: (open: boolean) => void;
  onOpenChangeComplete?: (open: boolean) => void;
  editorBlock?: boolean;
}) {
  const { t } = useTranslation();

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
            setError(t("地点搜索暂时不可用，可以重试或在地图上选点。"));
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
      setError(t("这个地点已在地图中。"));
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
      setError(t("浏览器不支持定位，仍可搜索或地图选点。"));
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
          name: t("当前位置"),
          displayName: t("当前位置"),
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
          .catch(() => setError(t("附近地点暂时不可用，仍可选择当前坐标。")))
          .finally(() => {
            if (!controller.signal.aborted) setLoading(false);
          });
      },
      () => {
        if (controller.signal.aborted) return;
        setLoading(false);
        setError(t("未取得位置权限或定位超时，仍可搜索其他国家的地点。"));
      },
      { timeout: 8000, maximumAge: 300000 },
    );
  };
  return (
    <>
      {triggerOnly ? (
        <ActionTooltip label={t("添加地图")}>
          <button
            type="button"
            aria-label={t("添加地图")}
            className={styles.toolbarButton}
            disabled={disabled || atLimit}
            onMouseDown={(event) => event.preventDefault()}
            onClick={show}
          >
            <Icon
              name="map-picker"
              style={{
                width: "var(--composer-map-icon-size, 22px)",
                height: "var(--composer-map-icon-size, 22px)",
              }}
            />
          </button>
        </ActionTooltip>
      ) : (
        value && (
          <div data-map-editor="" className={styles.editor}>
            <MapCard
              card={value}
              editorBlock={editorBlock}
              onChange={onChange}
              disabled={disabled}
              footer={
                <div className={styles.editActions}>
                  <button
                    className={styles.addButton}
                    type="button"
                    disabled={disabled || atLimit}
                    onClick={show}
                  >
                    <Icon name="plus" /> {t("添加地点")}
                  </button>
                  {value.places.length > 1 && (
                    <label className={styles.connectLabel}>
                      <Switch.Root
                        className={styles.connectSwitch}
                        checked={value.connectPoints}
                        disabled={disabled}
                        onCheckedChange={(checked) =>
                          onChange({
                            ...value,
                            connectPoints: checked,
                          })
                        }
                      >
                        <Switch.Thumb className={styles.connectThumb} />
                      </Switch.Root>
                      {t("连接地点")}
                    </label>
                  )}
                </div>
              }
            />
          </div>
        )
      )}
      <Dialog.Root
        open={open}
        onOpenChange={changeOpen}
        onOpenChangeComplete={onOpenChangeComplete}
      >
        <Dialog.Portal>
          <Dialog.Backdrop
            className={`${styles.backdrop} ${styles.pickerBackdrop}`}
          />
          <Dialog.Popup className={styles.picker}>
            <header className={styles.dialogHeader}>
              <Dialog.Close
                className={styles.pickerClose}
                aria-label={t("关闭地点选择")}
              >
                <Icon name="close" />
              </Dialog.Close>
              <Dialog.Title>{t("添加地图地点")}</Dialog.Title>
            </header>
            <div className={styles.pickerBody}>
              <div className={styles.search}>
                <input
                  aria-label={t("搜索全球地点")}
                  placeholder={t("搜索城市、餐馆、景点…")}
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
                  {t("当前位置")}
                </button>
              </div>
              {loading && <p role="status">{t("正在查找地点…")}</p>}
              {error && (
                <p className={styles.error} role="alert">
                  {error}
                </p>
              )}
              <div className={styles.results}>
                {!manual && picked && (
                  <button type="button" onClick={() => add(picked)}>
                    <strong>{t("使用当前坐标")}</strong>
                    <span>{t("将精确位置加入地图")}</span>
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
                      {t(
                        "没有找到地点。试着加上城市或国家名称，或在地图上选点。",
                      )}
                    </p>
                  )}
                <button
                  type="button"
                  onClick={() => {
                    setManual(true);
                    setError("");
                  }}
                >
                  {t("在地图上选点")}
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
                    aria-label={t("地点名称")}
                    placeholder={t("给选中的地点起个名字")}
                    value={manualName}
                    onChange={(event) => setManualName(event.target.value)}
                    maxLength={160}
                  />
                  <button
                    className={styles.confirmPoint}
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
                    {t("添加选中地点")}
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
