import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  type PublicComment,
  type PublicCommentAuthor,
  type SiteIdentity,
} from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { CommentItem } from "./CommentItem";

const postId = "01K2ABCDEF0123456789ABCDE0";
const rootId = "01K2ABCDEF0123456789ABCDE1";
const ownerId = "11d003f31a23262ea0f6cbe9857981a1";
const strangerId = "22e114g42b34373fb1g7dcfa968a92b2";

const site: SiteIdentity = {
  ownerId,
  username: "itscheems",
  // Deliberately different from the author projection below. Reading the site
  // for an owner comment is the bug being removed, so the two have to disagree
  // for any of these tests to prove anything.
  displayName: "Moment 站点",
  title: "乔乔的 Moment",
  bio: "沿着日常留下坐标",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: "01M08A31N0WNK66SC39ZAQFK6M",
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};

const ownerAuthor: PublicCommentAuthor = {
  userId: ownerId,
  username: "itscheems",
  displayName: "Cheems",
  avatarMediaId: "01M08A31N0WNK66SC39ZAQFK6M",
  profileUrl: "/@itscheems",
};

const guestAuthor: PublicCommentAuthor = {
  userId: null,
  username: null,
  displayName: "路过的人",
  avatarMediaId: null,
  profileUrl: null,
};

const strangerAuthor: PublicCommentAuthor = {
  userId: strangerId,
  username: "someone",
  displayName: "别人",
  avatarMediaId: "01M08A31N0WNK66SC39ZAQFK6N",
  profileUrl: "/@someone",
};

function comment(
  id: string,
  content: string,
  options: Partial<PublicComment> = {},
): PublicComment {
  const parentId = options.parentId ?? null;
  return {
    id,
    postId,
    parentId,
    depth: parentId === null ? 0 : 1,
    authorKind: "owner",
    authorName: "Owner",
    author: ownerAuthor,
    replyTo: null,
    contentMd: content,
    contentHtml: content ? `<p>${content}</p>\n` : "",
    createdAt: "2026-08-10T01:03:00Z",
    media: [],
    location: null,
    replyCount: 0,
    repliesNextCursor: null,
    ...options,
  };
}

/**
 * jsdom does no layout, so `scrollHeight` and `clientHeight` are both 0 there
 * and the component would never see an overflow. These stubs let a test say how
 * tall a given body box is; everything downstream of the measurement — the
 * control, the aria wiring, the focus handling — is then exercised for real.
 * The rendered-height judgement itself was verified in Chrome against the real
 * corpus, which is the only place it can be.
 */
const measured = new Map<string, { scroll: number; client: number }>();

function stubLayout() {
  for (const property of ["scrollHeight", "clientHeight"] as const) {
    Object.defineProperty(HTMLElement.prototype, property, {
      configurable: true,
      get(this: HTMLElement) {
        const entry = measured.get(this.id);
        if (!entry) return 0;
        return property === "scrollHeight" ? entry.scroll : entry.client;
      },
    });
  }
}

class FakeResizeObserver implements ResizeObserver {
  static instances = new Set<FakeResizeObserver>();

  constructor(private readonly callback: ResizeObserverCallback) {
    FakeResizeObserver.instances.add(this);
  }

  observe() {}

  unobserve() {}

  disconnect() {
    FakeResizeObserver.instances.delete(this);
  }

  static notify() {
    for (const observer of FakeResizeObserver.instances) {
      observer.callback([], observer);
    }
  }
}

function renderItem(
  target: PublicComment,
  others: PublicComment[] = [],
  overrides: {
    owner?: boolean;
    pinned?: boolean;
    replyRoot?: PublicComment;
  } = {},
) {
  const focusTarget = createRef<HTMLElement>();
  return render(
    <NavigationProvider>
      <ol>
        {[target, ...others].map((current) => (
          <CommentItem
            comment={current}
            focusTarget={focusTarget}
            key={current.id}
            onDeleted={() => {}}
            onPinChange={() => {}}
            onReply={() => {}}
            owner={overrides.owner ?? false}
            pinned={overrides.pinned ?? false}
            replyCount={0}
            replyRoot={overrides.replyRoot ?? null}
            site={site}
          />
        ))}
      </ol>
    </NavigationProvider>,
  );
}

beforeEach(() => {
  measured.clear();
  FakeResizeObserver.instances.clear();
  stubLayout();
  vi.stubGlobal("ResizeObserver", FakeResizeObserver);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  for (const property of ["scrollHeight", "clientHeight"] as const) {
    Reflect.deleteProperty(HTMLElement.prototype, property);
  }
});

describe("comment identity", () => {
  it("signs a comment with the resolved author rather than the site identity", () => {
    renderItem(comment(rootId, "顶级回应"));

    const name = screen.getByRole("link", { name: "Cheems" });
    const handle = screen.getByRole("link", { name: "@itscheems" });
    expect(name).toHaveAttribute("href", "/@itscheems");
    expect(handle).toHaveAttribute("href", "/@itscheems");
    expect(screen.queryByText("Moment 站点")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Cheems的回复")).toBeInTheDocument();
    expect(screen.getByText("所有者")).toBeInTheDocument();
  });

  it("renders a legacy guest row as plain text with no profile and no badge", () => {
    renderItem(
      comment(rootId, "很久以前的留言", {
        authorKind: "guest",
        authorName: "路过的人",
        author: guestAuthor,
      }),
    );

    expect(screen.getByText("路过的人")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.queryByText("所有者")).not.toBeInTheDocument();
    expect(screen.queryByText(/^@/)).not.toBeInTheDocument();
  });

  it("decides the badge on the account id, not on author_kind", () => {
    // Same verdict as the retired axis today, and a different one the moment a
    // second account exists — which is the whole reason for moving it.
    const { unmount } = renderItem(
      comment(rootId, "所有者写的", { authorKind: "guest" }),
    );
    expect(screen.getByText("所有者")).toBeInTheDocument();
    unmount();

    renderItem(
      comment(rootId, "别人写的", {
        authorKind: "owner",
        author: strangerAuthor,
      }),
    );
    expect(screen.queryByText("所有者")).not.toBeInTheDocument();
  });

  it("does not lend the site identity to an account that is not the owner", () => {
    renderItem(comment(rootId, "别人写的", { author: strangerAuthor }));

    // Linked, because the account has a profile...
    expect(screen.getByRole("link", { name: "别人" })).toHaveAttribute(
      "href",
      "/@someone",
    );
    expect(screen.getByRole("link", { name: "@someone" })).toBeInTheDocument();
    // ...but nothing of this site's is borrowed for it: no avatar image, and no
    // preview card trigger, because both would show the owner's own material.
    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByText("别")).toBeInTheDocument();
  });
});
