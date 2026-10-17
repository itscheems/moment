import type { PublicPost } from "../../api/public";

export interface PostChangeEvent {
  kind: "created" | "updated" | "deleted" | "reordered";
  post: PublicPost;
}

const listeners = new Set<(event: PostChangeEvent) => void>();
let pending: PostChangeEvent[] = [];

export function announcePostChanged(detail: PostChangeEvent): void {
  if (
    detail.kind === "created" &&
    typeof window.BroadcastChannel === "function"
  ) {
    try {
      const channel = new window.BroadcastChannel("moment.posts.v1");
      try {
        channel.postMessage({
          scope: window.location.origin,
          id: detail.post.id,
          kind: detail.post.kind,
        });
      } finally {
        channel.close();
      }
    } catch {
      // Optional cross-tab delivery must not turn a successful publish into
      // a failure. Focus and filter changes still check the server watermark.
    }
  }
  if (listeners.size === 0) {
    pending.push(detail);
    return;
  }
  for (const listener of listeners) listener(detail);
}

export function listenForPostChanges(
  listener: (event: PostChangeEvent) => void,
): () => void {
  listeners.add(listener);
  if (pending.length > 0) {
    const queued = pending;
    pending = [];
    for (const event of queued) listener(event);
  }
  return () => listeners.delete(listener);
}

/** Cross-tab invalidation contains no body or credentials; receivers fetch as themselves. */
export interface PublishedPostNotice {
  id: string;
  kind: "moment" | "article";
  authorId?: string;
  action?: "draft" | "published" | "unpublished" | "deleted";
}
export function listenForPublishedPosts(
  listener: (notice?: PublishedPostNotice) => void,
): () => void {
  if (typeof window.BroadcastChannel !== "function") return () => {};
  let channel: BroadcastChannel;
  try {
    channel = new window.BroadcastChannel("moment.posts.v1");
  } catch {
    return () => {};
  }
  channel.onmessage = (event: MessageEvent<unknown>) => {
    const data = event.data;
    if (typeof data !== "object" || data === null) return;
    const message = data as Record<string, unknown>;
    if (
      message.scope === window.location.origin &&
      typeof message.id === "string" &&
      (message.kind === "moment" || message.kind === "article")
    )
      listener(message as unknown as PublishedPostNotice);
  };
  return () => channel.close();
}
