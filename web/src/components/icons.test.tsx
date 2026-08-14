import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Icon } from "./icons";

describe("Icon", () => {
  it("renders the profile camera artwork as a current-color filled icon", () => {
    const { container } = render(<Icon name="camera" />);
    const icon = container.querySelector('svg[data-icon="camera"]');

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(icon?.querySelectorAll("path")).toHaveLength(3);
    expect(icon?.querySelector('path[fill-rule="evenodd"]')).not.toBeNull();
  });

  it("renders the trash artwork as a current-color filled icon", () => {
    const { container } = render(<Icon name="delete" />);
    const icon = container.querySelector('svg[data-icon="delete"]');
    const path = icon?.querySelector("path");

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(path?.getAttribute("d")).toContain("M16 6V4.5C16 3.12");
  });
});
