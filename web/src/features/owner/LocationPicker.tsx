import { Dialog } from "@base-ui/react/dialog";
import { useEffect, useMemo, useRef, useState } from "react";

import { PublicApiError } from "../../api/public";
import type { OwnerApi, OwnerLocation } from "../../api/owner";
import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import styles from "./Owner.module.css";

function locationError(error: unknown): string {
  if (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    error.code === 1
  ) {
    return "浏览器没有位置权限。你仍可搜索或手动添加地点。";
  }
  if (error instanceof DOMException && error.name === "NotAllowedError") {
    return "浏览器没有位置权限。你仍可搜索或手动添加地点。";
  }
  if (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    error.code === 3
  ) {
    return "获取当前位置超时。你仍可搜索或手动添加地点。";
  }
  if (error instanceof PublicApiError) {
    if (error.code === "LOCATION_PROVIDER_UNAVAILABLE") {
      return "地点服务暂时不可用，稍后重试或手动添加地点。";
    }
    if (error.code === "UNAUTHORIZED") {
      return "所有者会话已失效，请刷新身份后重试。";
    }
  }
  return "暂时无法取得地点，请检查连接后重试。";
}

function currentPosition(): Promise<GeolocationPosition> {
  return new Promise((resolve, reject) => {
    if (!navigator.geolocation) {
      reject(
        new DOMException("Geolocation is unavailable", "NotSupportedError"),
      );
      return;
    }
    navigator.geolocation.getCurrentPosition(resolve, reject, {
      enableHighAccuracy: false,
      maximumAge: 300_000,
      timeout: 8_000,
    });
  });
}

