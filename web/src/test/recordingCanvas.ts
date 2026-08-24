/**
 * A 2d context that writes down what it was asked to draw.
 *
 * jsdom has no canvas, so the share card's renderer -- the part of it that
 * decides what a card actually looks like -- had no way to be tested at all.
 * Rather than bring in a real rasteriser to compare pixels, this records the
 * calls: a test can then ask whether a badge was drawn over the second tile,
 * or which colours a line of code came out in, without owning a screenshot.
 *
 * Two fidelity gaps worth knowing: `save`/`restore` are recorded but do not
 * restore the fill, stroke or font, and nothing is clipped. Assert on a call's
 * recorded state -- which is what was set when the call was made -- rather than
 * on what a real context would have restored to.
 *
 * Text measurement is a rough model of a proportional face -- a full-width
 * character takes a full em, everything else takes half. Layout is what
 * measurement feeds, so this is deliberately predictable rather than accurate:
 * a test asserting where a line broke is asserting against this model, and
 * should say so.
 */
export interface DrawCall {
  op: string;
  args: unknown[];
  fill: string;
  stroke: string;
  font: string;
}

export interface Recording {
  calls: DrawCall[];
  /** Every call of one kind, in the order they were made. */
  of: (op: string) => DrawCall[];
  /** The text drawn, joined in order. */
  text: () => string;
}

const WIDE = /[ᄀ-ᅟ⺀-꓏가-힣豈-﫿︰-﹯＀-｠￠-￦]/;

function sizeOf(font: string): number {
  return Number(/(\d+(?:\.\d+)?)px/.exec(font)?.[1] ?? 16);
}

/**
 * Install the recording context for the length of a test.
 *
 * Returns the recording plus a restore function; the caller is expected to
 * restore in an afterEach so one test's stub cannot outlive it.
 */
export function recordCanvas(): { recording: Recording; restore: () => void } {
  const calls: DrawCall[] = [];
  const realGetContext = HTMLCanvasElement.prototype.getContext;
  const realToBlob = HTMLCanvasElement.prototype.toBlob;

  const makeContext = () => {
    const state = {
      fillStyle: "#000000",
      strokeStyle: "#000000",
      font: "16px sans-serif",
    };
    const note = (op: string, ...args: unknown[]) => {
      calls.push({
        op,
        args,
        fill: String(state.fillStyle),
        stroke: String(state.strokeStyle),
        font: state.font,
      });
    };
    const context = {
      get fillStyle() {
        return state.fillStyle;
      },
      set fillStyle(value: string) {
        state.fillStyle = value;
      },
      get strokeStyle() {
        return state.strokeStyle;
      },
      set strokeStyle(value: string) {
        state.strokeStyle = value;
      },
      get font() {
        return state.font;
      },
      set font(value: string) {
        state.font = value;
      },
      lineWidth: 1,
      lineJoin: "miter",
      textAlign: "start",
      textBaseline: "alphabetic",
      measureText: (text: string) => {
        const size = sizeOf(state.font);
        let width = 0;
        for (const glyph of text) width += WIDE.test(glyph) ? size : size / 2;
        return { width };
      },
    } as unknown as CanvasRenderingContext2D & Record<string, unknown>;

    for (const op of [
      "arc",
      "beginPath",
      "clip",
      "closePath",
      "drawImage",
      "fill",
      "fillRect",
      "fillText",
      "lineTo",
      "moveTo",
      "restore",
      "roundRect",
      "save",
      "scale",
      "stroke",
    ]) {
      context[op] = (...args: unknown[]) => note(op, ...args);
    }
    return context;
  };

  HTMLCanvasElement.prototype.getContext = function getContext(kind: string) {
    return kind === "2d" ? makeContext() : null;
  } as typeof HTMLCanvasElement.prototype.getContext;

  HTMLCanvasElement.prototype.toBlob = function toBlob(
    callback: BlobCallback,
    type?: string,
  ) {
    callback(new Blob(["card"], { type: type ?? "image/png" }));
  };

  return {
    recording: {
      calls,
      of: (op: string) => calls.filter((call) => call.op === op),
      text: () =>
        calls
          .filter((call) => call.op === "fillText")
          .map((call) => String(call.args[0]))
          .join(""),
    },
    restore: () => {
      HTMLCanvasElement.prototype.getContext = realGetContext;
      HTMLCanvasElement.prototype.toBlob = realToBlob;
    },
  };
}

/**
 * Make every image load, immediately.
 *
 * The renderer waits on a real `load` event before it measures anything, and
 * jsdom never fetches, so without this every card renders with no pictures on
 * it. Setting the property rather than replacing the constructor keeps the
 * element a real element -- a Proxy around Image breaks appendChild.
 */
export function resolveImages(size = { width: 1200, height: 900 }): () => void {
  const real = Object.getOwnPropertyDescriptor(
    HTMLImageElement.prototype,
    "src",
  );
  const loaded = new Set<string>();
  Object.defineProperty(HTMLImageElement.prototype, "src", {
    configurable: true,
    get(this: HTMLImageElement) {
      return loaded.has(this.dataset.recordedSrc ?? "")
        ? (this.dataset.recordedSrc ?? "")
        : "";
    },
    set(this: HTMLImageElement, value: string) {
      this.dataset.recordedSrc = value;
      loaded.add(value);
      Object.defineProperty(this, "naturalWidth", {
        configurable: true,
        value: size.width,
      });
      Object.defineProperty(this, "naturalHeight", {
        configurable: true,
        value: size.height,
      });
      queueMicrotask(() => this.dispatchEvent(new Event("load")));
    },
  });
  return () => {
    if (real) Object.defineProperty(HTMLImageElement.prototype, "src", real);
  };
}
