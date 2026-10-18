import { afterEach, expect, it, vi } from "vitest";
import type { PublicPost } from "../../api/public";
import {
  announcePostChanged,
  listenForPostChanges,
  listenForPublishedPosts,
} from "./postEvents";
afterEach(() => vi.unstubAllGlobals());
it("retains local publication and focus refresh when browser channels are blocked", () => {
  vi.stubGlobal(
    "BroadcastChannel",
    class {
      constructor() {
        throw new DOMException("Unavailable", "SecurityError");
      }
    },
  );
  const receive = vi.fn();
  const unsubscribe = listenForPostChanges(receive);
  const event = {
    kind: "created" as const,
    post: { id: "id", kind: "article" } as PublicPost,
  };
  expect(() => announcePostChanged(event)).not.toThrow();
  expect(receive).toHaveBeenCalledWith(event);
  expect(() => listenForPublishedPosts(() => {})()).not.toThrow();
  unsubscribe();
});
it("broadcasts only a scoped publication hint and closes the channel", () => {
  const send = vi.fn(),
    close = vi.fn();
  vi.stubGlobal(
    "BroadcastChannel",
    class {
      postMessage = send;
      close = close;
    },
  );
  const unsubscribe = listenForPostChanges(() => {});
  announcePostChanged({
    kind: "created",
    post: {
      id: "id",
      kind: "article",
      contentHtml: "private body",
    } as PublicPost,
  });
  expect(send).toHaveBeenCalledWith({
    scope: window.location.origin,
    id: "id",
    kind: "article",
  });
  expect(close).toHaveBeenCalledOnce();
  unsubscribe();
});
it("ignores foreign and malformed messages and removes its listener on cleanup", () => {
  const receivers: { onmessage: ((event: MessageEvent) => void) | null }[] = [];
  const close = vi.fn();
  vi.stubGlobal(
    "BroadcastChannel",
    class {
      onmessage = null;
      close = close;
      constructor() {
        receivers.push(this);
      }
    },
  );
  const receive = vi.fn(),
    unsubscribe = listenForPublishedPosts(receive);
  const receiver = receivers[0];
  receiver.onmessage?.(
    new MessageEvent("message", {
      data: { scope: "foreign", id: "id", kind: "article" },
    }),
  );
  receiver.onmessage?.(new MessageEvent("message", { data: null }));
  expect(receive).not.toHaveBeenCalled();
  receiver.onmessage?.(
    new MessageEvent("message", {
      data: { scope: window.location.origin, id: "id", kind: "article" },
    }),
  );
  expect(receive).toHaveBeenCalledOnce();
  unsubscribe();
  expect(close).toHaveBeenCalledOnce();
});
