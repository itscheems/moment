import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { PublicApi } from "../../api/public";
import { MemberProfilePage } from "./MemberProfilePage";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function memberEnvelope() {
  return {
    data: {
      user_id: "11111111111111111111111111111111",
      username: "linlin",
      display_name: "林林",
      bio: "在这里记下路过的天气",
      avatar_media_id: null,
      cover_media_id: null,
      role: "member",
      profile_url: "/linlin",
    },
    meta: { request_id: "req_user" },
  };
}

function notFound() {
  return json(
    {
      error: {
        code: "ACCOUNT_NOT_FOUND",
        message: "account not found",
        details: {},
        request_id: "req_missing",
      },
    },
    404,
  );
}

describe("member profile", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders another account's identity over the plain cover band", async () => {
    const requested: string[] = [];
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      requested.push(String(input));
      return json(memberEnvelope());
    });
    const { container } = render(
      <MemberProfilePage
        api={new PublicApi(fetcher as unknown as typeof fetch)}
        username="linlin"
      />,
    );

    expect(await screen.findByRole("heading", { name: "林林" })).toBeVisible();
    expect(screen.getByText("@linlin")).toBeInTheDocument();
    expect(screen.getByText("在这里记下路过的天气")).toBeInTheDocument();
    // No cover of their own falls back to the same colour band the site page
    // uses, rather than to an empty strip that reads as a failed image.
    expect(container.querySelector('[class*="coverFallback"]')).not.toBeNull();
    expect(requested).toEqual(["/api/v1/users/linlin"]);
  });

  it("reads an account that is not there as a page that is not there", async () => {
    render(
      <MemberProfilePage
        api={
          new PublicApi(
            vi.fn(async () => notFound()) as unknown as typeof fetch,
          )
        }
        username="ghost"
      />,
    );

    expect(
      await screen.findByRole("heading", { name: "页面不存在" }),
    ).toBeVisible();
  });
});
