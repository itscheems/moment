export interface ViewerMediaSize {
  width: number;
  height: number;
}

/** Small landscapes and short portraits use a 600px viewing region. */
export function viewerMediaSize(
  width: number,
  height: number,
  containerWidth: number,
  containerHeight: number,
): ViewerMediaSize | null {
  if (
    ![width, height, containerWidth, containerHeight].every(
      (value) => Number.isFinite(value) && value > 0,
    )
  )
    return null;
  const landscape = width > height;
  const availableWidth =
    landscape && width <= 600 ? Math.min(600, containerWidth) : containerWidth;
  const availableHeight =
    landscape || height > 600
      ? containerHeight
      : Math.min(600, containerHeight);
  const scale = Math.min(availableWidth / width, availableHeight / height);
  return { width: width * scale, height: height * scale };
}
