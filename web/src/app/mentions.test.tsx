import { StrictMode } from "react";
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, it, expect, vi } from "vitest";
import {
  MentionProvider,
  SourceMentionProvider,
  useMentionResolver,
} from "./mentions";
import { NavigationProvider } from "./navigation";
import { MarkdownBody } from "../components/content/MarkdownBody";
const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV";
const identity = {
  userId: "2".repeat(32),
  username: "cedar",
  displayName: "Replacement account",
  avatarMediaId: null,
  coverMediaId: null,
  bio: "",
  profileUrl: "/cedar",
};
const json = (data: unknown) =>
  new Response(
    JSON.stringify({ data, meta: { request_id: "saved-mentions" } }),
    { headers: { "Content-Type": "application/json" } },
  );
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("links a saved mention to its current account instead of a reused text handle", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      json([
        {
          handle: "cedar",
          account: {
            user_id: "1".repeat(32),
            username: "maple",
            display_name: "Original account",
            avatar_media_id: null,
          },
        },
        { handle: "unknown", account: null },
      ]),
    ),
  );
  render(
    <NavigationProvider>
      <MentionProvider
        identities={[identity, { ...identity, username: "unknown" }]}
      >
        <SourceMentionProvider kind="post" id={id} revision="first">
          <MarkdownBody html="<p>Hello @cedar @unknown</p>" />
        </SourceMentionProvider>
      </MentionProvider>
    </NavigationProvider>,
  );
  expect(await screen.findByRole("link", { name: "@cedar" })).toHaveAttribute(
    "href",
    "/maple",
  );
  expect(
    screen.queryByRole("link", { name: "@unknown" }),
  ).not.toBeInTheDocument();
});
it("keeps imported snapshot mentions unlinked and preserves legacy source behavior", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(json([{ handle: "cedar", account: null }]))
    .mockResolvedValueOnce(json([]));
  vi.stubGlobal("fetch", fetcher);
  const view = render(
    <NavigationProvider>
      <MentionProvider identities={[identity]}>
        <SourceMentionProvider kind="post" id={id} revision="snapshot">
          <MarkdownBody html="<p>@cedar</p>" />
        </SourceMentionProvider>
      </MentionProvider>
    </NavigationProvider>,
  );
  await screen.findByText("@cedar");
  await new Promise((r) => setTimeout(r, 0));
  expect(
    screen.queryByRole("link", { name: "@cedar" }),
  ).not.toBeInTheDocument();
  view.rerender(
    <NavigationProvider>
      <MentionProvider identities={[identity]}>
        <SourceMentionProvider kind="post" id={id} revision="legacy">
          <MarkdownBody html="<p>@cedar</p>" />
        </SourceMentionProvider>
      </MentionProvider>
    </NavigationProvider>,
  );
  expect(await screen.findByRole("link", { name: "@cedar" })).toHaveAttribute(
    "href",
    "/cedar",
  );
});

it("survives StrictMode cleanup and resolves the current full profile for hover cards", async () => {
  const fetcher = vi.fn<typeof fetch>(async (input) =>
    String(input).endsWith("/mentions")
      ? json([
          {
            handle: "cedar",
            account: {
              user_id: "1".repeat(32),
              username: "maple",
              display_name: "Original account",
              avatar_media_id: null,
            },
          },
        ])
      : json({
          user_id: "1".repeat(32),
          username: "maple",
          display_name: "Original account",
          bio: "Current biography",
          avatar_media_id: null,
          cover_media_id: null,
          profile_url: "/maple",
        }),
  );
  vi.stubGlobal("fetch", fetcher);
  function Profile() {
    const resolver = useMentionResolver();
    return <output>{resolver.identity("cedar")?.bio}</output>;
  }
  render(
    <StrictMode>
      <NavigationProvider>
        <MentionProvider>
          <SourceMentionProvider kind="post" id={id} revision="strict">
            <MarkdownBody html="<p>@cedar</p>" />
            <Profile />
          </SourceMentionProvider>
        </MentionProvider>
      </NavigationProvider>
    </StrictMode>,
  );
  expect(await screen.findByRole("link", { name: "@cedar" })).toHaveAttribute(
    "href",
    "/maple",
  );
  expect(await screen.findByText("Current biography")).toBeVisible();
  expect(
    fetcher.mock.calls.some((call) => String(call[0]).endsWith("/users/maple")),
  ).toBe(true);
});
