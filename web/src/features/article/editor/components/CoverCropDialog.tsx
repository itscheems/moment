import { Dialog } from "@base-ui/react/dialog";
import { Slider } from "@base-ui/react/slider";
import { useEffect, useMemo, useRef, useState } from "react";
import Cropper, { type Area, type MediaSize } from "react-easy-crop";
import "react-easy-crop/react-easy-crop.css";
import { Icon } from "../../../../components/icons";
import { ARTICLE_COVER_ASPECT, createCoverCrop } from "./coverCrop";
import styles from "./CoverCropDialog.module.css";

const MAX_ZOOMED_WIDTH_FACTOR = 2.4;

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

export function CoverCropDialog({
  file,
  busy = false,
  onCancel,
  onApply,
}: {
  file: File;
  busy?: boolean;
  onCancel: () => void;
  onApply: (cropped: File) => void;
}) {
  const [imageUrl, setImageUrl] = useState<string | null>(null);
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
  const cancelButton = useRef<HTMLButtonElement>(null);
  const locked = busy || applying;
  const cropSize = useMemo(() => {
    const width = Math.min(
      stageSize.width - 50,
      stageSize.height * ARTICLE_COVER_ASPECT,
    );
    return width > 0
      ? { width, height: width / ARTICLE_COVER_ASPECT }
      : undefined;
  }, [stageSize]);
  const minZoom =
    cropSize && media && media.width > 0 && media.height > 0
      ? Math.max(cropSize.width / media.width, cropSize.height / media.height)
      : 1;
  const maxZoomFactor =
    cropSize && media && media.width > 0
      ? Math.max(
          2,
          (MAX_ZOOMED_WIDTH_FACTOR * cropSize.width) / (media.width * minZoom),
        )
      : MAX_ZOOMED_WIDTH_FACTOR;
  const maxZoomLevel = Math.log2(maxZoomFactor);

  useEffect(() => {
    exportController.current?.abort();
    exportController.current = null;
    const url = URL.createObjectURL(file);
    setImageUrl(url);
    setCrop({ x: 0, y: 0 });
    setZoomLevel(0);
    setPixels(null);
    setMedia(null);
    setMediaReady(false);
    setApplying(false);
    setError(null);
    return () => {
      exportController.current?.abort();
      URL.revokeObjectURL(url);
    };
  }, [file]);

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
    if (!pixels || !mediaReady || locked) return;
    const controller = new AbortController();
    exportController.current = controller;
    setApplying(true);
    setError(null);
    try {
      const cropped = await createCoverCrop(file, pixels, controller.signal);
      if (!controller.signal.aborted) onApply(cropped);
    } catch (nextError) {
      if (!controller.signal.aborted) {
        setError(
          nextError instanceof Error
            ? nextError.message
            : "封面生成没有成功，请重试。",
        );
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
        <Dialog.Backdrop className={styles.backdrop} />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup className={styles.popup} initialFocus={cancelButton}>
            <header className={styles.header}>
              <button
                aria-label="返回"
                className={styles.back}
                onClick={cancel}
                ref={cancelButton}
                type="button"
              >
                <Icon name="back" />
              </button>
              <Dialog.Title className={styles.title}>编辑媒体</Dialog.Title>
              <button
                className={styles.apply}
                disabled={!pixels || !mediaReady || locked}
                onClick={() => void apply()}
                type="button"
              >
                {busy ? "正在上传…" : applying ? "正在处理…" : "应用"}
              </button>
            </header>
            <Dialog.Description className={styles.srOnly}>
              拖动图片调整封面位置，使用缩放滑块放大或缩小。聚焦图片后可用方向键移动，按
              Escape 返回。
            </Dialog.Description>
            <div aria-busy={locked} className={styles.stage} ref={setStage}>
              <div className={styles.cropperHost} inert={locked}>
                {imageUrl && cropSize ? (
                  <Cropper
                    image={imageUrl}
                    crop={crop}
                    zoom={minZoom * 2 ** zoomLevel}
                    minZoom={minZoom}
                    maxZoom={minZoom * maxZoomFactor}
                    aspect={ARTICLE_COVER_ASPECT}
                    cropSize={cropSize}
                    showGrid={false}
                    restrictPosition
                    roundCropAreaPixels={false}
                    disableAutomaticStylesInjection
                    classes={{ cropAreaClassName: styles.cropArea }}
                    cropperProps={{ "aria-label": "封面裁切区域" }}
                    mediaProps={{
                      onError: () => {
                        setMediaReady(false);
                        setPixels(null);
                        setError(
                          "无法读取这张图片，请返回并选择 JPEG、PNG 或 WebP 图片。",
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
                      aria-label="放大或缩小图片"
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
