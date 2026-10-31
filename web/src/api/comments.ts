import {
  ApiClient,
  decodeExactSuccess,
  invalidResponse,
  locationPayload,
  mediaPayload,
  type LocationInput,
  type PostMediaInput,
  requestInit,
} from "./client";
import {
  type CreatedComment,
  decodeCommentResponse,
  decodeCreatedComment,
  parseComment,
  parseDeletedCommentMeta,
  type PublicComment,
} from "./public";

export interface DeletedComment {
  comment: PublicComment;
  repliesAffected: number;
}

export interface CommentPin {
  postId: string;
  pinnedCommentId: string | null;
}

export class CommentsApi extends ApiClient {
  async createComment(
    csrfToken: string,
    postId: string,
    contentMd: string,
    parentId: string | null,
    idempotencyKey: string,
    media: PostMediaInput[] = [],
    location: LocationInput | null = null,
    replyToCommentId: string | null = null,
    excludedReplyUserIds: string[] = [],
  ): Promise<CreatedComment> {
    const init = requestInit("POST", csrfToken, {
      ...(excludedReplyUserIds.length
        ? { exclude_reply_user_ids: excludedReplyUserIds }
        : {}),
      content_md: contentMd,
      parent_id: parentId,
      reply_to_comment_id: replyToCommentId,
      media: mediaPayload(media),
      location: location === null ? null : locationPayload(location),
    });
    const headers = new Headers(init.headers);
    headers.set("Idempotency-Key", idempotencyKey);
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(postId)}/comments`,
      { ...init, headers },
    );
    return decodeCreatedComment(response, postId, {
      parentId,
    });
  }

  async deleteComment(
    csrfToken: string,
    postId: string,
    commentId: string,
  ): Promise<DeletedComment> {
    const response = await this.fetcher(
      `/api/v1/comments/${encodeURIComponent(commentId)}`,
      requestInit("DELETE", csrfToken),
    );
    const { data, meta } = await decodeCommentResponse(response, [
      "deleted",
      "dry_run",
      "replies_affected",
    ]);
    if (response.status !== 200) {
      throw invalidResponse("comment deletion must return HTTP 200");
    }
    const comment = parseComment(data, postId);
    const result = parseDeletedCommentMeta(meta);
    if (!result.deleted || result.dryRun || comment.id !== commentId) {
      throw invalidResponse("comment deletion was not confirmed");
    }
    if (comment.parentId !== null && result.repliesAffected !== 0) {
      throw invalidResponse("a reply deletion cannot affect child comments");
    }
    return { comment, repliesAffected: result.repliesAffected };
  }

  async pinComment(
    csrfToken: string,
    postId: string,
    commentId: string,
  ): Promise<CommentPin> {
    return this.commentPinChange(csrfToken, postId, commentId, "PUT");
  }

  async unpinComment(
    csrfToken: string,
    postId: string,
    commentId: string,
  ): Promise<CommentPin> {
    return this.commentPinChange(csrfToken, postId, commentId, "DELETE");
  }

  private async commentPinChange(
    csrfToken: string,
    postId: string,
    commentId: string,
    method: "PUT" | "DELETE",
  ): Promise<CommentPin> {
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(postId)}/comments/${encodeURIComponent(commentId)}/pin`,
      requestInit(method, csrfToken),
    );
    const data = await decodeExactSuccess(response, [
      "post_id",
      "pinned_comment_id",
    ]);
    const responsePostId = data.post_id;
    const pinnedCommentId = data.pinned_comment_id;
    if (
      typeof responsePostId !== "string" ||
      responsePostId !== postId ||
      (pinnedCommentId !== null && typeof pinnedCommentId !== "string") ||
      (method === "PUT" && pinnedCommentId !== commentId) ||
      (method === "DELETE" && pinnedCommentId !== null)
    ) {
      throw invalidResponse("comment pin response is invalid");
    }
    return { postId: responsePostId, pinnedCommentId };
  }
}

export const commentsApi = new CommentsApi();
