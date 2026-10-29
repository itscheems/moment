import { useTranslation } from "react-i18next";
import { Dialog } from "../../components/Dialog";
import { Slider } from "@base-ui/react/slider";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import Cropper, { type Area, type MediaSize } from "react-easy-crop";
import "react-easy-crop/react-easy-crop.css";
import { Icon } from "../../components/icons";
import { mediaApi } from "../../api/media";
import { createMediaCrop, type MediaCropSource } from "../media/mediaCrop";
import styles from "./ProfileMediaCrop.module.css";

const MAX_ZOOM_FACTOR = 6;

function ZoomIcon({ increase }: { increase?: boolean }) {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24">
      <path d="M11 4c-3.87 0-7 3.13-7 7s3.13 7 7 7c1.93 0 3.68-.78 4.95-2.05C17.21 14.68 18 12.93 18 11c0-3.87-3.14-7-7-7zm-9 7c0-4.97 4.03-9 9-9s9 4.03 9 9c0 2.12-.74 4.08-1.97 5.62l3.68 3.67-1.42 1.42-3.67-3.68C15.08 19.26 13.12 20 11 20c-4.97 0-9-4.03-9-9z" />
      <path
        d={
          increase ? "M10 7h2v3h3v2h-3v3h-2v-3H7v-2h3z" : "M14.5 12h-7v-2h7v2z"
        }
      />
    </svg>
  );
}

