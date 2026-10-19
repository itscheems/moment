import {
  Component,
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Dialog } from "@base-ui/react/dialog";
import { motion, useReducedMotion, type Transition } from "motion/react";
import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { ActionTooltip } from "../../components/TimelineInteractions";
import { MapPlaces } from "./MapPlaces";
import type { MapCardData, MapPlace } from "./types";
import type { MapCamera, MapPlaceFocus } from "./MapCanvas";
import styles from "./Maps.module.css";

const Canvas = lazy(() => import("./MapCanvas"));

class MapFailureBoundary extends Component<
  { children: ReactNode; fallback: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}

export function MapCard({
  card,
  deferMap = false,
  onPick,
  onChange,
  disabled,
  footer,
}: {
  card: MapCardData;
  deferMap?: boolean;
  onPick?: (longitude: number, latitude: number) => void;
  onChange?: (card: MapCardData | null) => void;
  disabled?: boolean;
  footer?: ReactNode;
}) {
  const container = useRef<HTMLElement>(null);
  const [visible, setVisible] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [globe, setGlobe] = useState(false);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [camera, setCamera] = useState<MapCamera>();
  const placeFocus = useRef<MapPlaceFocus | null>(null);
  const [listOpen, setListOpen] = useState(true);
  const [focusTarget, setFocusTarget] = useState<{ place: MapPlace } | null>(
    null,
  );
  const panelId = useId();
  const reduceMotion = useReducedMotion();
  const panelTransition: Transition = reduceMotion
    ? { duration: 0 }
    : { type: "spring", stiffness: 420, damping: 38 };
  const focusPlace = (place: MapPlace) => {
    setVisible(true);
    setFocusTarget({ place });
  };
  const fail = useCallback(() => setFailed(true), []);
  useDocumentScrollLock(expanded);
  useEffect(() => {
    if (!container.current || deferMap) return;
    if (typeof IntersectionObserver === "undefined") {
      setVisible(true);
      return;
    }
    let release: ReturnType<typeof setTimeout> | undefined;
    const observer = new IntersectionObserver(
      ([entry]) => {
        window.clearTimeout(release);
        if (entry.isIntersecting) setVisible(true);
        else release = window.setTimeout(() => setVisible(false), 3_000);
      },
      { threshold: 0.01 },
    );
    observer.observe(container.current);
    const preloader = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          void import("./MapCanvas").catch(fail);
          preloader.disconnect();
        }
      },
      { rootMargin: "300px 0px" },
    );
    preloader.observe(container.current);
    return () => {
      observer.disconnect();
      preloader.disconnect();
      window.clearTimeout(release);
    };
  }, [deferMap, fail]);
  const placeholder = (
    <div className={styles.placeholder} role="status">
      <Icon name="map-picker" />
      <span>{failed ? "地图暂时无法加载" : "正在准备地图…"}</span>
      {failed && (
        <button
          type="button"
          onClick={() => {
            setFailed(false);
            setAttempt((n) => n + 1);
          }}
        >
          重试
        </button>
      )}
    </div>
  );
  const canvas = (large: boolean) => (
    <div className={large ? styles.largeCanvas : styles.canvas}>
      {!deferMap && (visible || large) && !failed ? (
        <MapFailureBoundary
          key={attempt}
          fallback={
            <div className={styles.placeholder}>
              地图无法显示，请使用下方地点链接。
            </div>
          }
        >
          <Suspense fallback={placeholder}>
            <Canvas
              card={card}
              camera={camera}
              onCamera={(next) => {
                setCamera(next);
                setFocusTarget(null);
              }}
              focusTarget={focusTarget}
              placeFocus={placeFocus}
              fullscreen={large}
              globe={large && globe}
              onFailure={fail}
              onPick={onPick}
            />
          </Suspense>
        </MapFailureBoundary>
      ) : (
        placeholder
      )}
    </div>
  );
  const places = (
    <MapPlaces
      card={card}
      onChange={onChange}
      disabled={disabled}
      onSelect={onPick ? undefined : focusPlace}
    />
  );
  return (
    <section
      ref={container}
      className={styles.card}
      data-map-card=""
      data-interactive="true"
      aria-label={`地图：${card.places.map((p) => p.name).join("、")}`}
      onClick={(event) => event.stopPropagation()}
      onMouseDown={(event) => event.stopPropagation()}
    >
      <div className={styles.canvasFrame}>
        {expanded ? <div className={styles.canvas} /> : canvas(false)}
        <div className={styles.cardControls}>
          <ActionTooltip label="展开地图">
            <button
              className={styles.expand}
              type="button"
              aria-label="展开地图"
              onClick={() => setExpanded(true)}
            >
              <Icon name="expand" />
            </button>
          </ActionTooltip>
          {onChange && (
            <ActionTooltip label="移除地图">
              <button
                className={styles.expand}
                type="button"
                aria-label="移除地图"
                disabled={disabled}
                onClick={() => onChange(null)}
              >
                <Icon name="close" />
              </button>
            </ActionTooltip>
          )}
        </div>
      </div>
      {places}
      {footer}
      <Dialog.Root
        open={expanded}
        onOpenChange={(open, details) => {
          if (
            !open &&
            details.reason === "escape-key" &&
            details.event.target instanceof Element &&
            details.event.target.closest("[data-map-place][data-dragging]")
          ) {
            details.cancel();
            details.allowPropagation();
            return;
          }
          setExpanded(open);
        }}
      >
        <Dialog.Portal>
          <Dialog.Backdrop className={styles.backdrop} />
          <Dialog.Popup
            className={`${styles.expanded} ${onPick ? styles.pickerExpanded : ""}`}
            data-map-fullscreen=""
            data-map-editable={onChange ? "" : undefined}
            onKeyDown={(event) => {
              if (
                (event.key === "ArrowUp" || event.key === "ArrowDown") &&
                event.target instanceof Element &&
                event.target.closest("[data-map-place][data-dragging]")
              )
                event.preventBaseUIHandler();
            }}
          >
            <Dialog.Title className={styles.hiddenTitle}>地图</Dialog.Title>
            {expanded && canvas(true)}
            <div className={styles.fullscreenControls}>
              <ActionTooltip label="关闭地图">
                <Dialog.Close
                  className={styles.floatingControl}
                  aria-label="关闭地图"
                >
                  <Icon name="close" />
                </Dialog.Close>
              </ActionTooltip>
              <ActionTooltip label={globe ? "切换平面" : "切换地球"}>
                <button
                  className={styles.floatingControl}
                  type="button"
                  aria-label={globe ? "切换平面" : "切换地球"}
                  aria-pressed={globe}
                  onClick={() => {
                    setFocusTarget(null);
                    setGlobe((value) => !value);
                  }}
                >
                  <Icon name="globe" />
                </button>
              </ActionTooltip>
            </div>
            <motion.aside
              className={styles.placePanel}
              aria-label="地图地点面板"
              initial={false}
              animate={{
                width: listOpen ? 300 : 32,
                borderRadius: listOpen ? 20 : 16,
                x: listOpen ? 0 : -17,
                y: listOpen ? 0 : 8,
              }}
              transition={panelTransition}
            >
              <motion.header
                className={styles.panelHeader}
                initial={false}
                animate={{ height: listOpen ? 48 : 32 }}
                transition={panelTransition}
              >
                <motion.span
                  className={styles.panelLabel}
                  aria-hidden={!listOpen}
                  animate={{ opacity: listOpen ? 1 : 0 }}
                  transition={{ duration: reduceMotion ? 0 : 0.12 }}
                >
                  地点 · {card.places.length}
                </motion.span>
                <motion.button
                  className={styles.panelToggle}
                  initial={false}
                  animate={{ x: listOpen ? -17 : 0, y: listOpen ? 8 : 0 }}
                  transition={panelTransition}
                  type="button"
                  aria-label={listOpen ? "收起地点列表" : "展开地点列表"}
                  aria-expanded={listOpen}
                  aria-controls={panelId}
                  onClick={() => setListOpen((open) => !open)}
                >
                  <motion.span
                    className={styles.panelChevron}
                    animate={{ rotate: listOpen ? 0 : 90 }}
                    transition={{ duration: reduceMotion ? 0 : 0.2 }}
                  >
                    <Icon name="chevron-down" />
                  </motion.span>
                </motion.button>
              </motion.header>
              <motion.div
                id={panelId}
                aria-hidden={!listOpen}
                inert={!listOpen}
                initial={false}
                animate={{
                  height: listOpen ? "auto" : 0,
                  opacity: listOpen ? 1 : 0,
                }}
                transition={{ duration: reduceMotion ? 0 : 0.2 }}
                className={styles.panelBody}
              >
                <div className={styles.panelContent}>
                  {places}
                  {footer}
                </div>
              </motion.div>
            </motion.aside>
          </Dialog.Popup>
        </Dialog.Portal>
      </Dialog.Root>
    </section>
  );
}
