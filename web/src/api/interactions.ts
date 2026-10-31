import {
  ApiClient,
  booleanField,
  decodeExactSuccess,
  hasExactKeys,
  invalidResponse,
  nonNegativeIntegerField,
  objectData,
  requestInit,
  stringField,
} from "./client";
import {
  decodeResponse,
  parsePost,
  parseComment,
  type PublicPost,
  type PublicComment,
} from "./public";

export type SubjectKind = "post" | "comment";
export type NotificationScope = "all" | "mentions";
export interface LikeState {
  count: number;
  liked: boolean;
}
export interface NotificationState {
  newCount: number;
  mentionsNewCount: number;
  throughCursor: string;
}
export interface NotificationActor {
  userId: string | null;
  username: string | null;
  displayName: string;
  avatarMediaId: string | null;
  profileUrl: string | null;
}
export interface InboxItem {
  id: string;
  eventType: string;
  subject: { kind: SubjectKind; id: string };
  postId: string;
  postKind: "moment" | "article";
  replyToCommentId: string | null;
  reasons: string[];
  occurredAt: string;
  actor: NotificationActor;
  seen: boolean;
  available: boolean;
  excerpt: string;
  post: PublicPost | null;
  comment: PublicComment | null;
}
export interface InboxPage extends NotificationState {
  items: InboxItem[];
  nextCursor: string;
}
export interface ReplyParticipant {
  userId: string;
  username: string;
  displayName: string;
  avatarMediaId: string | null;
  required: boolean;
}
export interface ReplyAddressing {
  subject: { kind: SubjectKind; id: string };
  participants: ReplyParticipant[];
}
const ID = /^[0-7][0-9A-HJKMNP-TV-Z]{25}$/;
function id(value: unknown): string {
  if (typeof value !== "string" || !ID.test(value))
    throw invalidResponse("invalid content identifier");
  return value;
}
function nullableString(value: unknown): string | null {
  if (value !== null && typeof value !== "string")
    throw invalidResponse("invalid nullable text");
  return value;
}
function subject(value: unknown): { kind: SubjectKind; id: string } {
  const data = objectData(value);
  if (
    !hasExactKeys(data, ["type", "id"]) ||
    (data.type !== "post" && data.type !== "comment")
  )
    throw invalidResponse("invalid event subject");
  return { kind: data.type, id: id(data.id) };
}
export function parseLikeState(value: unknown): LikeState {
  const data = objectData(value);
  if (!hasExactKeys(data, ["like_count", "liked_by_me"]))
    throw invalidResponse("invalid like state");
  return {
    count: nonNegativeIntegerField(data, "like_count"),
    liked: booleanField(data, "liked_by_me"),
  };
}
export function parseNotificationState(value: unknown): NotificationState {
  const data = objectData(value);
  const result = {
    newCount: nonNegativeIntegerField(data, "new_count"),
    mentionsNewCount: nonNegativeIntegerField(data, "mentions_new_count"),
    throughCursor: stringField(data, "through_cursor"),
  };
  if (
    !result.throughCursor ||
    result.throughCursor.length > 4096 ||
    result.mentionsNewCount > result.newCount
  )
    throw invalidResponse("invalid inbox progress");
  return result;
}
export function parseInboxItem(value: unknown): InboxItem {
  const data = objectData(value);
  if (
    !hasExactKeys(data, [
      "id",
      "event_type",
      "subject",
      "post_id",
      "post_kind",
      "reply_to_comment_id",
      "reasons",
      "occurred_at",
      "actor",
      "seen",
      "available",
      "excerpt",
      "post",
      "comment",
    ])
  )
    throw invalidResponse("invalid notification fields");
  const actor = objectData(data.actor);
  if (
    !hasExactKeys(actor, [
      "user_id",
      "username",
      "display_name",
      "avatar_media_id",
      "profile_url",
    ])
  )
    throw invalidResponse("invalid notification actor");
  const target = subject(data.subject),
    postId = id(data.post_id),
    available = booleanField(data, "available");
  const reasons = data.reasons;
  if (
    !Array.isArray(reasons) ||
    reasons.length < 1 ||
    !reasons.every((v) =>
      ["like", "mention", "reply", "conversation"].includes(v),
    )
  )
    throw invalidResponse("invalid notification reasons");
  const eventType = stringField(data, "event_type");
  if (
    ![
      "post.created",
      "post.updated",
      "post.published",
      "comment.created",
      "post.liked",
      "comment.liked",
    ].includes(eventType)
  )
    throw invalidResponse("invalid event type");
  const occurredAt = stringField(data, "occurred_at");
  if (!Number.isFinite(Date.parse(occurredAt)))
    throw invalidResponse("invalid event time");
  if (data.post_kind !== "moment" && data.post_kind !== "article")
    throw invalidResponse("invalid notification post kind");
  const result: InboxItem = {
    id: id(data.id),
    eventType,
    subject: target,
    postId,
    postKind: data.post_kind,
    replyToCommentId:
      data.reply_to_comment_id === null ? null : id(data.reply_to_comment_id),
    reasons,
    occurredAt,
    actor: {
      userId: nullableString(actor.user_id),
      username: nullableString(actor.username),
      displayName: stringField(actor, "display_name"),
      avatarMediaId: nullableString(actor.avatar_media_id),
      profileUrl: nullableString(actor.profile_url),
    },
    seen: booleanField(data, "seen"),
    available,
    excerpt: stringField(data, "excerpt"),
    post: data.post === null ? null : parsePost(data.post),
    comment: data.comment === null ? null : parseComment(data.comment, postId),
  };
  if (
    !available &&
    (result.excerpt !== "" ||
      result.post !== null ||
      result.comment !== null ||
      result.replyToCommentId !== null)
  )
    throw invalidResponse(
      "unavailable notification contains protected content",
    );
  if (
    (result.post && result.post.id !== postId) ||
    (result.comment && result.comment.id !== target.id)
  )
    throw invalidResponse("notification source mismatch");
  if (
    result.actor.profileUrl !== null &&
    (!result.actor.userId ||
      !result.actor.username ||
      result.actor.profileUrl !== "/" + result.actor.username)
  )
    throw invalidResponse("invalid linked actor");
  return result;
}
export class InteractionsApi extends ApiClient {
  async likes(
    kind: SubjectKind,
    ids: string[],
    signal?: AbortSignal,
  ): Promise<Record<string, LikeState>> {
    const data = await decodeExactSuccessMap(
      await this.fetcher(
        `/api/v1/likes?${new URLSearchParams({ kind, ids: ids.join(",") })}`,
        { ...requestInit("GET"), signal },
      ),
    );
    return Object.fromEntries(
      Object.entries(data).map(([key, value]) => {
        if (!ids.includes(key))
          throw invalidResponse("unrequested like target");
        return [key, parseLikeState(value)];
      }),
    );
  }
  async setLike(
    csrf: string,
    kind: SubjectKind,
    id: string,
    active: boolean,
  ): Promise<LikeState> {
    return parseLikeState(
      await decodeExactSuccess(
        await this.fetcher(
          `/api/v1/${kind === "post" ? "posts" : "comments"}/${encodeURIComponent(id)}/like`,
          requestInit(active ? "PUT" : "DELETE", csrf),
        ),
        ["like_count", "liked_by_me"],
      ),
    );
  }
  async state(signal?: AbortSignal): Promise<NotificationState> {
    return parseNotificationState(
      await decodeExactSuccess(
        await this.fetcher("/api/v1/account/notifications/state", {
          ...requestInit("GET"),
          signal,
        }),
        ["new_count", "mentions_new_count", "through_cursor"],
      ),
    );
  }
  async list(
    scope: NotificationScope,
    cursor = "",
    signal?: AbortSignal,
  ): Promise<InboxPage> {
    const query = new URLSearchParams({ scope });
    if (cursor) query.set("cursor", cursor);
    const data = await decodeExactSuccess(
      await this.fetcher(`/api/v1/account/notifications?${query}`, {
        ...requestInit("GET"),
        signal,
      }),
      [
        "items",
        "next_cursor",
        "through_cursor",
        "new_count",
        "mentions_new_count",
      ],
    );
    if (!Array.isArray(data.items))
      throw invalidResponse("invalid notification page");
    return {
      ...parseNotificationState(data),
      items: data.items.map(parseInboxItem),
      nextCursor: stringField(data, "next_cursor"),
    };
  }
  async get(id: string, signal?: AbortSignal): Promise<InboxItem> {
    return parseInboxItem(
      await decodeExactSuccessMap(
        await this.fetcher(
          `/api/v1/account/notifications/${encodeURIComponent(id)}`,
          { ...requestInit("GET"), signal },
        ),
      ),
    );
  }
  async seen(
    csrf: string,
    scope: NotificationScope,
    throughCursor: string,
    signal?: AbortSignal,
  ): Promise<NotificationState> {
    return parseNotificationState(
      await decodeExactSuccess(
        await this.fetcher("/api/v1/account/notifications/seen", {
          ...requestInit("POST", csrf, {
            scope,
            through_cursor: throughCursor,
          }),
          signal,
        }),
        ["new_count", "mentions_new_count", "through_cursor"],
      ),
    );
  }
  async boundMentions(
    kind: SubjectKind,
    id: string,
    signal?: AbortSignal,
  ): Promise<
    { handle: string; account: import("./public").PublicAccount | null }[]
  > {
    const response = await this.fetcher(
      `/api/v1/${kind === "post" ? "posts" : "comments"}/${encodeURIComponent(id)}/mentions`,
      { ...requestInit("GET"), signal },
    );
    const result = await decodeResponse(response, {
      strictSuccessEnvelope: response.ok,
      requireJsonSuccess: response.ok,
    });
    if (response.status !== 200 || !Array.isArray(result.data))
      throw invalidResponse("invalid saved mentions");
    return result.data.map((value) => {
      const row = objectData(value);
      if (!hasExactKeys(row, ["handle", "account"]))
        throw invalidResponse("invalid saved mention");
      const handle = stringField(row, "handle");
      if (!/^[a-z][a-z0-9_]{2,29}$/.test(handle))
        throw invalidResponse("invalid mention handle");
      if (row.account === null) return { handle, account: null };
      const a = objectData(row.account);
      if (
        !hasExactKeys(a, [
          "user_id",
          "username",
          "display_name",
          "avatar_media_id",
        ])
      )
        throw invalidResponse("invalid saved mention identity");
      const userId = stringField(a, "user_id"),
        username = stringField(a, "username");
      if (
        !/^[0-9a-f]{32}$/.test(userId) ||
        !/^[a-z][a-z0-9_]{2,29}$/.test(username)
      )
        throw invalidResponse("invalid saved mention identity");
      return {
        handle,
        account: {
          userId,
          username,
          displayName: stringField(a, "display_name"),
          avatarMediaId: nullableString(a.avatar_media_id),
          coverMediaId: null,
          bio: "",
          profileUrl: "/" + username,
        },
      };
    });
  }
  async replyContext(
    kind: SubjectKind,
    id: string,
    signal?: AbortSignal,
  ): Promise<ReplyAddressing> {
    const data = await decodeExactSuccess(
      await this.fetcher(
        `/api/v1/${kind === "post" ? "posts" : "comments"}/${encodeURIComponent(id)}/reply-context`,
        { ...requestInit("GET"), signal },
      ),
      ["subject", "participants"],
    );
    if (!Array.isArray(data.participants))
      throw invalidResponse("invalid reply participants");
    return {
      subject: subject(data.subject),
      participants: data.participants.map((v) => {
        const p = objectData(v);
        return {
          userId: stringField(p, "user_id"),
          username: stringField(p, "username"),
          displayName: stringField(p, "display_name"),
          avatarMediaId: nullableString(p.avatar_media_id),
          required: booleanField(p, "required"),
        };
      }),
    };
  }
}
async function decodeExactSuccessMap(
  response: Response,
): Promise<Record<string, unknown>> {
  const result = await decodeResponse(response, {
    strictSuccessEnvelope: response.ok,
    requireJsonSuccess: response.ok,
  });
  if (response.status !== 200)
    throw invalidResponse("interaction API success must be HTTP 200");
  return objectData(result.data);
}
export const interactionsApi = new InteractionsApi();