export function ProfileMediaCrop({
  kind,
  file,
  csrfToken,
  onSessionError,
  onCancel,
  onApply,
}: {
  kind: "avatar" | "cover";
  file: File;
  csrfToken: string;
  onSessionError: (error: unknown) => boolean;
  onCancel: () => void;
  onApply: (cropped: File) => void;
}) {
  const { t } = useTranslation();

  const [source, setSource] = useState<MediaCropSource | null>(null);
  const [stage, setStage] = useState<HTMLDivElement | null>(null);
  const [stageSize, setStageSize] = useState({ width: 0, height: 0 });
  const [media, setMedia] = useState<MediaSize | null>(null);
  const [mediaReady, setMediaReady] = useState(false);
  const [crop, setCrop] = useState({ x: 0, y: 0 });
  const [zoomLevel, setZoomLevel] = useState(0);
  const [pixels, setPixels] = useState<Area | null>(null);
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const exportController = useRef<AbortController | null>(null);
  const cropper = useRef<Cropper>(null);
  const locked = applying;
  const isCover = kind === "cover";
  const aspect = isCover ? 3 : 1;
  const cropSize = useMemo(() => {
    const width = Math.min(
      stageSize.width - (isCover ? 50 : 100),
      (stageSize.height - 50) * aspect,
    );
    return width > 0 ? { width, height: width / aspect } : undefined;
  }, [stageSize, isCover, aspect]);
  const minZoom =
    cropSize && media && media.width > 0 && media.height > 0
      ? Math.max(cropSize.width / media.width, cropSize.height / media.height)
      : 1;
  const maxZoomFactor = MAX_ZOOM_FACTOR;
  const maxZoomLevel = Math.log2(maxZoomFactor);

  useLayoutEffect(() => {
    // The engine handles a simultaneous zoom/frame update as a zoom change.
    // Recompute with the committed frame so a resize cannot keep the old box.
    if (mediaReady) cropper.current?.computeSizes();
  }, [cropSize, mediaReady]);

  useEffect(() => {
    const controller = new AbortController();
    let local: string | null = null;
    const prepare = async () => {
      setSource(null);
      setError(null);
      try {
        if (
          /\.(heic|heif)$/i.test(file.name) ||
          /image\/hei[cf]/i.test(file.type)
        ) {
          // Use the existing owned upload/preview path for formats the browser
          // cannot decode. This does not bind or replace a profile picture.
          const uploaded = await mediaApi.uploadMedia(
            csrfToken,
            file,
            undefined,
            controller.signal,
          );
          if (!controller.signal.aborted)
            setSource({
              url: uploaded.previewUrl,
              name: file.name,
              mimeType: uploaded.previewMimeType,
            });
        } else {
          local = URL.createObjectURL(file);
          setSource({ url: local, name: file.name, mimeType: file.type });
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          onSessionError(error);
          setError(t("图片没有读取成功，请返回重新选择。"));
        }
      }
    };
    void prepare();
    return () => {
      controller.abort();
      exportController.current?.abort();
      if (local) URL.revokeObjectURL(local);
    };
  }, [file, csrfToken, onSessionError]);

  useEffect(() => {
    if (!stage) return;
    const measure = () => {
      const { width, height } = stage.getBoundingClientRect();
      setStageSize({ width, height });
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(stage);
    return () => observer.disconnect();
  }, [stage]);

  const cancel = () => {
    exportController.current?.abort();
    onCancel();
  };
  const apply = async () => {
    if (!source || !pixels || !mediaReady || locked) return;
    const controller = new AbortController();
    exportController.current = controller;
    setApplying(true);
    setError(null);
    try {
      const cropped = await createMediaCrop(source, pixels, controller.signal);
      if (!controller.signal.aborted) onApply(cropped);
    } catch {
      if (!controller.signal.aborted) {
        setError(t("图片生成没有成功，请重试。"));
      }
    } finally {
      if (!controller.signal.aborted) setApplying(false);
    }
  };

  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open) cancel();
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.backdrop} forceRender />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup
            className={`${styles.popup} ${isCover ? styles.coverMode : ""}`}
          >
            <header className={styles.header}>
              <button
                aria-label={t("返回")}
                className={styles.back}
                onClick={cancel}
                type="button"
              >
                <Icon name="arrow-left" />
              </button>
              <Dialog.Title className={styles.title}>
                {t("编辑媒体")}
              </Dialog.Title>
              <button
                className={styles.apply}
                disabled={!pixels || !mediaReady || locked}
                onClick={() => void apply()}
                type="button"
              >
                {applying ? t("正在处理…") : t("应用")}
              </button>
            </header>
            <Dialog.Description className={styles.srOnly}>
              {t("拖动图片调整")}
              {isCover ? t("背景") : t("头像")}
              {t(
                "位置，使用滑块、鼠标滚轮或触控板放大或缩小。聚焦图片后可用方向键移动，按 Escape 返回。",
              )}
            </Dialog.Description>
            <div aria-busy={locked} className={styles.stage} ref={setStage}>
              <div className={styles.cropperHost} inert={locked}>
                {source && cropSize ? (
                  <Cropper
                    ref={cropper}
                    image={source.url}
                    crop={crop}
                    zoom={minZoom * 2 ** zoomLevel}
                    minZoom={minZoom}
                    maxZoom={minZoom * maxZoomFactor}
                    aspect={aspect}
                    zoomWithScroll
                    cropSize={cropSize}
                    showGrid={false}
                    restrictPosition
                    roundCropAreaPixels={false}
                    disableAutomaticStylesInjection
                    classes={{ cropAreaClassName: styles.cropArea }}
                    cropperProps={{
                      "aria-label": isCover
                        ? t("背景裁切区域")
                        : t("头像裁切区域"),
                    }}
                    mediaProps={{
                      onError: () => {
                        setMediaReady(false);
                        setPixels(null);
                        setError(
                          t(
                            "无法读取这张图片，请返回并选择 JPEG、PNG 或 WebP 图片。",
                          ),
                        );
                      },
                    }}
                    onMediaLoaded={(loaded) =>
                      setMediaReady(
                        loaded.naturalWidth > 0 && loaded.naturalHeight > 0,
                      )
                    }
                    onCropChange={setCrop}
                    onCropComplete={(_, nextPixels) => setPixels(nextPixels)}
                    onZoomChange={(zoom) =>
                      setZoomLevel(
                        Math.max(
                          0,
                          Math.min(maxZoomLevel, Math.log2(zoom / minZoom)),
                        ),
                      )
                    }
                    setMediaSize={setMedia}
                  />
                ) : null}
              </div>
              {!source && !error ? (
                <p className={styles.loading} role="status">
                  {t("正在读取图片…")}
                </p>
              ) : null}
              {error ? (
                <p className={styles.error} role="alert">
                  {error}
                </p>
              ) : null}
            </div>
            <footer className={styles.footer}>
              <ZoomIcon />
              <Slider.Root
                className={styles.slider}
                value={zoomLevel}
                min={0}
                max={maxZoomLevel}
                thumbAlignment="edge"
                step={0.01}
                largeStep={0.1}
                disabled={locked || !mediaReady}
                onValueChange={setZoomLevel}
              >
                <Slider.Control className={styles.sliderControl}>
                  <Slider.Track className={styles.sliderTrack}>
                    <Slider.Indicator className={styles.sliderIndicator} />
                    <Slider.Thumb
                      className={styles.sliderThumb}
                      aria-label={t("放大或缩小图片")}
                      getAriaValueText={(_, value) =>
                        `${Math.round(2 ** value * 100)}%`
                      }
                    />
                  </Slider.Track>
                </Slider.Control>
              </Slider.Root>
              <ZoomIcon increase />
            </footer>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
