import {
  ApiClient,
  decodeExactSuccess,
  locationPayload,
  mediaPayload,
  type LinkCardInput,
  type LocationInput,
  type PostMediaInput,
  requestInit,
} from "./client";
import {
  decodeResponse,
  parsePost,
  parseSiteIdentity,
  type PostVisibility,
  type PublicPost,
  type SiteIdentity,
} from "./public";

export class PostsApi extends ApiClient {
  async create(
    csrfToken: string,
    contentMd: string,
    idempotencyKey: string,
    media: PostMediaInput[] = [],
    location?: LocationInput | null,
    quotedPostId?: string,
    linkCards?: LinkCardInput[] | null,
    visibility?: PostVisibility,
  ): Promise<PublicPost> {
    const body: Record<string, unknown> = {
      content_md: contentMd,
      media: mediaPayload(media),
    };
    if (location !== undefined) {
      body.location = location === null ? null : locationPayload(location);
    }
    if (linkCards !== undefined) body.link_cards = linkCards ?? [];
    if (quotedPostId !== undefined) body.quoted_post_id = quotedPostId;
    if (visibility !== undefined) body.visibility = visibility;
    const init = requestInit("POST", csrfToken, body);
    const headers = new Headers(init.headers);
    headers.set("Idempotency-Key", idempotencyKey);
    const response = await this.fetcher("/api/v1/posts", {
      ...init,
      headers,
    });
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async update(
    csrfToken: string,
    id: string,
    contentMd: string,
    media?: PostMediaInput[],
    location?: LocationInput | null,
    linkCards?: LinkCardInput[] | null,
    visibility?: PostVisibility,
  ): Promise<PublicPost> {
    const body: Record<string, unknown> = { content_md: contentMd };
    if (media) body.media = mediaPayload(media);
    if (location !== undefined) {
      body.location = location === null ? null : locationPayload(location);
    }
    if (linkCards !== undefined) body.link_cards = linkCards ?? [];
    if (visibility !== undefined) body.visibility = visibility;
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(id)}`,
      requestInit("PATCH", csrfToken, body),
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async delete(csrfToken: string, id: string): Promise<PublicPost> {
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(id)}`,
      requestInit("DELETE", csrfToken),
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async setReposted(
    csrfToken: string,
    id: string,
    active: boolean,
  ): Promise<PublicPost> {
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(id)}/repost`,
      requestInit(active ? "PUT" : "DELETE", csrfToken),
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async pinPost(csrfToken: string, id: string): Promise<SiteIdentity> {
    return this.pinPostChange(csrfToken, id, "PUT");
  }

  async unpinPost(csrfToken: string, id: string): Promise<SiteIdentity> {
    return this.pinPostChange(csrfToken, id, "DELETE");
  }

  private async pinPostChange(
    csrfToken: string,
    id: string,
    method: "PUT" | "DELETE",
  ): Promise<SiteIdentity> {
    const response = await this.fetcher(
      `/api/v1/posts/${encodeURIComponent(id)}/pin`,
      requestInit(method, csrfToken),
    );
    const data = await decodeExactSuccess(response, [
      "profile_user_id",
      "username",
      "display_name",
      "title",
      "bio",
      "timezone",
      "public_base_url",
      "avatar_media_id",
      "cover_media_id",
      "pinned_post_id",
      "comments_enabled",
    ]);
    return parseSiteIdentity(data);
  }
}

export const postsApi = new PostsApi();
