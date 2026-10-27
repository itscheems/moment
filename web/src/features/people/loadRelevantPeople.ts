import {
  PublicApiError,
  type CommentContext,
  type PublicAccount,
  type PublicApi,
  type PublicCommentAuthor,
  type PublicPost,
} from "../../api/public";

type PeopleApi = Pick<PublicApi, "commentContext" | "user">;

/** Resolve only the selected conversation's ancestors and its direct quote. */
export async function loadRelevantPeople(
  api: PeopleApi,
  post: PublicPost,
  context: CommentContext | null,
  signal?: AbortSignal,
): Promise<PublicAccount[]> {
  signal?.throwIfAborted();
  const authors = new Map<string, string>();
  const add = (author: PublicCommentAuthor | undefined) => {
    if (author?.userId && author.username && !authors.has(author.userId))
      authors.set(author.userId, author.username);
  };

  if (context) {
    add(context.comment.author);
    const visited = new Set([context.comment.id]);
    let current = context.comment;
    let target = context.replyTarget;
    try {
      while (true) {
        signal?.throwIfAborted();
        if (!target) {
          // Unaddressed replies belong to their root; a missing explicit
          // target does not establish that same relationship.
          if (
            current.replyTo ||
            !current.parentId ||
            visited.has(current.parentId)
          )
            break;
          target = (await api.commentContext(post.id, current.parentId, signal))
            .comment;
        }
        if (visited.has(target.id)) break;
        visited.add(target.id);
        add(target.author);
        current = target;
        target = current.replyTo
          ? (await api.commentContext(post.id, current.id, signal)).replyTarget
          : null;
      }
    } catch (error) {
      if (!(error instanceof PublicApiError && error.status === 404))
        throw error;
    }
  }
  add(post.author);
  if (!context && post.quotedPost && !post.quotedPost.unavailable)
    add(post.quotedPost.author);

  const profiles = await Promise.all(
    [...authors].map(async ([userId, username]) => {
      try {
        const profile = await api.user(username, signal);
        // A renamed handle may now belong to somebody else.
        return profile.userId === userId ? profile : null;
      } catch (error) {
        if (error instanceof PublicApiError && error.status === 404)
          return null;
        throw error;
      }
    }),
  );
  signal?.throwIfAborted();
  return profiles.filter((profile) => profile !== null);
}
