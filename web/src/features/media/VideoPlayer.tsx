import { useTranslation } from "react-i18next";
import {
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type Ref,
} from "react";
import { useMergedRef } from "@mantine/hooks";
import { VideoSettings, type VideoQuality } from "./VideoSettings";
import { ActionTooltip } from "../../components/TimelineInteractions";
import { Icon } from "../../components/icons";
import { notify } from "../../components/Feedback";
import { VideoControlIcon } from "./VideoControlIcon";
import { pauseVideos, type VideoPlaybackSnapshot } from "./videoPlayback";
import styles from "./VideoPlayer.module.css";

function clock(value: number) {
  const seconds = Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0;
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

export function VideoPlayer({
  src,
  originalSrc,
  poster,
  label,
  attached = true,
  autoplay = false,
  style,
  videoRef,
  onError,
  onOpen,
  openOnSurface = false,
  initialPlayback,
  onPlayback,
  onMetadata,
}: {
  src?: string;
  originalSrc?: string | null;
  poster?: string;
  label: string;
  attached?: boolean;
  autoplay?: boolean;
  style?: CSSProperties;
  videoRef?: Ref<HTMLVideoElement>;
  onError: () => void;
  onOpen?: (trigger: HTMLElement) => void;
  openOnSurface?: boolean;
  initialPlayback?: VideoPlaybackSnapshot;
  onPlayback?: (state: VideoPlaybackSnapshot) => void;
  onMetadata?: (video: HTMLVideoElement) => void;
}) {
  const { t } = useTranslation();

  const node = useRef<HTMLVideoElement>(null);
  const mergedRef = useMergedRef(node, videoRef);
  const root = useRef<HTMLDivElement>(null);
  const callbacks = useRef({ onPlayback, onMetadata });
  callbacks.current = { onPlayback, onMetadata };
  const initial = useRef(initialPlayback);
  const restorePending = useRef(Boolean(initialPlayback));
  const [playing, setPlaying] = useState(false);
  const [waiting, setWaiting] = useState(attached);
  const [showPoster, setShowPoster] = useState(true);
  const [duration, setDuration] = useState(0);
  const [time, setTime] = useState(0);
  const [muted, setMuted] = useState(initialPlayback?.muted ?? false);
  const [volume, setVolume] = useState(initialPlayback?.volume ?? 1);
  const audibleVolume = useRef(initialPlayback?.volume || 1);
  const [rate, setRate] = useState(initialPlayback?.rate ?? 1);
  const [visible, setVisible] = useState(true);
  const [fullscreen, setFullscreen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [quality, setQuality] = useState<VideoQuality>(
    initialPlayback?.quality ?? "preview",
  );
  const qualityRef = useRef(quality);
  qualityRef.current = quality;
  const [resolution, setResolution] = useState(0);
  const resumeAfterLoad = useRef(autoplay);
  const hideTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const controlsHovered = useRef(false);
  const keyboardFocusWithin = () => {
    const focused = document.activeElement;
    return (
      focused instanceof Element &&
      root.current?.contains(focused) &&
      focused.matches(":focus-visible")
    );
  };
  const snapshot = (video: HTMLVideoElement) => {
    const pending = restorePending.current ? initial.current : undefined;
    callbacks.current.onPlayback?.({
      time: pending?.time ?? video.currentTime,
      muted: pending?.muted ?? video.muted,
      volume: pending?.volume ?? video.volume,
      rate: pending?.rate ?? video.playbackRate,
      quality: qualityRef.current,
    });
  };
  const reveal = () => {
    setVisible(true);
    if (hideTimer.current) clearTimeout(hideTimer.current);
    hideTimer.current = setTimeout(() => {
      if (!controlsHovered.current && !keyboardFocusWithin()) setVisible(false);
    }, 2500);
  };
  useEffect(() => {
    const video = node.current;
    const changed = () =>
      setFullscreen(document.fullscreenElement === root.current);
    document.addEventListener("fullscreenchange", changed);
    return () => {
      document.removeEventListener("fullscreenchange", changed);
      if (hideTimer.current) clearTimeout(hideTimer.current);
      if (video) {
        snapshot(video);
        if (!video.paused) video.pause();
        if (document.pictureInPictureElement === video)
          void document.exitPictureInPicture().catch(() => {});
      }
    };
    // Each mounted player owns one media source and its final snapshot.
  }, []);
  useEffect(() => {
    if (attached) {
      setWaiting(true);
      return;
    }
    if (!attached) {
      node.current?.pause();
      setPlaying(false);
      setWaiting(false);
      setDuration(0);
      setTime(0);
      setShowPoster(true);
      initial.current = undefined;
    }
  }, [attached]);
  const play = () => {
    const video = node.current;
    if (!video) return;
    pauseVideos(video);
    setWaiting(true);
    void video.play().catch(() => {
      setPlaying(false);
      setWaiting(false);
    });
  };
  const toggle = () => {
    if (node.current?.paused) play();
    else node.current?.pause();
    reveal();
  };
  const mute = () => {
    const video = node.current;
    if (!video) return;
    if (video.muted || video.volume === 0) {
      if (video.volume === 0) video.volume = audibleVolume.current;
      video.muted = false;
    } else video.muted = true;
  };
  const enterFullscreen = () => {
    if (document.fullscreenElement === root.current) {
      void document.exitFullscreen().catch(() => {});
    } else if (root.current?.requestFullscreen) {
      void root.current
        .requestFullscreen()
        .catch(() => notify(t("暂时无法进入全屏"), { type: "error" }));
    } else {
      const video = node.current as
        (HTMLVideoElement & { webkitEnterFullscreen?: () => void }) | null;
      video?.webkitEnterFullscreen?.();
    }
  };
  const controlsShown = visible || !playing || settingsOpen;
  const tooltipContainer = fullscreen ? root.current : undefined;
  const control = (
    title: string,
    icon: Parameters<typeof VideoControlIcon>[0]["name"],
    action: () => void,
  ) => (
    <ActionTooltip
      label={title}
      placement={{ side: "top", sideOffset: 4 }}
      container={tooltipContainer}
    >
      <button
        type="button"
        aria-label={title}
        data-video-action={icon}
        onClick={action}
        className={styles.control}
      >
        <VideoControlIcon name={icon} />
      </button>
    </ActionTooltip>
  );
  return (
    <div
      ref={root}
      className={styles.player}
      style={style}
      role="group"
      aria-label={t("视频播放器")}
      tabIndex={0}
      data-video-player=""
      data-compact={openOnSurface || undefined}
      data-playback-state={
        !attached
          ? "idle"
          : waiting
            ? "loading"
            : playing
              ? "playing"
              : "paused"
      }
      data-resource-state={attached ? "attached" : "idle"}
      data-controls-visible={controlsShown || undefined}
      onPointerMove={reveal}
      onFocus={reveal}
      onPointerLeave={() => {
        if (!controlsHovered.current && !keyboardFocusWithin())
          setVisible(false);
      }}
      onClick={(event) => {
        event.stopPropagation();
        if (
          (event.target as Element).closest(
            "button,input,[data-video-controls]",
          )
        )
          return;
        if (onOpen && openOnSurface && !fullscreen) onOpen(root.current!);
        else if (node.current?.muted && playing) mute();
        else toggle();
      }}
      onKeyDown={(event) => {
        if (
          event.defaultPrevented ||
          event.nativeEvent.isComposing ||
          event.ctrlKey ||
          event.metaKey ||
          event.altKey
        )
          return;
        if ((event.target as Element).closest("[data-video-settings]")) return;
        if (
          event.key === "Escape" &&
          document.fullscreenElement === root.current
        ) {
          event.preventDefault();
          event.stopPropagation();
          void document.exitFullscreen();
          return;
        }
        if ((event.target as Element).closest("input,button,[role=menu]"))
          return;
        const video = node.current;
        if (!video) return;
        if (event.key === "Enter" && onOpen && !fullscreen)
          onOpen(root.current!);
        else if (event.key === " " || event.key.toLowerCase() === "k") toggle();
        else if (event.key === "ArrowLeft" || event.key === "ArrowRight")
          video.currentTime = Math.max(
            0,
            Math.min(
              duration,
              video.currentTime + (event.key === "ArrowLeft" ? -5 : 5),
            ),
          );
        else if (event.key.toLowerCase() === "m") mute();
        else if (event.key.toLowerCase() === "f") enterFullscreen();
        else return;
        event.preventDefault();
        event.stopPropagation();
        reveal();
      }}
    >
      <video
        ref={mergedRef}
        aria-label={label}
        src={
          attached
            ? quality === "original" && originalSrc
              ? originalSrc
              : src
            : undefined
        }
        poster={attached ? poster : undefined}
        playsInline
        preload={attached ? "metadata" : "none"}
        autoPlay={autoplay && resumeAfterLoad.current}
        muted={muted}
        loop={duration > 0 && duration <= 60}
        onLoadedMetadata={(event) => {
          const video = event.currentTarget;
          setResolution(Math.min(video.videoWidth, video.videoHeight));
          setDuration(Number.isFinite(video.duration) ? video.duration : 0);
          if (initial.current) {
            video.currentTime = Math.min(
              initial.current.time,
              Math.max(0, video.duration - 0.05),
            );
            video.muted = initial.current.muted;
            video.volume = initial.current.volume;
            video.playbackRate = initial.current.rate;
          }
          restorePending.current = false;
          callbacks.current.onMetadata?.(video);
          setWaiting(false);
          if (resumeAfterLoad.current) play();
        }}
        onDurationChange={(event) =>
          setDuration(
            Number.isFinite(event.currentTarget.duration)
              ? event.currentTarget.duration
              : 0,
          )
        }
        onCanPlay={() => setWaiting(false)}
        onPlay={(event) => pauseVideos(event.currentTarget)}
        onPlaying={() => {
          setPlaying(true);
          setWaiting(false);
          setShowPoster(false);
          reveal();
        }}
        onWaiting={() => setWaiting(true)}
        onPause={(event) => {
          setPlaying(false);
          setWaiting(false);
          snapshot(event.currentTarget);
        }}
        onTimeUpdate={(event) => {
          setTime(event.currentTarget.currentTime);
          snapshot(event.currentTarget);
        }}
        onVolumeChange={(event) => {
          setMuted(event.currentTarget.muted);
          setVolume(event.currentTarget.volume);
          if (event.currentTarget.volume > 0)
            audibleVolume.current = event.currentTarget.volume;
          snapshot(event.currentTarget);
        }}
        onRateChange={(event) => {
          setRate(event.currentTarget.playbackRate);
          snapshot(event.currentTarget);
        }}
        onEnded={() => {
          setPlaying(false);
          setShowPoster(true);
        }}
        onError={() => {
          setPlaying(false);
          setWaiting(false);
          if (qualityRef.current === "original") {
            setWaiting(true);
            restorePending.current = true;
            setQuality("preview");
            notify(t("原画质暂时无法播放，已切回自动"), { type: "error" });
          } else onError();
        }}
      />
      {attached && showPoster && poster ? (
        <img
          className={styles.poster}
          src={poster}
          alt=""
          aria-hidden="true"
          decoding="async"
        />
      ) : null}
      {!attached || waiting ? (
        <span
          className={styles.loading}
          role="status"
          aria-label={t("视频正在加载")}
        >
          <span />
        </span>
      ) : null}
      {attached && !waiting && !playing ? (
        <button
          type="button"
          className={styles.centerPlay}
          aria-label={t("播放视频")}
          onClick={(event) => {
            event.stopPropagation();
            if (onOpen && openOnSurface && !fullscreen) onOpen(root.current!);
            else play();
          }}
        >
          <Icon name="video-play" />
        </button>
      ) : null}
      {onOpen && !openOnSurface && !fullscreen ? (
        <ActionTooltip label={t("展开视频")} placement={{ side: "bottom" }}>
          <button
            className={styles.expand}
            type="button"
            aria-label={t("展开视频")}
            onClick={(event) => {
              event.stopPropagation();
              onOpen(event.currentTarget);
            }}
          >
            <Icon name="expand" />
          </button>
        </ActionTooltip>
      ) : null}
      {attached && duration > 0 ? (
        <div
          className={styles.controls}
          data-video-controls=""
          onPointerEnter={() => {
            controlsHovered.current = true;
          }}
          onPointerLeave={() => {
            controlsHovered.current = false;
            reveal();
          }}
          aria-hidden={!controlsShown}
          inert={!controlsShown}
          onClick={(event) => event.stopPropagation()}
        >
          <input
            className={styles.seek}
            aria-label={t("播放进度")}
            type="range"
            min={0}
            max={duration}
            step="0.01"
            value={Math.min(time, duration)}
            style={
              { "--progress": `${(time / duration) * 100}%` } as CSSProperties
            }
            onChange={(event) => {
              if (node.current) {
                node.current.currentTime = Number(event.target.value);
                setTime(node.current.currentTime);
                snapshot(node.current);
              }
            }}
          />
          <div className={styles.row}>
            {control(
              playing ? t("暂停视频") : t("继续播放"),
              playing ? "pause" : "play",
              toggle,
            )}
            <span className={styles.time}>
              {clock(time)} / {clock(Math.round(duration))}
            </span>
            <div className={styles.volume}>
              {control(
                muted || volume === 0 ? t("取消静音") : t("静音"),
                muted || volume === 0 ? "muted" : "volume",
                mute,
              )}
              <div
                className={styles.volumePopup}
                style={
                  {
                    "--volume": `${(muted ? 0 : volume) * 100}%`,
                  } as CSSProperties
                }
              >
                <input
                  aria-label={t("音量")}
                  type="range"
                  min={0}
                  max={1}
                  step=".05"
                  value={muted ? 0 : volume}
                  onChange={(event) => {
                    if (node.current) {
                      node.current.volume = Number(event.target.value);
                      node.current.muted = false;
                    }
                  }}
                />
              </div>
            </div>
            <VideoSettings
              rate={rate}
              quality={quality}
              resolution={resolution}
              hasOriginal={Boolean(originalSrc)}
              container={tooltipContainer}
              onOpenChange={setSettingsOpen}
              onRateChange={(value) => {
                if (node.current) node.current.playbackRate = value;
              }}
              onQualityChange={(value) => {
                const video = node.current;
                if (!video || value === quality) return;
                initial.current = {
                  time: video.currentTime,
                  muted: video.muted,
                  volume: video.volume,
                  rate: video.playbackRate,
                  quality: value,
                };
                restorePending.current = true;
                resumeAfterLoad.current = !video.paused;
                setWaiting(true);
                setQuality(value);
              }}
            />
            {typeof document !== "undefined" && document.pictureInPictureEnabled
              ? control(t("画中画"), "pip", () => {
                  const video = node.current;
                  if (!video) return;
                  void (
                    document.pictureInPictureElement === video
                      ? document.exitPictureInPicture()
                      : video.requestPictureInPicture()
                  ).catch(() =>
                    notify(t("暂时无法进入画中画"), { type: "error" }),
                  );
                })
              : null}
            {control(
              fullscreen ? t("退出全屏") : t("全屏"),
              fullscreen ? "shrink" : "fullscreen",
              enterFullscreen,
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}
