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

  it("previews the site identity and applies accent and background choices", async () => {
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

    fireEvent.click(within(dialog).getByTitle("粉色"));
    expect(document.documentElement).toHaveAttribute("data-accent", "pink");
    expect(within(dialog).getByTitle("粉色")).toHaveAttribute(
      "aria-pressed",
      "true",
    );

    expect(
      within(dialog).getByRole("button", { name: "默认" }),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "柔和深色" }),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("button", { name: /浅色|跟随系统/ }),
    ).not.toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: /纯黑/ }));
    expect(document.documentElement).toHaveAttribute(
      "data-theme",
      "lights-out",
    );
  });
});
