import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { mediaPreviewUrl } from "../api/mediaUrls";
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

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("ThemeMenu", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.stubGlobal("scrollTo", vi.fn());
  });

  it("keeps the navigation More icon outlined while its menu is open", async () => {
    render(
      <ThemeProvider>
        <ThemeMenu navigation site={site} />
      </ThemeProvider>,
    );

    const trigger = screen.getByRole("button", { name: "更多" });
    const iconUse = trigger.querySelector('svg[data-icon="more"] use');
    expect(iconUse).toHaveAttribute(
      "href",
      "/icons/ui-symbols-v3.svg#nav-more",
    );

    fireEvent.click(trigger);
    await screen.findByRole("menuitem", { name: "显示" });
    await waitFor(() =>
      expect(iconUse).toHaveAttribute(
        "href",
        "/icons/ui-symbols-v3.svg#nav-more",
      ),
    );
  });

  it("locks the page while display settings are open and restores their trigger", async () => {
    vi.spyOn(window, "scrollY", "get").mockReturnValue(275);
    render(
      <ThemeProvider>
        <ThemeMenu site={site} />
      </ThemeProvider>,
    );

    const trigger = screen.getByRole("button", { name: "更多" });
    trigger.focus();
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("menuitem", { name: /显示/ }));

    const dialog = await screen.findByRole("dialog", { name: "自定义显示" });
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
    fireEvent.click(within(dialog).getByRole("button", { name: "完成" }));

    await waitFor(() => expect(dialog).not.toBeInTheDocument());
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.body.style.position).toBe("");
    expect(document.documentElement.style.overflow).toBe("");
    await waitFor(() => expect(trigger).toHaveFocus());
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
      mediaPreviewUrl("01K2ABCDEF0123456789ABCDEFG"),
    );

    const accentChoices = [
      ["蓝色", "blue"],
      ["黄色", "yellow"],
      ["粉色", "pink"],
      ["紫色", "purple"],
      ["橙色", "orange"],
      ["绿色", "green"],
      ["纯黑色", "black"],
    ] as const;
    const backgroundChoices = [
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

  it("offers only the three explicit background choices", async () => {
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
      within(dialog).queryByRole("button", { name: "跟随系统" }),
    ).toBeNull();
    expect(
      within(dialog).getAllByRole("button", {
        name: /^(浅色|柔和深色|纯黑)$/,
      }),
    ).toHaveLength(3);
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
  });
});
