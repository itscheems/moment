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
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { mediaApi } from "../../api/media";
import type { Account, SiteIdentity } from "../../api/public";
import { FeedbackProvider } from "../../components/Feedback";
import { SessionProvider, useSession } from "../auth/SessionProvider";
import { ProfileEditor } from "./ProfileEditor";
import { makeAccountLimits } from "../../test/accountFixtures";

vi.mock("./ProfileMediaCrop", () => ({
  ProfileMediaCrop: ({
    file,
    onApply,
    onCancel,
  }: {
    file: File;
    onApply: (file: File) => void;
    onCancel: () => void;
  }) => (
    <div role="dialog" aria-label="编辑媒体">
      <button onClick={() => onApply(file)}>应用</button>
      <button onClick={onCancel}>返回</button>
    </div>
  ),
}));

const sessionKey = "moment.auth.session.v2";
const mediaId = "01ARZ3NDEKTSV4RRFFQ69G5FAY";

it("ignores a profile save that finishes after the editor's account was replaced", async () => {
  sessionStorage.clear();
  window.history.replaceState({}, "", "/owner");
  vi.stubGlobal("scrollTo", vi.fn());
  vi.stubGlobal("BroadcastChannel", undefined);
  let switched = false;
  let auth: ReturnType<typeof useSession>;
  let complete!: (response: Response) => void;
  const saved = vi.fn();
  const pending = new Promise<Response>((resolve) => {
    complete = resolve;
  });
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    if (String(input) === "/api/v1/account/profile") return pending;
    const envelope = accountSessionEnvelope();
    if (switched) {
      envelope.data.csrf_token = "csrf-replacement";
      envelope.data.account = {
        ...envelope.data.account,
        user_id: "1".repeat(32),
        username: "replacement",
        role: "member",
        profile_url: "/replacement",
      };
    }
    return json(envelope);
  });
  vi.stubGlobal("fetch", fetcher);
  function Harness() {
    auth = useSession();
    return (
      <>
        <output data-testid="profile-identity">
          {auth.account?.username}|{auth.account?.role}|
          {auth.session?.csrfToken}
        </output>
        {auth.account?.username === "owner" ? (
          <ProfileEditor account={auth.account} onSaved={saved} />
        ) : null}
      </>
    );
  }
  try {
    render(
      <NavigationProvider>
        <FeedbackProvider>
          <SessionProvider>
            <Harness />
          </SessionProvider>
        </FeedbackProvider>
      </NavigationProvider>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "编辑资料" }));
    fireEvent.change(screen.getByLabelText("显示名称"), {
      target: { value: "Changed profile" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() =>
      expect(fetcher).toHaveBeenCalledWith(
        "/api/v1/account/profile",
        expect.objectContaining({ method: "PATCH" }),
      ),
    );
    switched = true;
    await act(async () => {
      await auth.refreshSession();
    });
    expect(screen.getByTestId("profile-identity")).toHaveTextContent(
      "replacement|member|csrf-replacement",
    );
    expect(screen.queryByRole("dialog", { name: "编辑资料" })).toBeNull();
    const destination = window.location.pathname;
    await act(async () => {
      complete(
        json({
          data: {
            ...accountSessionEnvelope().data.account,
            display_name: "Changed profile",
          },
          meta: {},
        }),
      );
    });
    expect(screen.getByTestId("profile-identity")).toHaveTextContent(
      "replacement|member|csrf-replacement",
    );
    expect(saved).not.toHaveBeenCalled();
    expect(screen.queryByText("个人资料已更新")).toBeNull();
    expect(window.location.pathname).toBe(destination);
  } finally {
    cleanup();
    vi.unstubAllGlobals();
    sessionStorage.clear();
  }
});

