import { makeSiteIdentity } from "../../test/accountFixtures";
import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NavigationProvider } from "../../app/navigation";
import { NotificationBadge, notificationBadgeText } from "./NotificationBadge";
import { NotificationProvider } from "./NotificationProvider";
import { NotificationsPage } from "./NotificationsPage";
import { LikeProvider, LikeAction } from "./LikeAction";
import { ReplyRecipients } from "./ReplyRecipients";
const mocks = vi.hoisted(() => ({
  auth: {
    state: "signed-in",
    account: { userId: "1".repeat(32), status: "active" },
    session: { csrfToken: "csrf", version: "session-one" },
    expireIfInvalidSession: vi.fn(),
  },
  live: {
    progress: null as null | {
      newCount: number;
      mentionsNewCount: number;
      throughCursor: string;
    },
    reconnecting: false,
    refresh: vi.fn(),
  },
}));
vi.mock("../auth/SessionProvider", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../auth/SessionProvider")>()),
  useSession: () => mocks.auth,
  useOptionalSession: () => mocks.auth,
}));

const json = (data: unknown) =>
  new Response(
    JSON.stringify({ data, meta: { request_id: "notifications" } }),
    { headers: { "Content-Type": "application/json" } },
  );
class Source {
  static instances: Source[] = [];
  listeners = new Map<string, (event: MessageEvent) => void>();
  close = vi.fn();
  onerror: (() => void) | null = null;
  constructor() {
    Source.instances.push(this);
  }
  addEventListener(name: string, fn: (event: MessageEvent) => void) {
    this.listeners.set(name, fn);
  }
  emit(name: string, data: unknown) {
    this.listeners.get(name)?.(
      new MessageEvent(name, { data: JSON.stringify(data) }),
    );
  }
}
let visibility = "visible";
beforeEach(() => {
  window.history.replaceState({}, "", "/account/notifications");
  Source.instances = [];
  visibility = "visible";
  vi.spyOn(document, "visibilityState", "get").mockImplementation(
    () => visibility as DocumentVisibilityState,
  );
  vi.spyOn(window, "scrollY", "get").mockReturnValue(0);
  mocks.auth.account.userId = "1".repeat(32);
  mocks.auth.expireIfInvalidSession.mockClear();
  vi.stubGlobal("EventSource", Source);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
describe("notification interactions", () => {
  it("uses the measured badge threshold", () => {
    expect([0, 1, 9, 10, 19, 20, 99, 999].map(notificationBadgeText)).toEqual([
      "",
      "1",
      "9",
      "10",
      "19",
      "20+",
      "20+",
      "20+",
    ]);
  });
  it("keeps newer stream progress when an older initial read finishes and isolates accounts", async () => {
    let finish!: (response: Response) => void;
    vi.stubGlobal(
      "fetch",
      vi.fn(
        () =>
          new Promise<Response>((resolve) => {
            finish = resolve;
          }),
      ),
    );
    const view = render(
      <NotificationProvider>
        <NotificationBadge />
      </NotificationProvider>,
    );
    await waitFor(() => expect(Source.instances.length).toBe(1));
    act(() =>
      Source.instances[0].emit("sync", {
        new_count: 20,
        mentions_new_count: 2,
        through_cursor: "latest",
      }),
    );
    expect(screen.getByText("20+")).toBeVisible();
    await act(async () =>
      finish(
        json({ new_count: 0, mentions_new_count: 0, through_cursor: "older" }),
      ),
    );
    expect(screen.getByText("20+")).toBeVisible();
    mocks.auth.account.userId = "2".repeat(32);
    view.rerender(
      <NotificationProvider>
        <NotificationBadge />
      </NotificationProvider>,
    );
    expect(screen.queryByText("20+")).not.toBeInTheDocument();
    expect(Source.instances[0].close).toHaveBeenCalled();
  });
  it("loads in a hidden tab but acknowledges only the returned scope when visible at the top", async () => {
    visibility = "hidden";
    const calls: { url: string; init: RequestInit }[] = [];
    const fetcher = vi.fn(
      async (input: RequestInfo | URL, init: RequestInit = {}) => {
        calls.push({ url: String(input), init });
        if (String(input).endsWith("/seen"))
          return json({
            new_count: 1,
            mentions_new_count: 0,
            through_cursor: "all-progress",
          });
        return json({
          items: [],
          next_cursor: "",
          through_cursor: "mentions-boundary",
          new_count: 2,
          mentions_new_count: 1,
        });
      },
    );
    vi.stubGlobal("fetch", fetcher);
    render(
      <NavigationProvider>
        <NotificationsPage scope="mentions" site={makeSiteIdentity()} />
      </NavigationProvider>,
    );
    await screen.findByText("暂时还没有新消息");
    expect(calls.some((c) => c.init.method === "POST")).toBe(false);
    vi.spyOn(window, "scrollY", "get").mockReturnValue(250);
    visibility = "visible";
    fireEvent(document, new Event("visibilitychange"));
    await act(
      async () => new Promise<void>((r) => requestAnimationFrame(() => r())),
    );
    expect(calls.some((c) => c.init.method === "POST")).toBe(false);
    vi.spyOn(window, "scrollY", "get").mockReturnValue(0);
    fireEvent.scroll(window);
    await waitFor(() =>
      expect(calls.some((c) => c.init.method === "POST")).toBe(true),
    );
    const write = calls.find((c) => c.init.method === "POST")!;
    expect(JSON.parse(String(write.init.body))).toEqual({
      scope: "mentions",
      through_cursor: "mentions-boundary",
    });
  });
  it("keeps the direct reply author required while allowing optional participants to be excluded", async () => {
    const required = "1".repeat(32),
      optional = "2".repeat(32);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        json({
          subject: { type: "comment", id: "01ARZ3NDEKTSV4RRFFQ69G5FAV" },
          participants: [
            {
              user_id: required,
              username: "cedar",
              display_name: "Cedar",
              avatar_media_id: null,
              required: true,
            },
            {
              user_id: optional,
              username: "maple",
              display_name: "Maple",
              avatar_media_id: null,
              required: false,
            },
          ],
        }),
      ),
    );
    const change = vi.fn();
    render(
      <ReplyRecipients
        kind="comment"
        id="01ARZ3NDEKTSV4RRFFQ69G5FAV"
        enabled
        disabled={false}
        fallback="@cedar"
        excluded={[]}
        onChange={change}
      />,
    );
    await screen.findByRole("button", { name: "正在回复 @cedar, @maple" });
    fireEvent.click(
      screen.getByRole("button", { name: "正在回复 @cedar, @maple" }),
    );
    const checkboxes = await screen.findAllByRole("checkbox");
    expect(checkboxes[0]).toBeDisabled();
    fireEvent.click(checkboxes[1]);
    expect(change).toHaveBeenCalledWith([optional]);
  });
});

