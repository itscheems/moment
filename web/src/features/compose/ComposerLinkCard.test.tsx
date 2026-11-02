import "@testing-library/jest-dom/vitest";
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ComposerLinkCard } from "./ComposerLinkCard";
import type { PublicLinkCard } from "../../api/public";

const card: PublicLinkCard = {
  position: 0,
  canonicalUrl: "https://example.com/",
  cardKind: "link",
  title: "Saved page",
  description: "",
  siteName: "Example",
  authorName: "",
  durationSeconds: null,
  viewCount: null,
  commentCount: null,
  likeCount: null,
  faviconMediaId: null,
  thumbnailMediaId: null,
  thumbnailMimeType: null,
  thumbnailWidth: null,
  thumbnailHeight: null,
  embedUrl: null,
};
afterEach(cleanup);
it("selects a layout through the existing radio menu without removing the card", async () => {
  const change = vi.fn(),
    remove = vi.fn();
  render(
    <ComposerLinkCard
      card={card}
      disabled={false}
      onRemove={remove}
      onDisplayModeChange={change}
    />,
  );
  const trigger = screen.getByRole("button", { name: "链接卡片展示方式" });
  fireEvent.keyDown(trigger, { key: "ArrowDown", code: "ArrowDown" });
  const cover = await screen.findByRole("menuitemradio", { name: "大图封面" });
  expect(cover).toHaveAttribute("aria-checked", "true");
  fireEvent.click(screen.getByRole("menuitemradio", { name: "小图卡片" }));
  expect(change).toHaveBeenCalledWith("compact");
  expect(remove).not.toHaveBeenCalled();
  expect(screen.getByRole("region", { name: "链接预览" })).toHaveTextContent(
    "Saved page",
  );
});
it("disables layout changes while saving", () => {
  render(
    <ComposerLinkCard
      card={card}
      disabled
      onRemove={vi.fn()}
      onDisplayModeChange={vi.fn()}
    />,
  );
  expect(
    screen.getByRole("button", { name: "链接卡片展示方式" }),
  ).toBeDisabled();
});
