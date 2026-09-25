import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { EditorIcon, type EditorIconName } from "./EditorIcons";

const names: EditorIconName[] = [
  "bold",
  "italic",
  "strike",
  "underline",
  "code",
  "quote",
  "bullet-list",
  "ordered-list",
  "link",
  "unlink",
  "table",
  "image",
  "rule",
  "chevron",
];

describe("EditorIcon", () => {
  // An SVG with only a viewBox renders at 300x150, so a container that forgets
  // to size one gets a glyph the height of the page. Carrying its own size
  // means the worst a forgetful container can do is look slightly wrong.
  it.each(names)("gives %s a size of its own", (name) => {
    const { container } = render(<EditorIcon name={name} />);
    const glyph = container.querySelector("svg");
    expect(glyph).not.toBeNull();
    expect(glyph?.getAttribute("width")).toBe("1em");
    expect(glyph?.getAttribute("height")).toBe("1em");
  });

  it("still lets a caller override the size", () => {
    const { container } = render(
      <EditorIcon height="2rem" name="bold" width="2rem" />,
    );
    expect(container.querySelector("svg")?.getAttribute("width")).toBe("2rem");
  });
});