it("shares batched like state across renderers and rolls back a failed write", async () => {
  const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV";
  let rejectWrite!: (error: Error) => void;
  const fetcher = vi.fn<typeof fetch>((...args) => {
    const init = args[1] ?? {};
    if (init.method === "PUT")
      return new Promise<Response>((_resolve, reject) => {
        rejectWrite = reject;
      });
    return Promise.resolve(
      json({ [id]: { like_count: 2, liked_by_me: false } }),
    );
  });
  vi.stubGlobal("fetch", fetcher);
  render(
    <NavigationProvider>
      <LikeProvider scope="account-one">
        <LikeAction kind="post" id={id} />
        <LikeAction kind="post" id={id} />
      </LikeProvider>
    </NavigationProvider>,
  );
  await waitFor(() =>
    expect(
      screen.getAllByRole("button", { name: "点赞" })[0],
    ).toHaveTextContent("2"),
  );
  expect(fetcher).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getAllByRole("button", { name: "点赞" })[0]);
  await waitFor(() =>
    expect(screen.getAllByRole("button", { name: "取消点赞" })).toHaveLength(2),
  );
  await act(async () => rejectWrite(new Error("connection lost")));
  await waitFor(() =>
    expect(
      screen.getAllByRole("button", { name: "点赞" })[0],
    ).toHaveTextContent("2"),
  );
});

it("reconciles an ambiguous like response with the committed server state", async () => {
  const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV";
  let reads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>((...args) => {
      if (args[1]?.method === "PUT")
        return Promise.reject(new Error("response lost after commit"));
      reads++;
      return Promise.resolve(
        json({
          [id]: { like_count: reads === 1 ? 2 : 3, liked_by_me: reads > 1 },
        }),
      );
    }),
  );
  render(
    <NavigationProvider>
      <LikeProvider scope="ambiguous">
        <LikeAction kind="post" id={id} />
      </LikeProvider>
    </NavigationProvider>,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "点赞" })).toHaveTextContent("2"),
  );
  fireEvent.click(screen.getByRole("button", { name: "点赞" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "取消点赞" })).toHaveTextContent(
      "3",
    ),
  );
  expect(reads).toBe(2);
});

it("reads the current source before acknowledging a content notification", async () => {
  const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV";
  const event = {
    id,
    event_type: "post.created",
    subject: { type: "post", id },
    post_id: id,
    post_kind: "moment",
    reply_to_comment_id: null,
    reasons: ["mention"],
    occurred_at: "2026-10-31T01:00:00Z",
    actor: {
      user_id: "1".repeat(32),
      username: "cedar",
      display_name: "Cedar",
      avatar_media_id: null,
      profile_url: "/cedar",
    },
    seen: false,
    available: true,
    excerpt: "List preview",
    post: null,
    comment: null,
  };
  const post = {
    id,
    kind: "moment",
    content_md: "Current body",
    article: null,
    content_html: "<p>Current body</p>",
    created_at: event.occurred_at,
    updated_at: event.occurred_at,
    published_at: event.occurred_at,
    reposted_at: null,
    edited_at: null,
    visibility: "everyone",
    timeline_item_kind: "original",
    quoted_post: null,
    comment_count: 0,
    media: [],
    map_card: null,
    location: null,
    link_cards: [],
  };
  let resolveBody!: (value: Response) => void;
  const writes: RequestInit[] = [];
  vi.stubGlobal("IntersectionObserver", undefined);
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>((...args) => {
      const url = String(args[0]),
        init = args[1] ?? {};
      if (url.endsWith("/" + id))
        return new Promise((resolve) => {
          resolveBody = resolve;
        });
      if (url.endsWith("/seen")) {
        writes.push(init);
        return Promise.resolve(
          json({ new_count: 0, mentions_new_count: 0, through_cursor: "all" }),
        );
      }
      return Promise.resolve(
        json({
          items: [event],
          next_cursor: "",
          through_cursor: "body-boundary",
          new_count: 1,
          mentions_new_count: 1,
        }),
      );
    }),
  );
  render(
    <NavigationProvider>
      <NotificationsPage scope="mentions" site={makeSiteIdentity()} />
    </NavigationProvider>,
  );
  await waitFor(() => expect(resolveBody).toBeTypeOf("function"));
  expect(writes).toHaveLength(0);
  await act(async () => resolveBody(json({ ...event, post })));
  expect(await screen.findByText("Current body")).toBeVisible();
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(JSON.parse(String(writes[0].body)).through_cursor).toBe(
    "body-boundary",
  );
});
