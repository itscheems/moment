import {
  ApiClient,
  hasExactKeys,
  invalidResponse,
  type LinkCardInput,
  requestInit,
} from "./client";
import { decodeResponse } from "./public";

export const linkCardInputKeys = [
  "source_url",
  "canonical_url",
  "provider",
  "card_kind",
  "title",
  "description",
  "site_name",
  "author_name",
  "duration_seconds",
  "view_count",
  "comment_count",
  "like_count",
  "favicon_media_id",
  "thumbnail_media_id",
  "thumbnail_mime_type",
  "thumbnail_width",
  "thumbnail_height",
  "embed_url",
  "captured_at_ms",
];

export function parseLinkCardInput(value: unknown): LinkCardInput {
  // mirrors linkdto.Card (internal/transport/linkdto/dto.go)
  if (
    typeof value !== "object" ||
    value === null ||
    !hasExactKeys(value as Record<string, unknown>, linkCardInputKeys)
  ) {
    throw invalidResponse("link card contains an unsupported or missing field");
  }
  return value as unknown as LinkCardInput;
}

export class LinksApi extends ApiClient {
  async unfurlLink(url: string, signal?: AbortSignal): Promise<LinkCardInput> {
    const params = new URLSearchParams({ url });
    const response = await this.fetcher(`/api/v1/links/unfurl?${params}`, {
      ...requestInit("GET"),
      signal,
    });
    const { data } = await decodeResponse(response);
    return parseLinkCardInput(data);
  }
}

export const linksApi = new LinksApi();
