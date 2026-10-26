import { createContext, useContext } from "react";
import type { MediaLightboxItem } from "./MediaLightbox";

export interface MediaSource {
  postId: string;
  commentId?: string;
}

export const MediaViewerContext = createContext<{
  open: (
    source: MediaSource,
    items: MediaLightboxItem[],
    selected: number,
    trigger: HTMLElement,
  ) => void;
} | null>(null);

export function useMediaViewer() {
  return useContext(MediaViewerContext);
}

export function mediaSourceKey(source: MediaSource): string {
  return source.postId + ":" + (source.commentId ?? "post");
}
