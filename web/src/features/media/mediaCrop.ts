export type MediaCropAspect = "original" | "wide" | "square";
export interface CropArea {
  x: number;
  y: number;
  width: number;
  height: number;
}
export interface MediaCropSelection {
  aspect: MediaCropAspect;
  area: CropArea;
  percentages: CropArea;
}
export interface MediaCropSource {
  url: string;
  name: string;
  mimeType: string;
}

export function canCropMedia(item: {
  mimeType: string;
  animatedOriginal: boolean;
}) {
  return (
    !item.animatedOriginal &&
    ["image/jpeg", "image/png", "image/webp"].includes(item.mimeType)
  );
}

export function cropGeometry(area: CropArea, width: number, height: number) {
  if (
    ![area.x, area.y, area.width, area.height, width, height].every(
      Number.isFinite,
    ) ||
    area.width <= 0 ||
    area.height <= 0 ||
    width <= 0 ||
    height <= 0
  )
    throw new Error("There is no usable crop area.");
  const x = Math.max(0, area.x),
    y = Math.max(0, area.y);
  const w = Math.min(width, area.x + area.width) - x,
    h = Math.min(height, area.y + area.height) - y;
  if (w < 1 || h < 1) throw new Error("There is no usable crop area.");
  return {
    x,
    y,
    width: w,
    height: h,
    outputWidth: Math.max(1, Math.round(w)),
    outputHeight: Math.max(1, Math.round(h)),
  };
}

export async function createMediaCrop(
  source: MediaCropSource,
  area: CropArea,
  signal?: AbortSignal,
): Promise<File> {
  const cancelled = () =>
    new DOMException("Image cropping was cancelled", "AbortError");
  if (signal?.aborted) throw cancelled();
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
      reject(new Error("Unable to read this image. Please try another image."));
    };
    signal?.addEventListener("abort", abort, { once: true });
    image.src = source.url;
  });
  if (signal?.aborted) throw cancelled();
  const g = cropGeometry(area, image.naturalWidth, image.naturalHeight);
  const canvas = document.createElement("canvas");
  canvas.width = g.outputWidth;
  canvas.height = g.outputHeight;
  try {
    const context = canvas.getContext("2d");
    if (!context)
      throw new Error("Image cropping is unavailable in this browser.");
    context.drawImage(
      image,
      g.x,
      g.y,
      g.width,
      g.height,
      0,
      0,
      g.outputWidth,
      g.outputHeight,
    );
    const type =
      source.mimeType === "image/png" || source.mimeType === "image/webp"
        ? source.mimeType
        : "image/jpeg";
    const blob = await new Promise<Blob>((resolve, reject) =>
      canvas.toBlob(
        (b) =>
          b
            ? resolve(b)
            : reject(new Error("Unable to save the cropped image.")),
        type,
        0.92,
      ),
    );
    if (signal?.aborted) throw cancelled();
    const extension =
      blob.type === "image/png"
        ? "png"
        : blob.type === "image/webp"
          ? "webp"
          : "jpg";
    return new File(
      [blob],
      `${source.name.replace(/\.[^.]+$/, "")}-cropped.${extension}`,
      { type: blob.type },
    );
  } finally {
    canvas.width = 0;
    canvas.height = 0;
  }
}
