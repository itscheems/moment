import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Icon } from "./icons";

describe("Icon", () => {
  it.each([
    ["home", "nav-home", "nav-home-active"],
    ["search", "nav-search", "nav-search-active"],
    ["archive", "nav-archive", "nav-archive-active"],
    ["owner", "nav-profile", "nav-profile-active"],
    ["more", "nav-more", "nav-more-active"],
  ] as const)(
    "uses reusable navigation artwork for %s",
    (name, idleSymbol, activeSymbol) => {
      const { container, rerender } = render(
        <Icon name={name} variant="navigation" />,
      );
      const icon = container.querySelector(`svg[data-icon="${name}"]`);

      expect(icon?.getAttribute("fill")).toBe("currentColor");
      expect(icon?.querySelector("use")?.getAttribute("href")).toBe(
        `/icons/ui-symbols-v3.svg#${idleSymbol}`,
      );

      rerender(<Icon name={name} variant="navigation-active" />);
      expect(icon?.querySelector("use")?.getAttribute("href")).toBe(
        `/icons/ui-symbols-v3.svg#${activeSymbol}`,
      );
    },
  );

  it("renders the profile camera artwork as a current-color filled icon", () => {
    const { container } = render(<Icon name="camera" />);
    const icon = container.querySelector('svg[data-icon="camera"]');
    const use = icon?.querySelector("use");

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(use?.getAttribute("href")).toBe(
      "/icons/ui-symbols-v3.svg#profile-camera",
    );
  });

  it("renders the trash artwork as a current-color filled icon", () => {
    const { container } = render(<Icon name="delete" />);
    const icon = container.querySelector('svg[data-icon="delete"]');
    const use = icon?.querySelector("use");

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(use?.getAttribute("href")).toBe("/icons/ui-symbols-v3.svg#trash");
  });

  it("renders the share artwork as a current-color filled icon", () => {
    const { container } = render(<Icon name="share" />);
    const icon = container.querySelector('svg[data-icon="share"]');

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(icon?.querySelector("path")?.getAttribute("d")).toBe(
      "M12 2.59l5.7 5.7-1.41 1.42L13 6.41V16h-2V6.41l-3.3 3.3-1.41-1.42L12 2.59zM21 15l-.02 3.51c0 1.38-1.12 2.49-2.5 2.49H5.5C4.11 21 3 19.88 3 18.5V15h2v3.5c0 .28.22.5.5.5h12.98c.28 0 .5-.22.5-.5L19 15h2z",
    );
  });

  it("renders the media removal artwork from the shared sprite", () => {
    const { container } = render(<Icon name="media-remove" />);
    const icon = container.querySelector('svg[data-icon="media-remove"]');

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(icon?.querySelector("use")?.getAttribute("href")).toBe(
      "/icons/ui-symbols-v3.svg#media-remove",
    );
  });
});
