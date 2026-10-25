import defaultAvatarUrl from "../assets/default-avatar.png";

const images = new Map<string, string>();

/** Rasterize once per initial so every avatar size displays the same PNG. */
export function initialAvatarUrl(displayName: string): string {
  const initial = (
    Array.from(displayName.trim() || "Moment")[0] ?? "M"
  ).toLocaleUpperCase();
  const cached = images.get(initial);
  if (cached) return cached;
  if (typeof CanvasRenderingContext2D === "undefined") return defaultAvatarUrl;

  const canvas = document.createElement("canvas");
  canvas.width = 256;
  canvas.height = 256;
  const context = canvas.getContext("2d");
  if (!context) return defaultAvatarUrl;

  context.fillStyle = "#e8f5fe";
  context.fillRect(0, 0, 256, 256);
  context.fillStyle = "#1d9bf0";
  context.font =
    '700 128px Arial, "PingFang SC", "Noto Sans CJK SC", sans-serif';
  context.textAlign = "left";
  context.textBaseline = "alphabetic";
  const metrics = context.measureText(initial);
  const width = metrics.actualBoundingBoxLeft + metrics.actualBoundingBoxRight;
  const height =
    metrics.actualBoundingBoxAscent + metrics.actualBoundingBoxDescent;
  context.fillText(
    initial,
    (256 - width) / 2 + metrics.actualBoundingBoxLeft,
    (256 - height) / 2 + metrics.actualBoundingBoxAscent,
  );
  const url = canvas.toDataURL("image/png");
  images.set(initial, url);
  return url;
}
