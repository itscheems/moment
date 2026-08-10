import type { PublicPost } from "../../api/public";

export interface OwnerPostEvent {
  kind: "created" | "updated" | "deleted";
  post: PublicPost;
}

const listeners = new Set<(event: OwnerPostEvent) => void>();
let pending: OwnerPostEvent[] = [];

export function announceOwnerPost(detail: OwnerPostEvent): void {
  if (listeners.size === 0) {
    pending.push(detail);
    return;
  }
  for (const listener of listeners) listener(detail);
}

export function listenForOwnerPosts(
  listener: (event: OwnerPostEvent) => void,
): () => void {
  listeners.add(listener);
  if (pending.length > 0) {
    const queued = pending;
    pending = [];
    for (const event of queued) listener(event);
  }
  return () => listeners.delete(listener);
}
