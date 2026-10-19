import { expect, it, vi } from "vitest";
import { EditMediaHistory } from "./editMediaHistory";
import { applyUpload, newMediaItem } from "./mediaState";

it("restores the completed upload with historical descriptions and retains previews until editing ends", () => {
  const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  const history = new EditMediaHistory();
  const item = {
    ...newMediaItem(new File(["image"], "photo.png", { type: "image/png" })),
    localPreviewUrl: "blob:original",
  };
  history.remember([item]);
  const beforeUpload = history.signature([item]);
  const uploaded = {
    ...applyUpload(item, {
      id: "uploaded-image",
      byteSize: 5,
      width: 2,
      height: 2,
      mimeType: "image/png",
      previewMimeType: "image/png",
      animatedOriginal: false,
      previewUrl: "/preview",
      originalUrl: "/file",
    }),
    altMode: "provided" as const,
    altText: "后来的描述",
  };
  const completed = {
    ...uploaded,
    altMode: item.altMode,
    altText: item.altText,
  };
  history.remember([completed], [item]);
  expect(history.signature([completed])).toEqual(beforeUpload);
  history.remember([uploaded], [completed]);
  history.remember([]);
  const restored = history.restore([item])[0];
  expect(restored.mediaId).toBe("uploaded-image");
  expect(restored.state).toBe("ready");
  expect(restored.file).toBeNull();
  expect(restored.altText).toBe("");
  expect(revoke).not.toHaveBeenCalled();
  history.clear();
  expect(revoke).toHaveBeenCalledWith("blob:original");
  revoke.mockRestore();
});
