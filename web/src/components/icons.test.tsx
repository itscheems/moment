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
        `/icons/ui-symbols-v2.svg#${idleSymbol}`,
      );

      rerender(<Icon name={name} variant="navigation-active" />);
      expect(icon?.querySelector("use")?.getAttribute("href")).toBe(
        `/icons/ui-symbols-v2.svg#${activeSymbol}`,
      );
    },
  );

  it("renders the profile camera artwork as a current-color filled icon", () => {
    const { container } = render(<Icon name="camera" />);
    const icon = container.querySelector('svg[data-icon="camera"]');
    const use = icon?.querySelector("use");

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(use?.getAttribute("href")).toBe(
      "/icons/ui-symbols-v2.svg#profile-camera",
    );
  });

  it("renders the trash artwork as a current-color filled icon", () => {
    const { container } = render(<Icon name="delete" />);
    const icon = container.querySelector('svg[data-icon="delete"]');
    const use = icon?.querySelector("use");

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(use?.getAttribute("href")).toBe("/icons/ui-symbols-v2.svg#trash");
  });

  it("renders the media removal artwork from the shared sprite", () => {
    const { container } = render(<Icon name="media-remove" />);
    const icon = container.querySelector('svg[data-icon="media-remove"]');

    expect(icon?.getAttribute("fill")).toBe("currentColor");
    expect(icon?.querySelector("use")?.getAttribute("href")).toBe(
      "/icons/ui-symbols-v2.svg#media-remove",
    );
  });
});
