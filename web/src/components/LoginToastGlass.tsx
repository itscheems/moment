import {
  type CSSProperties,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import styles from "./Feedback.module.css";

interface LensMap {
  image: string;
  width: number;
  height: number;
}

// A rounded lens bends the backdrop at its bevel, leaving the flat centre
// neutral. R/G encode the surface normal; this is not a noise/blur texture.
// Technique: https://kube.io/blog/liquid-glass-css-svg/
function createLensMap(width: number, height: number): LensMap | undefined {
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext("2d");
  if (!context) return;
  const image = context.createImageData(width, height);
  const radius = Math.min(28, height / 2, width / 2);
  const bevel = Math.min(14, radius);
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const dx = x + 0.5 - width / 2;
      const dy = y + 0.5 - height / 2;
      const qx = Math.abs(dx) - (width / 2 - radius);
      const qy = Math.abs(dy) - (height / 2 - radius);
      const cornerX = Math.max(qx, 0);
      const cornerY = Math.max(qy, 0);
      const length = Math.hypot(cornerX, cornerY);
      const distance = length + Math.min(Math.max(qx, qy), 0) - radius;
      const normalX =
        length > 0
          ? (cornerX / length) * Math.sign(dx)
          : qx > qy
            ? Math.sign(dx)
            : 0;
      const normalY =
        length > 0
          ? (cornerY / length) * Math.sign(dy)
          : qy >= qx
            ? Math.sign(dy)
            : 0;
      const depth = Math.max(0, -distance);
      // Convex, smoothly flattening bevel. Negative displacement samples
      // inward along the normal instead of pulling transparent outer pixels.
      const bend =
        distance <= 0 && depth < bevel ? (1 - depth / bevel) ** 2 : 0;
      const offset = (y * width + x) * 4;
      image.data[offset] = Math.round(128 - 127 * normalX * bend);
      image.data[offset + 1] = Math.round(128 - 127 * normalY * bend);
      image.data[offset + 2] = 128;
      image.data[offset + 3] = 255;
    }
  }
  context.putImageData(image, 0, 0);
  return { image: canvas.toDataURL(), width, height };
}

export function LoginToastGlass() {
  const id = `login-lens-${useId().replaceAll(":", "")}`;
  const surface = useRef<HTMLSpanElement>(null);
  const [map, setMap] = useState<LensMap>();
  useLayoutEffect(() => {
    const element = surface.current;
    // SVG backdrop filters are a Chromium capability. Keep the CSS material
    // fallback in other engines rather than cloning the page behind the toast.
    if (
      !element ||
      typeof ResizeObserver === "undefined" ||
      !/Chrome\/|Chromium\/|Edg\//.test(navigator.userAgent)
    )
      return;
    let lastSize = "";
    const observer = new ResizeObserver(([entry]) => {
      const width = Math.ceil(entry.contentRect.width);
      const height = Math.ceil(entry.contentRect.height);
      const size = `${width}:${height}`;
      if (!width || !height || size === lastSize) return;
      lastSize = size;
      setMap(createLensMap(width, height));
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return (
    <>
      {map ? (
        <svg aria-hidden="true" className={styles.lensDefinitions}>
          <defs>
            <filter
              id={id}
              x="0"
              y="0"
              width={map.width}
              height={map.height}
              filterUnits="userSpaceOnUse"
              colorInterpolationFilters="sRGB"
            >
              <feImage
                href={map.image}
                width={map.width}
                height={map.height}
                result="lens"
              />
              <feDisplacementMap
                in="SourceGraphic"
                in2="lens"
                scale="28"
                xChannelSelector="R"
                yChannelSelector="G"
              />
            </filter>
          </defs>
        </svg>
      ) : null}
      <span
        aria-hidden="true"
        className={styles.loginGlass}
        ref={surface}
        style={
          map
            ? ({
                "--login-glass-filter": `url("#${id}") saturate(135%)`,
              } as CSSProperties)
            : undefined
        }
      />
    </>
  );
}