function accountSessionEnvelope() {
  return {
    data: {
      preferences: { ui_locale: null },
      expires_at: "2999-08-11T01:00:00.000Z",
      csrf_token: "csrf-from-cookie",
      account: {
        user_id: "00000000000000000000000000000000",
        username: "owner",
        display_name: "Moment",
        bio: "",
        avatar_media_id: null,
        cover_media_id: null,
        role: "admin",
        profile_url: "/owner",
        status: "active",
        username_chosen: true,
      },
      limits: makeAccountLimits(),
    },
    meta: { request_id: "req_account_session" },
  };
}

const site: SiteIdentity = {
  profileUserId: "00000000000000000000000000000000",
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

describe("profile editor", () => {
  beforeEach(() => {
    window.sessionStorage.setItem(
      sessionKey,
      JSON.stringify({
        csrf_token: "csrf-profile",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    window.history.replaceState({}, "", "/owner");
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
        if (url === "/api/v1/account/session") {
          return json(accountSessionEnvelope());
        }
        if (url === "/api/v1/status") {
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
        if (url === "/api/v1/account/profile") {
          bodies.push(JSON.parse(String(init?.body)));
          return json({
            data: {
              ...accountSessionEnvelope().data.account,
              display_name: "更新后的 Moment",
              avatar_media_id: null,
              cover_media_id: null,
            },
            meta: { request_id: "profile" },
          });
        }
        return json({}, 404);
      }),
    );
    const onSaved = vi.fn();
    render(
      <NavigationProvider>
        <FeedbackProvider>
          <SessionProvider>
            <ProfileEditor
              onSaved={onSaved}
              account={{
                ...site,
                userId: site.profileUserId,
                profileUrl: `/${site.username}`,
              }}
            />
          </SessionProvider>
        </FeedbackProvider>
      </NavigationProvider>,
    );

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "编辑资料" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "编辑资料" }));
    expect(window.location.pathname).toBe("/settings/profile");
    expect(
      screen.getByRole("dialog", { name: "编辑资料" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "关闭编辑资料" }),
    ).toBeInTheDocument();
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
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
      expect(document.documentElement.style.overflow).toBe(""),
    );
    expect(document.body.style.position).toBe("");
    expect(document.documentElement.style.overflow).toBe("");
    expect(bodies).toEqual([
      {
        display_name: "更新后的 Moment",
        avatar_media_id: null,
        cover_media_id: null,
      },
    ]);
    expect(await screen.findByText("个人资料已更新")).toBeInTheDocument();
  });

  it("keeps icon media controls and discards unsaved changes on reopening", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input) === "/api/v1/account/session") {
          return json(accountSessionEnvelope());
        }
        if (String(input) === "/api/v1/status") {
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
      <NavigationProvider>
        <FeedbackProvider>
          <SessionProvider>
            <ProfileEditor
              onSaved={vi.fn()}
              account={{
                ...site,
                userId: site.profileUserId,
                profileUrl: `/${site.username}`,
              }}
            />
          </SessionProvider>
        </FeedbackProvider>
      </NavigationProvider>,
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

    expect(screen.queryByRole("button", { name: "重置资料更改" })).toBeNull();
    fireEvent.change(screen.getByLabelText("显示名称"), {
      target: { value: "尚未保存的名称" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: /用户名/ }), {
      target: { value: "new_owner" },
    });
    expect(preview).toHaveTextContent("尚未保存的名称");
    expect(preview).toHaveTextContent("@new_owner");
    fireEvent.click(screen.getByRole("button", { name: "关闭编辑资料" }));
    await waitFor(() => expect(window.location.pathname).toBe("/owner"));
    fireEvent.click(screen.getByRole("button", { name: "编辑资料" }));

    expect(screen.getByLabelText("显示名称")).toHaveValue("Moment");
    expect(
      screen.getByRole("region", { name: "个人资料预览" }),
    ).toHaveTextContent("@owner");
    expect(screen.getByRole("img", { name: "封面预览" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "头像预览" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存" })).toBeDisabled();
  });
});

const memberID = "11111111111111111111111111111111";

