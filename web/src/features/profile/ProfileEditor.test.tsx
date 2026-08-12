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
  title: "Moment",
  bio: "一段简介",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: mediaId,
  coverMediaId: mediaId,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
};

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function siteEnvelope(coverMediaId: string | null) {
  return {
    data: {
      title: "更新后的 Moment",
      bio: "一段简介",
      timezone: "Asia/Shanghai",
      public_base_url: null,
      avatar_media_id: mediaId,
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
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    window.sessionStorage.clear();
  });

  it("previews removal and publishes text and cover in one profile update", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v1/owner/status") {
          return json({
            data: {
              initialized: true,
              session_epoch: 1,
              initialized_at: "2026-08-01T00:00:00Z",
            },
            meta: { request_id: "req_status" },
          });
        }
        if (url === "/api/v1/owner/site") {
          bodies.push(JSON.parse(String(init?.body)));
          return json(siteEnvelope(null));
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
      expect(
        screen.getByRole("button", { name: "编辑个人资料" }),
      ).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "编辑个人资料" }));
    expect(screen.getByRole("img", { name: "封面预览" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "移除封面图片" }));
    fireEvent.change(screen.getByLabelText("名称"), {
      target: { value: "更新后的 Moment" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(bodies).toEqual([
      { title: "更新后的 Moment", cover_media_id: null },
    ]);
    expect(await screen.findByText("个人资料已更新")).toBeInTheDocument();
  });
});
