import { useEffect, useRef, useState } from "react";
import { VideoPlayer } from "./VideoPlayer";

import styles from "./Media.module.css";

const RELEASE_DELAY_MS = 3_000;

export function ViewportVideo({
  src,
  originalSrc,
  poster,
  label,
  eager = false,
  onError,
  onOpen,
  openOnSurface,
}: {
  src: string;
  originalSrc?: string;
  poster: string;
  label: string;
  eager?: boolean;
  onError: () => void;
  onOpen?: (trigger: HTMLElement) => void;
  openOnSurface?: boolean;
}) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const releaseTimerRef = useRef<number | null>(null);
  const supportsObserver = typeof IntersectionObserver !== "undefined";
  const [nearViewport, setNearViewport] = useState(eager || !supportsObserver);

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
          return;
        }
        node.pause();
        if (releaseTimerRef.current !== null) {
          window.clearTimeout(releaseTimerRef.current);
        }
        releaseTimerRef.current = window.setTimeout(() => {
          releaseTimerRef.current = null;
          setNearViewport(false);
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

  return (
    <div className={styles.videoPlayer}>
      <VideoPlayer
        key={src}
        videoRef={videoRef}
        src={src}
        originalSrc={originalSrc}
        poster={poster}
        label={label}
        attached={nearViewport}
        onError={onError}
        onOpen={onOpen}
        openOnSurface={openOnSurface}
      />
    </div>
  );
}
