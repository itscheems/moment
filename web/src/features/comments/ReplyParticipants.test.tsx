import "@testing-library/jest-dom/vitest";
import type { ReactElement } from "react";
import {
  act,
  cleanup,
  fireEvent,
  render as renderUI,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ReplyParticipants } from "./ReplyParticipants";
import { MentionProvider } from "../../app/mentions";
import { NavigationProvider } from "../../app/navigation";
import i18n from "../../i18n";

const postId = "01ARZ3NDEKTSV4RRFFQ69G5FAV";
const commentId = "01ARZ3NDEKTSV4RRFFQ69G5FAW";
function render(ui: ReactElement) {
  return renderUI(ui, { wrapper: NavigationProvider });
}
function response(type: "post" | "comment", id: string, names: string[]) {
  return new Response(
    JSON.stringify({
      data: {
        subject: { type, id },
        participants: names.map((username, index) => ({
          user_id: String(index + 1).repeat(32),
          username,
          display_name: username,
          avatar_media_id: null,
          required: index === 0,
        })),
      },
      meta: { request_id: "reply-context" },
    }),
    { headers: { "Content-Type": "application/json" } },
  );
}
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("shows all server-selected participants outside the reply body without recipient controls", async () => {
  const fetcher = vi.fn(async () =>
    response("post", postId, ["cedar", "maple"]),
  );
  vi.stubGlobal("fetch", fetcher);
  const view = render(
    <ReplyParticipants
      kind="post"
      id={postId}
      enabled={false}
      fallback="@cedar"
    />,
  );
  expect(fetcher).not.toHaveBeenCalled();
  view.rerender(
    <ReplyParticipants kind="post" id={postId} enabled fallback="@cedar" />,
  );
  await screen.findByText(/@maple/);
  expect(view.container).toHaveTextContent("正在回复 @cedar和@maple");
  expect(fetcher).toHaveBeenCalledWith(
    `/api/v1/posts/${postId}/reply-context`,
    expect.objectContaining({ method: "GET" }),
  );
  expect(screen.queryByRole("button")).not.toBeInTheDocument();
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});

it("uses the actual comment target and ignores a late response from the previous target", async () => {
  let finishPost!: (value: Response) => void;
  const fetcher = vi.fn((input: RequestInfo | URL) =>
    String(input).includes("/posts/")
      ? new Promise<Response>((resolve) => {
          finishPost = resolve;
        })
      : Promise.resolve(response("comment", commentId, ["birch", "willow"])),
  );
  vi.stubGlobal("fetch", fetcher);
  const view = render(
    <ReplyParticipants
      key={postId}
      kind="post"
      id={postId}
      enabled
      fallback="@cedar"
    />,
  );
  view.rerender(
    <ReplyParticipants
      key={commentId}
      kind="comment"
      id={commentId}
      enabled
      fallback="@birch"
    />,
  );
  await screen.findByText(/@willow/);
  await act(async () => {
    finishPost(response("post", postId, ["cedar", "maple"]));
  });
  expect(view.container).not.toHaveTextContent("@cedar");
  expect(view.container).not.toHaveTextContent("@maple");
  expect(fetcher).toHaveBeenLastCalledWith(
    `/api/v1/comments/${commentId}/reply-context`,
    expect.anything(),
  );
});

it("reports a failed read and allows retry without displaying a partial list as complete", async () => {
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new TypeError("offline"))
    .mockResolvedValueOnce(response("post", postId, ["cedar", "maple"]));
  vi.stubGlobal("fetch", fetcher);
  render(
    <ReplyParticipants kind="post" id={postId} enabled fallback="@cedar" />,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "收件人暂时无法加载",
  );
  expect(screen.queryByText("@cedar")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "重试" }));
  await screen.findByText(/@maple/);
  await waitFor(() =>
    expect(screen.queryByRole("alert")).not.toBeInTheDocument(),
  );
});

it.each([
  ["en", ["cedar"], "@cedar"],
  ["en", ["cedar", "maple"], "@cedar and @maple"],
  ["en", ["cedar", "maple", "birch"], "@cedar @maple and @birch"],
  ["en", ["cedar", "maple", "birch", "willow"], "@cedar @maple and 2 others"],
  [
    "en",
    ["cedar", "maple", "birch", "willow", "elm", "oak"],
    "@cedar @maple and 4 others",
  ],
  ["zh-CN", ["cedar", "maple", "birch", "willow"], "@cedar @maple 和其他 2 人"],
] satisfies [string, string[], string][])(
  "formats the X reply summary in %s for %j",
  async (locale, names, summary) => {
    await i18n.changeLanguage(locale);
    const fetcher = vi.fn(async () => response("post", postId, names));
    vi.stubGlobal("fetch", fetcher);
    const view = render(
      <ReplyParticipants kind="post" id={postId} enabled fallback="@cedar" />,
    );
    await waitFor(() => expect(view.container).toHaveTextContent(summary));
    const visibleNames = names.slice(0, names.length > 3 ? 2 : 3);
    for (const username of visibleNames) {
      expect(
        screen.getByRole("link", { name: "@" + username }),
      ).toHaveAttribute("href", "/" + username);
    }
    expect(screen.getAllByRole("link")).toHaveLength(visibleNames.length);
    if (names.length > 3) {
      expect(view.container.textContent).not.toContain("@birch");
      expect(
        screen.getByTitle(names.map((name) => "@" + name).join(", ")),
      ).toBeInTheDocument();
    }
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(fetcher.mock.calls).toHaveLength(1);
  },
);

it("opens the existing full-profile preview from a reply participant", async () => {
  const fetcher = vi.fn<typeof fetch>(async (input) =>
    String(input).endsWith("/reply-context")
      ? response("post", postId, ["cedar"])
      : new Response(
          JSON.stringify({
            data: {
              user_id: "1".repeat(32),
              username: "cedar",
              display_name: "Cedar",
              bio: "A current account biography",
              avatar_media_id: null,
              cover_media_id: null,
              profile_url: "/cedar",
            },
            meta: { request_id: "profile-preview" },
          }),
          { headers: { "Content-Type": "application/json" } },
        ),
  );
  vi.stubGlobal("fetch", fetcher);
  await act(async () => {
    render(
      <MentionProvider>
        <ReplyParticipants kind="post" id={postId} enabled fallback="@cedar" />
      </MentionProvider>,
    );
  });
  const link = await screen.findByRole("link", { name: "@cedar" });
  fireEvent.mouseEnter(link);
  await waitFor(() =>
    expect(screen.getByText("A current account biography")).toBeVisible(),
  );
  expect(screen.getByRole("link", { name: "Cedar" })).toHaveAttribute(
    "href",
    "/cedar",
  );
  expect(link).not.toHaveAttribute("title");
});