export function LocationPicker({
  api,
  disabled,
  onChange,
  value,
}: {
  api: OwnerApi;
  disabled: boolean;
  onChange: (location: OwnerLocation | null) => void;
  value: OwnerLocation | null;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<OwnerLocation[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [manual, setManual] = useState(false);
  const [manualName, setManualName] = useState("");
  const [bias, setBias] = useState<{ latitude: number; longitude: number }>();
  const requestRef = useRef<AbortController | null>(null);
  const usesGeoapify = useMemo(
    () => results.some((location) => location.provider === "geoapify"),
    [results],
  );

  useDocumentScrollLock(open);

  useEffect(() => {
    if (!open || manual || query.trim().length < 2) return;
    const controller = new AbortController();
    requestRef.current?.abort();
    requestRef.current = controller;
    const timer = window.setTimeout(() => {
      setLoading(true);
      setError(null);
      void api
        .searchLocations(query.trim(), bias, controller.signal)
        .then((items) => setResults(items))
        .catch((nextError: unknown) => {
          if (!controller.signal.aborted) setError(locationError(nextError));
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false);
        });
    }, 280);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [api, bias, manual, open, query]);

  useEffect(
    () => () => {
      requestRef.current?.abort();
    },
    [],
  );

  const nearby = async () => {
    requestRef.current?.abort();
    const controller = new AbortController();
    requestRef.current = controller;
    setLoading(true);
    setError(null);
    setManual(false);
    try {
      const position = await currentPosition();
      const point = {
        latitude: position.coords.latitude,
        longitude: position.coords.longitude,
      };
      setBias(point);
      setResults(await api.nearbyLocations(point, controller.signal));
    } catch (nextError) {
      if (!controller.signal.aborted) setError(locationError(nextError));
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  };

  const select = (location: OwnerLocation) => {
    onChange(location);
    setOpen(false);
  };

  const addManual = () => {
    const name = manualName.trim();
    if (!name) return;
    select({
      name,
      displayName: name,
      countryCode: "",
      country: "",
      region: "",
      city: "",
      district: "",
      category: "",
      provider: "manual",
      providerPlaceId: "",
      latitude: null,
      longitude: null,
      coordinateSystem: null,
    });
  };

  return (
    <Dialog.Root
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (nextOpen) {
          setQuery("");
          setResults([]);
          setError(null);
          setManual(false);
          setManualName("");
        }
      }}
      open={open}
    >
      <Dialog.Trigger
        aria-label={value ? `更改位置：${value.name}` : "添加位置"}
        className={styles.locationTrigger}
        disabled={disabled}
        title={value ? "更改位置" : "添加位置"}
      >
        <Icon name="location-picker" />
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.locationBackdrop} />
        <Dialog.Viewport className={styles.locationViewport}>
          <Dialog.Popup className={styles.locationPopup}>
            <header className={styles.locationHeader}>
              <Dialog.Close
                aria-label="关闭位置选择"
                className={styles.locationClose}
              >
                <Icon name="close" />
              </Dialog.Close>
              <Dialog.Title>添加位置</Dialog.Title>
              {value ? (
                <button
                  className={styles.locationRemove}
                  onClick={() => {
                    onChange(null);
                    setOpen(false);
                  }}
                  type="button"
                >
                  移除
                </button>
              ) : (
                <span />
              )}
            </header>
            <Dialog.Description className={styles.srOnly}>
              搜索地点、使用浏览器当前位置，或手动添加地点标签。
            </Dialog.Description>
            {!manual ? (
              <>
                <label className={styles.locationSearch}>
                  <Icon name="search" />
                  <span className={styles.srOnly}>搜索地点</span>
                  <input
                    autoComplete="off"
                    autoFocus
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder="搜索地点"
                    value={query}
                  />
                </label>
                <button
                  className={styles.locationNearby}
                  disabled={loading}
                  onClick={() => void nearby()}
                  type="button"
                >
                  <span>
                    <Icon name="location" />
                  </span>
                  <span>
                    <strong>使用当前位置</strong>
                    <small>仅在你点击后向浏览器请求权限</small>
                  </span>
                </button>
                <div aria-busy={loading} className={styles.locationResults}>
                  {loading ? (
                    <p className={styles.locationState}>正在查找地点…</p>
                  ) : null}
                  {!loading &&
                  query.trim().length > 0 &&
                  query.trim().length < 2 ? (
                    <p className={styles.locationState}>再输入一个字开始搜索</p>
                  ) : null}
                  {!loading &&
                  !error &&
                  results.length === 0 &&
                  query.trim().length >= 2 ? (
                    <p className={styles.locationState}>没有找到匹配地点</p>
                  ) : null}
                  {results.map((location) => (
                    <button
                      className={styles.locationResult}
                      key={`${location.provider}:${location.providerPlaceId}:${location.name}:${location.displayName}`}
                      onClick={() => select(location)}
                      type="button"
                    >
                      <span>
                        <Icon name="location" />
                      </span>
                      <span>
                        <strong>{location.name}</strong>
                        <small>{location.displayName}</small>
                      </span>
                    </button>
                  ))}
                </div>
                {error ? (
                  <p className={styles.locationError} role="alert">
                    {error}
                  </p>
                ) : null}
                <footer className={styles.locationFooter}>
                  <button onClick={() => setManual(true)} type="button">
                    手动添加地点
                  </button>
                  {usesGeoapify ? (
                    <a
                      href="https://www.geoapify.com/"
                      rel="noreferrer"
                      target="_blank"
                    >
                      Powered by Geoapify
                    </a>
                  ) : results.some((item) => item.provider === "amap") ? (
                    <span>地点数据由高德提供</span>
                  ) : null}
                </footer>
              </>
            ) : (
              <form
                className={styles.locationManual}
                onSubmit={(event) => {
                  event.preventDefault();
                  addManual();
                }}
              >
                <button
                  className={styles.locationBack}
                  onClick={() => setManual(false)}
                  type="button"
                >
                  <Icon name="back" /> 返回搜索
                </button>
                <label>
                  <span>地点名称</span>
                  <input
                    autoFocus
                    maxLength={160}
                    onChange={(event) => setManualName(event.target.value)}
                    placeholder="例如：书房、珠江边"
                    value={manualName}
                  />
                </label>
                <p>手动地点只保存你填写的名称，不会添加坐标。</p>
                <button
                  className={styles.primaryButton}
                  disabled={!manualName.trim()}
                  type="submit"
                >
                  添加
                </button>
              </form>
            )}
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