function memberWire(
  status: "active" | "pending",
  avatarMediaID: string | null = null,
) {
  return {
    user_id: memberID,
    username: "linlin",
    display_name: "林林",
    bio: "在这里记下路过的天气",
    avatar_media_id: avatarMediaID,
    cover_media_id: null,
    role: "member",
    profile_url: "/linlin",
    status,
    username_chosen: true,
  };
}

function memberAccount(
  status: "active" | "pending",
  avatarMediaId: string | null = null,
): Account {
  return {
    userId: memberID,
    username: "linlin",
    displayName: "林林",
    bio: "在这里记下路过的天气",
    avatarMediaId,
    coverMediaId: null,
    role: "member",
    profileUrl: "/linlin",
    status,
    usernameChosen: true,
  };
}

function memberSession(status: "active" | "pending") {
  return {
    data: {
      preferences: { ui_locale: null },
      expires_at: "2999-08-11T01:00:00.000Z",
      csrf_token: "csrf-from-cookie",
      account: memberWire(status),
      limits: makeAccountLimits(),
    },
    meta: { request_id: "req_member_session" },
  };
}

function refusal(code: string, status: number): Response {
  return json(
    {
      error: {
        code,
        message: code,
        details: {},
        request_id: "req_picture",
      },
    },
    status,
  );
}

function png(name = "face.png"): File {
  return new File(["image bytes"], name, { type: "image/png" });
}

