import { ApiClient } from "./client";
import { uploadMediaXHR } from "./public";

export interface UploadedMedia {
  id: string;
  byteSize: number;
  width: number;
  height: number;
  mimeType: string;
  previewMimeType: string;
  animatedOriginal: boolean;
  previewUrl: string;
  originalUrl: string;
}

export class MediaApi extends ApiClient {
  async uploadMedia(
    csrfToken: string,
    file: File,
    onProgress: (percent: number) => void = () => undefined,
    signal?: AbortSignal,
    onProcessingPoster?: (objectUrl: string) => void,
  ): Promise<UploadedMedia> {
    return uploadMediaXHR(
      "/api/v1/media",
      file,
      onProgress,
      signal,
      {
        "X-CSRF-Token": csrfToken,
      },
      onProcessingPoster,
    );
  }
}

export const mediaApi = new MediaApi();
