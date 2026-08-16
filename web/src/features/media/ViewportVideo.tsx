import { useEffect, useRef, useState } from "react";
import { motion, useReducedMotion } from "motion/react";

import { Icon } from "../../components/icons";
import styles from "./Media.module.css";

const RELEASE_DELAY_MS = 3_000;

type PlaybackState = "idle" | "loading" | "paused" | "playing";

function formatDuration(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "";
  const seconds = Math.round(value);
  const minutes = Math.floor(seconds / 60);
  return `${minutes}:${String(seconds % 60).padStart(2, "0")}`;
}

export function ViewportVideo({
  src,
  poster,
  label,
  eager = false,
  onError,
}: {
  src: string;
  poster: string;
  label: string;
  eager?: boolean;
  onError: () => void;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const releaseTimerRef = useRef<number | null>(null);
  const reduceMotion = useReducedMotion();
  const supportsObserver = typeof IntersectionObserver !== "undefined";
  const [nearViewport, setNearViewport] = useState(eager || !supportsObserver);
  const [playback, setPlayback] = useState<PlaybackState>(
    eager || !supportsObserver ? "loading" : "idle",
  );
  const [duration, setDuration] = useState(0);

  useEffect(() => {
    if (eager || !supportsObserver) {
      setNearViewport(true);
      return;
    }
    const node = videoRef.current;
    if (!node) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          if (releaseTimerRef.current !== null) {
            window.clearTimeout(releaseTimerRef.current);
            releaseTimerRef.current = null;
          }
          setNearViewport(true);
          setPlayback((current) => (current === "idle" ? "loading" : current));
          return;
        }
        node.pause();
        if (releaseTimerRef.current !== null) {
          window.clearTimeout(releaseTimerRef.current);
        }
        releaseTimerRef.current = window.setTimeout(() => {
          releaseTimerRef.current = null;
          setNearViewport(false);
          setPlayback("idle");
          setDuration(0);
        }, RELEASE_DELAY_MS);
      },
      { rootMargin: "600px 0px", threshold: 0.01 },
    );
    observer.observe(node);
    return () => {
      observer.disconnect();
      if (releaseTimerRef.current !== null) {
        window.clearTimeout(releaseTimerRef.current);
      }
    };
  }, [eager, supportsObserver]);

  const togglePlayback = () => {
    const video = videoRef.current;
    if (!video) return;
    if (!video.paused) {
      video.pause();
      return;
    }
    setPlayback("loading");
    void video.play().catch(() => setPlayback("paused"));
  };

  return (
    <div
      className={styles.videoPlayer}
      data-playback-state={playback}
      data-resource-state={nearViewport ? "attached" : "idle"}
    >
      <video
        aria-label={label}
        controls={playback === "playing"}
        onCanPlay={() =>
          setPlayback((current) => (current === "playing" ? current : "paused"))
        }
        onDurationChange={(event) => setDuration(event.currentTarget.duration)}
        onEnded={() => setPlayback("paused")}
        onError={() => {
          setPlayback("paused");
          onError();
        }}
        onLoadedMetadata={(event) => {
          setDuration(event.currentTarget.duration);
          setPlayback("paused");
        }}
        onPause={() => setPlayback("paused")}
        onPlaying={() => setPlayback("playing")}
        onWaiting={() => setPlayback("loading")}
        playsInline
        poster={nearViewport ? poster : undefined}
        preload={nearViewport ? "metadata" : "none"}
        ref={videoRef}
        src={nearViewport ? src : undefined}
      />
      {playback === "loading" || playback === "idle" ? (
        <span
          aria-label="视频正在加载"
          className={styles.videoLoadingIndicator}
          role="status"
        >
          <motion.span
            aria-hidden="true"
            className={styles.videoLoadingRing}
            animate={reduceMotion ? undefined : { rotate: 360 }}
            transition={
              reduceMotion
                ? undefined
                : { duration: 0.85, ease: "linear", repeat: Infinity }
            }
          />
        </span>
      ) : null}
      {playback === "paused" ? (
        <button
          aria-label="播放视频"
          className={styles.videoPlayIndicator}
          onClick={togglePlayback}
          type="button"
        >
          <Icon name="video-play" />
        </button>
      ) : null}
      {duration > 0 && playback !== "playing" ? (
        <span aria-hidden="true" className={styles.videoDuration}>
          {formatDuration(duration)}
        </span>
      ) : null}
    </div>
  );
}
