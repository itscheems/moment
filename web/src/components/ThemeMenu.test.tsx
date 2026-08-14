import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SiteIdentity } from "../api/public";
import { ThemeProvider } from "../theme/ThemeProvider";
import { ThemeMenu } from "./ThemeMenu";

const site: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "Moment",
  title: "Cheems Notes",
  bio: "",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: "01K2ABCDEF0123456789ABCDEFG",
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};

afterEach(cleanup);

describe("ThemeMenu", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    );
  });

  it("previews the site identity and applies every accent and background choice", async () => {
    render(
      <ThemeProvider>
        <ThemeMenu site={site} />
      </ThemeProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "更多" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /显示/ }));

    const dialog = await screen.findByRole("dialog", { name: "自定义显示" });
    const preview = within(dialog).getByLabelText("显示效果预览");
    expect(within(preview).getByText("Cheems Notes")).toBeInTheDocument();
    expect(within(preview).getByText("@owner · 刚刚")).toBeInTheDocument();
    expect(within(preview).getByText("@owner")).toBeInTheDocument();
    expect(preview.querySelector("img")).toHaveAttribute(
      "src",
      "/api/v1/media/01K2ABCDEF0123456789ABCDEFG/preview",
    );

    const accentChoices = [
      ["蓝色", "blue"],
      ["黄色", "yellow"],
      ["粉色", "pink"],
      ["紫色", "purple"],
      ["橙色", "orange"],
      ["绿色", "green"],
    ] as const;
    const backgroundChoices = [
      ["跟随系统", "light"],
      ["浅色", "light"],
      ["柔和深色", "dim"],
      ["纯黑", "lights-out"],
    ] as const;
    for (const [label, resolvedTheme] of backgroundChoices) {
      fireEvent.click(within(dialog).getByRole("button", { name: label }));
      expect(
        within(dialog).getByRole("button", { name: label }),
      ).toHaveAttribute("aria-pressed", "true");
      expect(document.documentElement).toHaveAttribute(
        "data-theme",
        resolvedTheme,
      );
      for (const [accentLabel, value] of accentChoices) {
        fireEvent.click(within(dialog).getByTitle(accentLabel));
        expect(document.documentElement).toHaveAttribute("data-accent", value);
        expect(within(dialog).getByTitle(accentLabel)).toHaveAttribute(
          "aria-pressed",
          "true",
        );
      }
    }
  });

  it("reports a saved light override as light rather than system", async () => {
    window.localStorage.setItem("moment.theme.v1", "light");

    render(
      <ThemeProvider>
        <ThemeMenu site={site} />
      </ThemeProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "更多" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /显示/ }));

    const dialog = await screen.findByRole("dialog", { name: "自定义显示" });
    expect(
      within(dialog).getByRole("button", { name: "浅色" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(
      within(dialog).getByRole("button", { name: "跟随系统" }),
    ).toHaveAttribute("aria-pressed", "false");
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
  });
});
