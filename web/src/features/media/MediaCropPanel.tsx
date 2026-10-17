import { Slider } from "@base-ui/react/slider";
import { useCallback, useEffect, useRef, useState } from "react";
import Cropper, { type MediaSize } from "react-easy-crop";
import { getInitialCropFromCroppedAreaPercentages } from "react-easy-crop";
import "react-easy-crop/react-easy-crop.css";
import { Icon } from "../../components/icons";
import type { MediaCropAspect, MediaCropSelection } from "./mediaCrop";
import styles from "./MediaDescriptionEditor.module.css";

export function MediaCropPanel({
  source,
  initial,
  disabled,
  active = true,
  onChange,
}: {
  source: string;
  initial?: MediaCropSelection;
  disabled: boolean;
  active?: boolean;
  onChange: (selection: MediaCropSelection) => void;
}) {
  const [stage, setStage] = useState<HTMLDivElement | null>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [media, setMedia] = useState<MediaSize | null>(null);
  const [aspect, setAspect] = useState<MediaCropAspect>(
    initial?.aspect ?? "original",
  );
  const [crop, setCrop] = useState({ x: 0, y: 0 });
  const [zoom, setZoom] = useState(0);
  const [error, setError] = useState(false);
  const percentages = useRef(initial?.percentages);
  const interacted = useRef(false);
  const callback = useRef(onChange);
  callback.current = onChange;
  const acceptMediaSize = useCallback((next: MediaSize) => {
    // Resize can arrive before image decoding, or while its host is hidden.
    // Zero natural dimensions must never become a NaN aspect ratio.
    const dimensions = [
      next.width,
      next.height,
      next.naturalWidth,
      next.naturalHeight,
    ];
    if (!dimensions.every((value) => Number.isFinite(value) && value > 0))
      return;
    setMedia((current) =>
      current &&
      current.width === next.width &&
      current.height === next.height &&
      current.naturalWidth === next.naturalWidth &&
      current.naturalHeight === next.naturalHeight
        ? current
        : next,
    );
  }, []);
  useEffect(() => {
    if (!stage) return;
    let previousWidth = 0;
    let previousHeight = 0;
    const observer = new ResizeObserver(([entry]) => {
      const { width, height } = entry.contentRect;
      if (width <= 0 || height <= 0) {
        interacted.current = false;
        return;
      }
      if (width === previousWidth && height === previousHeight) return;
      previousWidth = width;
      previousHeight = height;
      // Layout is not an edit. Preserve the last chosen source percentages
      // while the cropper recomputes its rendered image and frame sizes.
      interacted.current = false;
      setSize({ width, height });
    });
    observer.observe(stage);
    return () => observer.disconnect();
  }, [stage]);
  const ratio =
    aspect === "square"
      ? 1
      : aspect === "wide"
        ? 16 / 9
        : media
          ? media.naturalWidth / media.naturalHeight
          : 1;
  const width = Math.max(
    1,
    Math.min(size.width - 50, (size.height - 50) * ratio),
  );
  const cropSize = { width, height: width / ratio };
  const minZoom = media
    ? Math.max(cropSize.width / media.width, cropSize.height / media.height)
    : 1;
  useEffect(() => {
    if (!active || !media || !percentages.current) return;
    const restored = getInitialCropFromCroppedAreaPercentages(
      percentages.current,
      media,
      0,
      { width, height: width / ratio },
      minZoom,
      minZoom * 4,
    );
    setCrop(restored.crop);
    setZoom(Math.max(0, Math.min(2, Math.log2(restored.zoom / minZoom))));
  }, [active, media, width, ratio, minZoom]);
  const choose = (value: MediaCropAspect) => {
    interacted.current = true;
    percentages.current = undefined;
    setCrop({ x: 0, y: 0 });
    setZoom(0);
    setAspect(value);
  };
  return (
    <>
      <div
        className={styles.cropStage}
        ref={setStage}
        aria-busy={disabled}
        inert={disabled}
      >
        {size.width > 0 && size.height > 0 ? (
          <Cropper
            image={source}
            crop={crop}
            zoom={minZoom * 2 ** zoom}
            minZoom={minZoom}
            maxZoom={minZoom * 4}
            aspect={ratio}
            cropSize={cropSize}
            objectFit="contain"
            showGrid={false}
            restrictPosition
            roundCropAreaPixels={false}
            disableAutomaticStylesInjection
            classes={{ cropAreaClassName: styles.cropArea }}
            cropperProps={{ "aria-label": "Image crop area" }}
            mediaProps={{ onError: () => setError(true) }}
            setMediaSize={acceptMediaSize}
            onCropChange={(next) => {
              if (active && Number.isFinite(next.x) && Number.isFinite(next.y))
                setCrop((current) =>
                  current.x === next.x && current.y === next.y ? current : next,
                );
            }}
            onZoomChange={(value) => {
              if (active && Number.isFinite(value) && value > 0)
                setZoom(Math.max(0, Math.min(2, Math.log2(value / minZoom))));
            }}
            onInteractionStart={() => {
              interacted.current = true;
            }}
            onTouchRequest={() => !disabled}
            onWheelRequest={() => !disabled}
            onCropComplete={(area, pixels) => {
              if (
                active &&
                interacted.current &&
                [
                  area.x,
                  area.y,
                  area.width,
                  area.height,
                  pixels.x,
                  pixels.y,
                  pixels.width,
                  pixels.height,
                ].every(Number.isFinite) &&
                pixels.width > 0 &&
                pixels.height > 0
              ) {
                percentages.current = area;
                callback.current({ aspect, area: pixels, percentages: area });
              }
            }}
          />
        ) : null}
        {error ? (
          <p className={styles.cropError} role="alert">
            Unable to read this image. Return and choose another image.
          </p>
        ) : null}
      </div>
      <footer className={styles.cropFooter}>
        <div className={styles.ratios}>
          {(["original", "wide", "square"] as const).map((value) => (
            <button
              type="button"
              key={value}
              aria-label={`Aspect ratio: ${value}`}
              aria-pressed={aspect === value}
              disabled={disabled || !media || error}
              onClick={() => choose(value)}
            >
              <Icon name={`aspect-${value}`} />
            </button>
          ))}
        </div>
        <div className={styles.zoom}>
          <Icon name="zoom-out" />
          <Slider.Root
            key={active ? "visible" : "hidden"}
            className={styles.zoomSlider}
            value={zoom}
            min={0}
            max={2}
            step={0.002}
            largeStep={0.1}
            thumbAlignment="edge"
            disabled={disabled || !media || error}
            onValueChange={(value) => {
              interacted.current = true;
              setZoom(value);
            }}
          >
            <Slider.Control className={styles.zoomControl}>
              <Slider.Track className={styles.zoomTrack}>
                <Slider.Indicator className={styles.zoomIndicator} />
                <Slider.Thumb
                  className={styles.zoomThumb}
                  aria-label="Zoom in or out on your image."
                />
              </Slider.Track>
            </Slider.Control>
          </Slider.Root>
          <Icon name="zoom-in" />
        </div>
      </footer>
    </>
  );
}
