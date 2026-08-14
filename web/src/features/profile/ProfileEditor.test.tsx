import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SiteIdentity } from "../../api/public";
import { FeedbackProvider } from "../../components/Feedback";
import { OwnerProvider } from "../owner/OwnerProvider";
import { ProfileEditor } from "./ProfileEditor";

const sessionKey = "moment.owner.session.v1";
const mediaId = "01ARZ3NDEKTSV4RRFFQ69G5FAY";

const site: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "Moment",
  title: "Moment",
  bio: "一段简介",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: mediaId,
  coverMediaId: mediaId,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function siteEnvelope(
  coverMediaId: string | null,
  avatarMediaId: string | null = mediaId,
) {
  return {
    data: {
      owner_id: "00000000000000000000000000000000",
      username: "owner",
      display_name: "更新后的 Moment",
      title: "更新后的 Moment",
      bio: "一段简介",
      timezone: "Asia/Shanghai",
      public_base_url: null,
      avatar_media_id: avatarMediaId,
      cover_media_id: coverMediaId,
      pinned_post_id: null,
      comments_enabled: true,
    },
    meta: { request_id: "req_profile" },
  };
}

describe("profile editor", () => {
  beforeEach(() => {
    window.sessionStorage.setItem(
      sessionKey,
      JSON.stringify({
        csrf_token: "csrf-profile",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    vi.stubGlobal("scrollTo", vi.fn());
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    window.sessionStorage.clear();
  });

  it("previews removal and publishes text with both profile media updates", async () => {
    vi.spyOn(window, "scrollY", "get").mockReturnValue(520);
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v1/owner/status") {
          return json({
            data: {
              initialized: true,
              password_enabled: false,
              session_epoch: 1,
              initialized_at: "2026-08-01T00:00:00Z",
            },
            meta: { request_id: "req_status" },
          });
        }
        if (url === "/api/v1/owner/site") {
          bodies.push(JSON.parse(String(init?.body)));
          return json(siteEnvelope(null, null));
        }
        return json({}, 404);
      }),
    );
    const onSaved = vi.fn();
    render(
      <FeedbackProvider>
        <OwnerProvider>
          <ProfileEditor onSaved={onSaved} site={site} />
        </OwnerProvider>
      </FeedbackProvider>,
    );

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "编辑资料" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "编辑资料" }));
    expect(
      screen.getByRole("dialog", { name: "编辑资料" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "关闭编辑资料" }),
    ).toBeInTheDocument();
    expect(document.documentElement.style.overflowY).toBe("hidden");
    expect(document.body.style.position).toBe("fixed");
    expect(document.body.style.top).toBe("-520px");
    expect(screen.getByRole("img", { name: "封面预览" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "编辑封面" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "移除封面" }));
    fireEvent.click(screen.getByRole("button", { name: "编辑头像" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "移除头像" }));
    fireEvent.change(screen.getByLabelText("显示名称"), {
      target: { value: "更新后的 Moment" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(document.documentElement.style.overflowY).toBe(""),
    );
    expect(document.body.style.position).toBe("");
    expect(window.scrollTo).toHaveBeenCalledWith(0, 520);
    expect(bodies).toEqual([
      {
        display_name: "更新后的 Moment",
        avatar_media_id: null,
        cover_media_id: null,
      },
    ]);
    expect(await screen.findByText("个人资料已更新")).toBeInTheDocument();
  });

  it("uses icon media controls and can reset unsaved profile changes", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input) === "/api/v1/owner/status") {
          return json({
            data: {
              initialized: true,
              password_enabled: false,
              session_epoch: 1,
              initialized_at: "2026-08-01T00:00:00Z",
            },
            meta: { request_id: "req_status" },
          });
        }
        return json({}, 404);
      }),
    );
    render(
      <FeedbackProvider>
        <OwnerProvider>
          <ProfileEditor onSaved={vi.fn()} site={site} />
        </OwnerProvider>
      </FeedbackProvider>,
    );

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "编辑资料" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "编辑资料" }));

    const preview = screen.getByRole("region", { name: "个人资料预览" });
    expect(preview).toHaveTextContent("Moment");
    expect(preview).toHaveTextContent("@owner");
    expect(screen.queryByText("调整访客看到的身份与主页外观")).toBeNull();
    expect(screen.queryByText("为你的时间线留一块背景")).toBeNull();
    expect(
      screen.queryByText("这些内容会显示在你的个人资料与分享页面。"),
    ).toBeNull();
    const coverInput = document.querySelector<HTMLInputElement>(
      'input[data-profile-media-input="cover"]',
    )!;
    expect(coverInput).not.toBeNull();
    const coverInputClick = vi.spyOn(coverInput, "click");
    const coverAction = screen.getByRole("button", { name: "编辑封面" });
    expect(coverAction).toHaveTextContent(/^$/);
    fireEvent.click(coverAction);
    expect(coverInputClick).not.toHaveBeenCalled();
    expect(screen.getByRole("menu", { name: "编辑封面" })).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "移除封面" }),
    ).not.toHaveAttribute("data-disabled");
    fireEvent.click(screen.getByRole("menuitem", { name: "更换封面" }));
    expect(coverInputClick).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(coverAction).toHaveFocus());

    fireEvent.click(coverAction);
    fireEvent.click(screen.getByRole("menuitem", { name: "移除封面" }));
    expect(screen.queryByRole("img", { name: "封面预览" })).toBeNull();
    await waitFor(() => expect(coverAction).toHaveFocus());

    fireEvent.click(coverAction);
    expect(screen.getByRole("menuitem", { name: "移除封面" })).toHaveAttribute(
      "data-disabled",
    );
    fireEvent.keyDown(screen.getByRole("menu", { name: "编辑封面" }), {
      key: "Escape",
    });
    await waitFor(() => expect(coverAction).toHaveFocus());

    const avatarInput = document.querySelector<HTMLInputElement>(
      'input[data-profile-media-input="avatar"]',
    )!;
    expect(avatarInput).not.toBeNull();
    const avatarInputClick = vi.spyOn(avatarInput, "click");
    const avatarAction = screen.getByRole("button", { name: "编辑头像" });
    expect(screen.queryByText("移除头像")).toBeNull();
    fireEvent.click(avatarAction);
    expect(avatarInputClick).not.toHaveBeenCalled();
    expect(screen.getByRole("menu", { name: "编辑头像" })).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "移除头像" }),
    ).not.toHaveAttribute("data-disabled");
    fireEvent.click(screen.getByRole("menuitem", { name: "更换头像" }));
    expect(avatarInputClick).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(avatarAction).toHaveFocus());

    fireEvent.click(avatarAction);
    fireEvent.click(screen.getByRole("menuitem", { name: "移除头像" }));
    expect(screen.queryByRole("img", { name: "头像预览" })).toBeNull();
    await waitFor(() => expect(avatarAction).toHaveFocus());

    const reset = screen.getByRole("button", { name: "重置资料更改" });
    expect(reset).toBeEnabled();
    fireEvent.change(screen.getByLabelText("显示名称"), {
      target: { value: "尚未保存的名称" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: /用户名/ }), {
      target: { value: "new_owner" },
    });
    expect(preview).toHaveTextContent("尚未保存的名称");
    expect(preview).toHaveTextContent("@new_owner");
    expect(reset).toBeEnabled();
    fireEvent.click(reset);

    expect(screen.getByLabelText("显示名称")).toHaveValue("Moment");
    expect(preview).toHaveTextContent("Moment");
    expect(preview).toHaveTextContent("@owner");
    expect(screen.getByRole("img", { name: "封面预览" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "头像预览" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存" })).toBeDisabled();
  });
});
