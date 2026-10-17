import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CropperProps } from "react-easy-crop";
import { MediaCropPanel } from "./MediaCropPanel";
vi.mock("react-easy-crop", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-easy-crop")>();
  return {
    ...actual,
    default: (props: CropperProps) => (
      <div>
        <output data-testid="engine">
          {JSON.stringify({
            aspect: props.aspect,
            zoom: props.zoom,
            crop: props.crop,
            size: props.cropSize,
          })}
        </output>
        <button
          onClick={() =>
            props.setMediaSize?.({
              width: 0,
              height: 0,
              naturalWidth: 0,
              naturalHeight: 0,
            })
          }
        >
          Before decode
        </button>
        <button
          onClick={() =>
            props.setMediaSize?.({
              width: 600,
              height: 300,
              naturalWidth: 1200,
              naturalHeight: 600,
            })
          }
        >
          Decoded
        </button>
        <button
          onClick={() => {
            props.onCropChange({ x: NaN, y: Infinity });
            props.onZoomChange?.(NaN);
          }}
        >
          Transient invalid coordinates
        </button>
      </div>
    ),
  };
});
beforeEach(() =>
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(private callback: ResizeObserverCallback) {}
      observe() {
        this.callback(
          [{ contentRect: { width: 600, height: 481 } } as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        );
      }
      unobserve() {}
      disconnect() {}
    },
  ),
);
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
const state = () => JSON.parse(screen.getByTestId("engine").textContent!);
describe("cropper loading boundary", () => {
  it("ignores measurements before decoding and accepts the decoded image ratio", () => {
    render(
      <MediaCropPanel
        source="/image.png"
        disabled={false}
        onChange={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Before decode" }));
    expect(state().aspect).toBe(1);
    expect(Number.isFinite(state().size.width)).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Decoded" }));
    expect(state().aspect).toBe(2);
    expect(state().size).toEqual({ width: 550, height: 275 });
    fireEvent.click(screen.getByRole("button", { name: "Before decode" }));
    expect(state().aspect).toBe(2);
  });
  it("keeps invalid intermediate positions and zoom out of the controlled cropper", () => {
    render(
      <MediaCropPanel
        source="/image.png"
        disabled={false}
        onChange={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Decoded" }));
    const before = state();
    fireEvent.click(
      screen.getByRole("button", { name: "Transient invalid coordinates" }),
    );
    expect(state()).toEqual(before);
  });
});
