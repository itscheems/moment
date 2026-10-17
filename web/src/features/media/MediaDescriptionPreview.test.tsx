import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";
import { MediaDescriptionEditor } from "./MediaDescriptionEditor";
import { createMediaCrop, type MediaCropSelection } from "./mediaCrop";
import { newMediaItem, type ComposerMediaItem } from "./mediaState";

vi.mock("./mediaCrop", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./mediaCrop")>()),
  createMediaCrop: vi.fn(),
}));
vi.mock("./MediaCropPanel", () => ({
  MediaCropPanel: ({
    onChange,
  }: {
    onChange: (value: MediaCropSelection) => void;
  }) => (
    <button
      onClick={() =>
        onChange({
          aspect: "square",
          area: { x: 100, y: 50, width: 300, height: 300 },
          percentages: { x: 100 / 12, y: 5, width: 25, height: 30 },
        })
      }
    >
      Choose crop
    </button>
  ),
}));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});
function Harness() {
  const [items, setItems] = useState<ComposerMediaItem[]>(
    ["first", "second"].map((key) => ({
      ...newMediaItem(new File([key], key + ".png", { type: "image/png" })),
      key,
      localPreviewUrl: "blob:" + key,
    })),
  );
  return (
    <MediaDescriptionEditor
      items={items}
      disabled={false}
      editRequest={undefined}
      onChange={setItems}
    />
  );
}

describe("crop preview work", () => {
  it("reuses the source across tab changes and image paging, and exports only on Save", async () => {
    const create = vi
      .mocked(createMediaCrop)
      .mockResolvedValue(
        new File(["cropped"], "cropped.png", { type: "image/png" }),
      );
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:saved");
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "Add descriptions" }));
    fireEvent.click(screen.getByRole("tab", { name: "Crop media" }));
    fireEvent.click(screen.getByRole("button", { name: "Choose crop" }));
    fireEvent.click(
      screen.getByRole("tab", { name: "Edit image description" }),
    );
    const preview = screen.getByRole("img", { name: "Image 1 of 2" });
    expect(preview).toHaveAttribute("viewBox", "100 50 300 300");
    const source = preview.querySelector("image")!;
    expect(source).toHaveAttribute("href", "blob:first");
    expect(source).toHaveAttribute("width", "1200");
    expect(source).toHaveAttribute("height", "1000");
    for (let i = 0; i < 6; i++) {
      fireEvent.click(screen.getByRole("tab", { name: "Crop media" }));
      expect(preview).toBeInTheDocument();
      fireEvent.click(
        screen.getByRole("tab", { name: "Edit image description" }),
      );
      expect(screen.getByRole("img", { name: "Image 1 of 2" })).toBe(preview);
    }
    fireEvent.click(screen.getByRole("button", { name: "Next image" }));
    fireEvent.click(screen.getByRole("button", { name: "Previous image" }));
    expect(
      screen.getByRole("img", { name: "Image 1 of 2" }).querySelector("image"),
    ).toHaveAttribute("href", "blob:first");
    expect(screen.queryByText("Preparing media…")).not.toBeInTheDocument();
    expect(create).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(create).toHaveBeenCalledWith(
      { url: "blob:first", name: "first.png", mimeType: "image/png" },
      { x: 100, y: 50, width: 300, height: 300 },
      expect.any(AbortSignal),
    );
  });
});
