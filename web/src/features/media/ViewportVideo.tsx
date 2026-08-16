import { useEffect, useRef, useState } from "react";

const RELEASE_DELAY_MS = 3_000;

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
    <video
      aria-label={label}
      data-resource-state={nearViewport ? "attached" : "idle"}
      muted
      onError={onError}
      playsInline
      poster={nearViewport ? poster : undefined}
      preload={nearViewport ? "metadata" : "none"}
      ref={videoRef}
      src={nearViewport ? src : undefined}
    />
  );
}
