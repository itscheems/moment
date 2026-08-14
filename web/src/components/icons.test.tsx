import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Icon } from "./icons";

describe("Icon", () => {
  it("renders the profile camera artwork as a current-color filled icon", () => {
    const { container } = render(<Icon name="camera" />);
    const icon = container.querySelector('svg[data-icon="camera"]');
    const use = icon?.querySelector("use");

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(use?.getAttribute("href")).toBe(
      "/icons/ui-symbols.svg#profile-camera",
    );
  });

  it("renders the trash artwork as a current-color filled icon", () => {
    const { container } = render(<Icon name="delete" />);
    const icon = container.querySelector('svg[data-icon="delete"]');
    const use = icon?.querySelector("use");

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(use?.getAttribute("href")).toBe("/icons/ui-symbols.svg#trash");
  });
});
