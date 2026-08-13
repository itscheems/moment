import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { PublicApi, type SiteIdentity } from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { FeedbackProvider } from "../../components/Feedback";
import { OwnerProvider } from "../owner/OwnerProvider";
import { ProfilePage } from "./ProfilePage";

const pinnedId = "01K2ABCDEF0123456789ABCDE0";
const otherId = "01K2ABCDEF0123456789ABCDE1";

const site: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "Moment",
  title: "Moment",
  bio: "",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: pinnedId,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: "2026-08-01T09:08:00Z",
};

function post(id: string, text: string) {
  return {
    id,
    content_md: text,
    content_html: `<p>${text}</p>`,
    created_at: "2026-08-10T01:00:00.000Z",
    updated_at: "2026-08-10T01:00:00.000Z",
    published_at: "2026-08-10T01:00:00.000Z",
    comment_count: 0,
    media: [],
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  window.sessionStorage.clear();
});

describe("profile page", () => {
  it("renders the singleton pin first without duplicating its timeline row", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        json({
          data: {
            initialized: true,
            password_enabled: false,
            session_epoch: 1,
            initialized_at: "2026-08-01T00:00:00Z",
          },
          meta: { request_id: "req_status" },
        }),
      ),
    );
    const api = new PublicApi(async (input) => {
      const url = String(input);
      if (url.startsWith("/api/v1/posts?")) {
        return json({
          data: {
            items: [post(pinnedId, "置顶内容"), post(otherId, "普通内容")],
          },
          meta: { next_cursor: null },
        });
      }
      if (url === `/api/v1/posts/${pinnedId}`) {
        return json({ data: post(pinnedId, "置顶内容"), meta: {} });
      }
      return json({}, 404);
    });

    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <ProfilePage api={api} onSiteSaved={vi.fn()} site={site} />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    await waitFor(() =>
      expect(screen.getAllByText("置顶内容")).toHaveLength(1),
    );
    expect(
      screen.getByRole("navigation", { name: "个人资料内容" }),
    ).toHaveTextContent("Moment");
    expect(screen.queryByText("媒体与归档")).not.toBeInTheDocument();
    expect(screen.getByText("@owner")).toBeInTheDocument();
    expect(screen.getByText("2026年8月加入")).toHaveAttribute(
      "datetime",
      site.instanceInitializedAt,
    );
    expect(screen.getByRole("link", { name: "查看归档" })).toHaveAttribute(
      "href",
      "/archive",
    );
    expect(screen.getByText("已置顶")).toBeInTheDocument();
    const pinned = screen.getByText("置顶内容").closest("article");
    const normal = screen.getByText("普通内容").closest("article");
    expect(
      pinned && normal
        ? pinned.compareDocumentPosition(normal) &
            Node.DOCUMENT_POSITION_FOLLOWING
        : 0,
    ).toBeTruthy();
  });
});
