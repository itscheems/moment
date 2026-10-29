import { t } from "../../../../i18n";
export const ARTICLE_COVER_ASPECT = 5 / 2;
const MAX_COVER_WIDTH = 1600;

export interface CoverCropArea {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Keep rounded cropper coordinates inside the decoded image without enlarging it. */
export function coverCropGeometry(
  area: CoverCropArea,
  imageWidth: number,
  imageHeight: number,
) {
  if (
    ![area.x, area.y, area.width, area.height, imageWidth, imageHeight].every(
      Number.isFinite,
    ) ||
    area.width <= 0 ||
    area.height <= 0 ||
    imageWidth <= 0 ||
    imageHeight <= 0
  ) {
    throw new Error(t("没有可用的裁切区域，请重新选择图片。"));
  }
  const left = Math.max(0, area.x);
  const top = Math.max(0, area.y);
  const right = Math.min(imageWidth, area.x + area.width);
  const bottom = Math.min(imageHeight, area.y + area.height);
  const width = Math.min(right - left, (bottom - top) * ARTICLE_COVER_ASPECT);
  const height = width / ARTICLE_COVER_ASPECT;
  // Multiples of 5 × 2 produce an exact ratio even for small or fractional crops.
  const unit = Math.floor(Math.min(width / 5, MAX_COVER_WIDTH / 5) + 1e-9);
  if (unit < 1) throw new Error(t("图片太小，无法生成封面，请换一张图片。"));
  return {
    source: {
      x: left + (right - left - width) / 2,
      y: top + (bottom - top - height) / 2,
      width,
      height,
    },
    width: unit * 5,
    height: unit * 2,
  };
}

function cancelled() {
  return new DOMException("Cover cropping was cancelled", "AbortError");
}

async function loadImage(url: string, signal?: AbortSignal) {
  const image = new Image();
  await new Promise<void>((resolve, reject) => {
    const clean = () => {
      image.onload = null;
      image.onerror = null;
      signal?.removeEventListener("abort", abort);
    };
    const abort = () => {
      clean();
      image.removeAttribute("src");
      reject(cancelled());
    };
    image.onload = () => {
      clean();
      resolve();
    };
    image.onerror = () => {
      clean();
      reject(new Error(t("无法读取这张图片，请选择 JPEG、PNG 或 WebP 图片。")));
    };
    if (signal?.aborted) {
      abort();
      return;
    }
    signal?.addEventListener("abort", abort, { once: true });
    // The browser applies image orientation, just as it does in the cropper preview.
    image.src = url;
  });
  return image;
}

/** Export only the selected pixels. Cancellation and failure never upload anything. */
export async function createCoverCrop(
  file: File,
  area: CoverCropArea,
  signal?: AbortSignal,
): Promise<File> {
  if (signal?.aborted) throw cancelled();
  const url = URL.createObjectURL(file);
  try {
    const image = await loadImage(url, signal);
    if (signal?.aborted) throw cancelled();
    const geometry = coverCropGeometry(
      area,
      image.naturalWidth,
      image.naturalHeight,
    );
    const canvas = document.createElement("canvas");
    canvas.width = geometry.width;
    canvas.height = geometry.height;
    const context = canvas.getContext("2d");
    if (!context) throw new Error(t("无法处理这张图片，请重试。"));
    const source = geometry.source;
    context.drawImage(
      image,
      source.x,
      source.y,
      source.width,
      source.height,
      0,
      0,
      geometry.width,
      geometry.height,
    );
    let mimeType = "image/jpeg";
    if (file.type !== "image/jpeg") {
      // PNG output keeps transparency; opaque WebP and other browser images
      // can use JPEG without adding an artificial background to transparent pixels.
      const pixels = context.getImageData(
        0,
        0,
        canvas.width,
        canvas.height,
      ).data;
      for (let alpha = 3; alpha < pixels.length; alpha += 4) {
        if (pixels[alpha] !== 255) {
          mimeType = "image/png";
          break;
        }
      }
    }
    const blob = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(
        (result) =>
          result
            ? resolve(result)
            : reject(new Error(t("封面生成没有成功，请重试。"))),
        mimeType,
        0.92,
      );
    });
    if (signal?.aborted) throw cancelled();
    const basename = file.name.replace(/\.[^.]+$/, "") || "cover";
    return new File(
      [blob],
      `${basename}-cover.${mimeType === "image/png" ? "png" : "jpg"}`,
      {
        type: mimeType,
        lastModified: Date.now(),
      },
    );
  } finally {
    URL.revokeObjectURL(url);
  }
}