describe("profile editor for a member", () => {
  beforeEach(() => {
    window.sessionStorage.setItem(
      sessionKey,
      JSON.stringify({
        csrf_token: "csrf-member",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    window.history.replaceState({}, "", "/owner");
    vi.stubGlobal("scrollTo", vi.fn());
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:local-preview");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    window.sessionStorage.clear();
  });

  async function openEditor(account: Account, onSaved = vi.fn()) {
    render(
      <NavigationProvider>
        <FeedbackProvider>
          <SessionProvider>
            <ProfileEditor account={account} onSaved={onSaved} />
          </SessionProvider>
        </FeedbackProvider>
      </NavigationProvider>,
    );
    const trigger = await screen.findByRole("button", { name: "编辑资料" });
    if (!screen.queryByRole("dialog", { name: "编辑资料" })) {
      fireEvent.click(trigger);
    }
    await screen.findByRole("dialog", { name: "编辑资料" });
    return onSaved;
  }

  function selectAvatar(file = png(), apply = true) {
    const input = document.querySelector<HTMLInputElement>(
      'input[data-profile-media-input="avatar"]',
    )!;
    expect(input).not.toBeNull();
    fireEvent.change(input, { target: { files: [file] } });
    const applyButton = screen.queryByRole("button", { name: "应用" });
    if (apply && applyButton) fireEvent.click(applyButton);
  }

  function selectCover() {
    const input = document.querySelector<HTMLInputElement>(
      'input[data-profile-media-input="cover"]',
    )!;
    fireEvent.change(input, { target: { files: [png("cover.png")] } });
  }

  it("keeps a cover selection private until the profile is saved", async () => {
    const bodies: unknown[] = [];
    const uploaded = {
      id: mediaId,
      byteSize: 100,
      width: 1200,
      height: 400,
      mimeType: "image/png",
      previewMimeType: "image/png",
      animatedOriginal: false,
      previewUrl: mediaPreviewUrl(mediaId),
      originalUrl: `/api/v1/media/${mediaId}/original`,
    };
    const upload = vi
      .spyOn(mediaApi, "uploadMedia")
      .mockResolvedValue(uploaded);
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        calls.push(url);
        if (url === "/api/v1/account/session")
          return json(memberSession("active"));
        if (url === "/api/v1/account/profile") {
          bodies.push(JSON.parse(String(init?.body)));
          return json({
            data: { ...memberWire("active"), cover_media_id: mediaId },
            meta: { request_id: "req_cover" },
          });
        }
        return json({}, 404);
      }),
    );
    const onSaved = await openEditor(memberAccount("active"));
    selectCover();
    expect(
      screen.getByRole("dialog", { name: "编辑媒体" }),
    ).toBeInTheDocument();
    expect(upload).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "返回" }));
    expect(onSaved).not.toHaveBeenCalled();
    expect(screen.queryByRole("img", { name: "封面预览" })).toBeNull();

    selectCover();
    fireEvent.click(screen.getByRole("button", { name: "应用" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "保存" })).toBeEnabled(),
    );
    expect(upload).toHaveBeenCalledTimes(1);
    expect(onSaved).not.toHaveBeenCalled();
    expect(bodies).toEqual([]);
    expect(calls).not.toContain("/api/v1/account/cover");
    expect(screen.getByRole("img", { name: "封面预览" })).toHaveAttribute(
      "src",
      uploaded.previewUrl,
    );

    fireEvent.click(screen.getByRole("button", { name: "关闭编辑资料" }));
    fireEvent.click(screen.getByRole("button", { name: "编辑资料" }));
    expect(screen.queryByRole("img", { name: "封面预览" })).toBeNull();
    expect(screen.getByRole("button", { name: "保存" })).toBeDisabled();
    expect(onSaved).not.toHaveBeenCalled();

    selectCover();
    fireEvent.click(screen.getByRole("button", { name: "应用" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "保存" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(bodies).toEqual([{ cover_media_id: mediaId }]);
    expect(onSaved.mock.calls[0][0]).toMatchObject({ coverMediaId: mediaId });
  });

  it("aborts an unfinished cover preview when the editor closes", async () => {
    let signal: AbortSignal | undefined;
    vi.spyOn(mediaApi, "uploadMedia").mockImplementation(
      (_csrf, _file, _progress, nextSignal) => {
        signal = nextSignal;
        return new Promise(() => undefined);
      },
    );
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) =>
        String(input) === "/api/v1/account/session"
          ? json(memberSession("active"))
          : json({}, 404),
      ),
    );
    const onSaved = await openEditor(memberAccount("active"));
    selectCover();
    fireEvent.click(screen.getByRole("button", { name: "应用" }));
    expect(signal?.aborted).toBe(false);
    expect(screen.getByRole("button", { name: "保存" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "关闭编辑资料" }));
    expect(signal?.aborted).toBe(true);
    expect(onSaved).not.toHaveBeenCalled();
  });

  it("binds an approved member's picture in one request and adopts the answer", async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        calls.push({ url, init });
        if (url === "/api/v1/account/session")
          return json(memberSession("active"));
        if (url === "/api/v1/account/avatar") {
          return json({
            data: memberWire("active", mediaId),
            meta: { request_id: "req_avatar" },
          });
        }
        return json({}, 404);
      }),
    );
    const onSaved = await openEditor(memberAccount("active"));

    // No note stands between an approved member and the control.
    expect(screen.queryByText("账户通过审核后就能上传图片")).toBeNull();
    selectAvatar(png(), false);
    expect(
      screen.getByRole("dialog", { name: "编辑媒体" }),
    ).toBeInTheDocument();
    expect(
      calls.filter((call) => call.url === "/api/v1/account/avatar"),
    ).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "返回" }));
    expect(onSaved).not.toHaveBeenCalled();
    selectAvatar();

    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(onSaved.mock.calls[0][0]).toMatchObject({
      userId: memberID,
      avatarMediaId: mediaId,
      status: "active",
    });
    // Bound, not merely uploaded: the editor shows the stored picture rather
    // than the local blob it was previewing a moment ago, and says so.
    await waitFor(() =>
      expect(screen.getByRole("img", { name: "头像预览" })).toHaveAttribute(
        "src",
        mediaPreviewUrl(mediaId),
      ),
    );
    expect(await screen.findByText("头像已更新")).toBeInTheDocument();
    // Nothing is left for the save button to carry.
    expect(screen.getByRole("button", { name: "保存" })).toBeDisabled();

    const upload = calls.find((call) => call.url === "/api/v1/account/avatar")!;
    expect(upload.init?.method).toBe("POST");
    expect(upload.init?.credentials).toBe("same-origin");
    expect(new Headers(upload.init?.headers).get("X-CSRF-Token")).toBe(
      "csrf-from-cookie",
    );
    // The browser has to write the multipart boundary, so naming the type here
    // would make the body unreadable.
    expect(new Headers(upload.init?.headers).get("Content-Type")).toBeNull();
    const body = upload.init?.body as FormData;
    expect(body).toBeInstanceOf(FormData);
    expect((body.get("file") as File).name).toBe("face.png");
  });

  it("keeps a pending member's controls on screen and says what unlocks them", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) =>
        String(input) === "/api/v1/account/session"
          ? json(memberSession("pending"))
          : json({}, 404),
      ),
    );
    await openEditor(memberAccount("pending", mediaId));

    // Disabled, never absent: a control that disappears reads as a feature
    // this site does not have.
    expect(screen.getByText("账户通过审核后就能上传图片")).toBeInTheDocument();
    const avatarAction = screen.getByRole("button", { name: "编辑头像" });
    expect(avatarAction).toBeInTheDocument();
    fireEvent.click(avatarAction);
    expect(screen.getByRole("menuitem", { name: "更换头像" })).toHaveAttribute(
      "data-disabled",
    );
    // Clearing a picture is a text write, and pending accounts keep those.
    expect(
      screen.getByRole("menuitem", { name: "移除头像" }),
    ).not.toHaveAttribute("data-disabled");
  });

  it.each([
    ["ACCOUNT_NOT_APPROVED", 403, "账户通过审核后就能上传图片"],
    ["MEDIA_TOO_LARGE", 413, "图片不能超过 8 MB"],
    ["MEDIA_TYPE_UNSUPPORTED", 415, "这里只能放图片"],
  ])(
    "renders %s as something a reader can act on",
    async (code, status, message) => {
      vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
          const url = String(input);
          if (url === "/api/v1/account/session") {
            return json(memberSession("active"));
          }
          if (url === "/api/v1/account/avatar") return refusal(code, status);
          return json({}, 404);
        }),
      );
      await openEditor(memberAccount("active"));
      selectAvatar();

      expect(await screen.findByRole("alert")).toHaveTextContent(message);
      // A refusal is not an expired session: the editor stays open and the
      // account keeps the picture it already had.
      expect(
        screen.getByRole("dialog", { name: "编辑资料" }),
      ).toBeInTheDocument();
    },
  );

  it("refuses an oversized picture before spending the upload", async () => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) =>
      String(input) === "/api/v1/account/session"
        ? json(memberSession("active"))
        : json({}, 404),
    );
    vi.stubGlobal("fetch", fetcher);
    await openEditor(memberAccount("active"));
    selectAvatar(
      new File([new Uint8Array(9 * 1024 * 1024)], "huge.png", {
        type: "image/png",
      }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "图片不能超过 8 MB",
    );
    expect(
      fetcher.mock.calls.some(
        (call) => String(call[0]) === "/api/v1/account/avatar",
      ),
    ).toBe(false);
  });

  it("sends a member's text edits to the account route, not the site", async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v1/account/session") {
          return json(memberSession("active"));
        }
        if (url === "/api/v1/account/profile") {
          bodies.push(JSON.parse(String(init?.body)));
          return json({
            data: { ...memberWire("active"), display_name: "林林之外" },
            meta: { request_id: "req_profile" },
          });
        }
        return json({}, 404);
      }),
    );
    const onSaved = await openEditor(memberAccount("active"));

    // The site title belongs to the site, and a member has no site.
    expect(screen.queryByLabelText("站点标题")).toBeNull();
    fireEvent.change(screen.getByLabelText("显示名称"), {
      target: { value: "林林之外" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(bodies).toEqual([{ display_name: "林林之外" }]);
  });
});
