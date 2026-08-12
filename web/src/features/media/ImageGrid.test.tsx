import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { PublicMediaUsage } from "../../api/public";
import { ImageGrid } from "./ImageGrid";

afterEach(cleanup);

function media(count: number): PublicMediaUsage[] {
  return Array.from({ length: count }, (_, position) => ({
    mediaId: `01K2ABCDEF0123456789ABCD${String(position).padStart(2, "0")}`,
    position,
    altText:
      position === 0 ? null : position === 1 ? "" : `第 ${position + 1} 张描述`,
  }));
}

describe("public image grid", () => {
  it.each([0, 1, 2, 3, 4, 9])(
    "renders the deliberate %i-image layout",
    (count) => {
      const { container } = render(<ImageGrid media={media(count)} />);
      if (count === 0) {
        expect(container).toBeEmptyDOMElement();
        return;
      }
      const group = screen.getByRole("group", { name: `${count} 张图片` });
      expect(group).toHaveAttribute("data-media-count", String(count));
      expect(group.querySelectorAll("img")).toHaveLength(count);
      for (const image of group.querySelectorAll("img")) {
        expect(image).toHaveAttribute("loading", "lazy");
        expect(image.getAttribute("src")).toMatch(/\/preview$/);
      }
    },
  );

  it("renders all nullable alt states without disguising missing as decorative", () => {
    render(<ImageGrid media={media(3)} />);
    const images = screen
      .getByRole("group", { name: "3 张图片" })
      .querySelectorAll("img");
    expect(images[0]).toHaveAttribute(
      "alt",
      "未提供图片描述，第 1 张，共 3 张",
    );
    expect(images[1]).toHaveAttribute("alt", "");
    expect(images[2]).toHaveAttribute("alt", "第 3 张描述");
    expect(
      screen.getByRole("button", {
        name: "打开第 1 张图片，共 3 张：尚未提供图片描述",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "打开第 2 张图片，共 3 张：明确标记为纯装饰",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "打开第 3 张图片，共 3 张：第 3 张描述",
      }),
    ).toBeInTheDocument();
  });

  it("keeps a useful fallback when a preview is missing", () => {
    render(<ImageGrid media={media(1)} />);
    fireEvent.error(screen.getByRole("img", { name: /未提供图片描述/ }));
    expect(screen.getByText("图片暂不可用")).toBeInTheDocument();
  });

  it("does not carry a failed preview across reordered or replaced usages", () => {
    const initial = media(2);
    const { rerender } = render(<ImageGrid media={initial} />);
    fireEvent.error(screen.getByRole("img", { name: /未提供图片描述/ }));
    expect(screen.getByText("图片暂不可用")).toBeInTheDocument();

    rerender(
      <ImageGrid
        media={[
          { ...initial[1], position: 0 },
          {
            mediaId: "01K2ABCDEF0123456789ABCD09",
            position: 1,
            altText: "替换后的图片",
          },
        ]}
      />,
    );

    expect(screen.queryByText("图片暂不可用")).toBeNull();
    expect(
      screen.getByRole("group", { name: "2 张图片" }).querySelectorAll("img"),
    ).toHaveLength(2);
    expect(screen.getByRole("img", { name: "替换后的图片" })).toHaveAttribute(
      "src",
      "/api/v1/media/01K2ABCDEF0123456789ABCD09/preview",
    );
  });

  it("opens on preview, links to the original, and restores focus", async () => {
    render(<ImageGrid media={media(1)} />);
    const trigger = screen.getByRole("button", {
      name: "打开第 1 张图片，共 1 张：尚未提供图片描述",
    });
    trigger.focus();
    fireEvent.click(trigger);

    const dialog = await screen.findByRole("dialog");
    expect(dialog.querySelector("img")).toHaveAttribute(
      "src",
      "/api/v1/media/01K2ABCDEF0123456789ABCD00/preview",
    );
    expect(
      dialog.querySelector(
        'img[src="/api/v1/media/01K2ABCDEF0123456789ABCD00"]',
      ),
    ).toBeNull();
    expect(screen.getByRole("link", { name: "打开原图" })).toHaveAttribute(
      "href",
      "/api/v1/media/01K2ABCDEF0123456789ABCD00",
    );
    expect(screen.getByRole("link", { name: "打开原图" })).toHaveAttribute(
      "target",
      "_blank",
    );
    expect(dialog.querySelector("img")).toHaveAttribute(
      "src",
      "/api/v1/media/01K2ABCDEF0123456789ABCD00/preview",
    );

    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(trigger).toHaveFocus());
  });
});
