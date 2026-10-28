export interface VideoPlaybackSnapshot {
  time: number;
  muted: boolean;
  volume: number;
  rate: number;
  quality?: "preview" | "original";
}

export function pauseVideos(except?: HTMLVideoElement) {
  document.querySelectorAll("video").forEach((video) => {
    if (video !== except && !video.paused) video.pause();
  });
}
