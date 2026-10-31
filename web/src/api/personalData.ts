import {
  ApiClient,
  booleanField,
  hasExactKeys,
  invalidResponse,
  nonNegativeIntegerField,
  objectData,
  stringField,
} from "./client";
import { decodeResponse } from "./public";

const countKeys = [
  "entries",
  "comments",
  "media",
  "users",
  "identities",
  "articles",
  "article_drafts",
] as const;
export type PersonalCounts = Record<(typeof countKeys)[number], number> & {
  interactions?: number;
};
export interface PersonalImport {
  dryRun: boolean;
  archiveSHA256: string;
  accountId: string;
  counts: PersonalCounts;
  added: PersonalCounts;
  skipped: PersonalCounts;
}
function counts(value: unknown): PersonalCounts {
  const data = objectData(value);
  if (
    !hasExactKeys(data, [
      ...countKeys,
      ...(data.interactions === undefined ? [] : ["interactions"]),
    ])
  )
    throw invalidResponse("personal import counts are invalid");
  return {
    ...(data.interactions === undefined
      ? {}
      : { interactions: nonNegativeIntegerField(data, "interactions") }),
    entries: nonNegativeIntegerField(data, "entries"),
    comments: nonNegativeIntegerField(data, "comments"),
    media: nonNegativeIntegerField(data, "media"),
    users: nonNegativeIntegerField(data, "users"),
    identities: nonNegativeIntegerField(data, "identities"),
    articles: nonNegativeIntegerField(data, "articles"),
    article_drafts: nonNegativeIntegerField(data, "article_drafts"),
  };
}
export class PersonalDataApi extends ApiClient {
  async import(
    file: File,
    csrf: string,
    expectedSHA: string | null,
    signal: AbortSignal,
  ): Promise<PersonalImport> {
    const headers: Record<string, string> = {
      "Content-Type": "application/zip",
      Accept: "application/json",
      "X-CSRF-Token": csrf,
    };
    if (expectedSHA) headers["X-Moment-Archive-SHA256"] = expectedSHA;
    const response = await this.fetcher(
      `/api/v1/account/import/${expectedSHA ? "restore" : "plan"}`,
      { method: "POST", headers, body: file, signal },
    );
    const { data: raw } = await decodeResponse(response);
    const data = objectData(raw);
    if (
      !hasExactKeys(data, [
        "dry_run",
        "archive_sha256",
        "account_id",
        "counts",
        "added",
        "skipped",
      ])
    )
      throw invalidResponse("personal import response is invalid");
    const sha = stringField(data, "archive_sha256");
    if (
      !/^[a-f0-9]{64}$/.test(sha) ||
      (expectedSHA !== null && sha !== expectedSHA)
    )
      throw invalidResponse("personal import archive changed");
    const dryRun = booleanField(data, "dry_run");
    if (dryRun !== (expectedSHA === null))
      throw invalidResponse("personal import phase is invalid");
    return {
      dryRun,
      archiveSHA256: sha,
      accountId: stringField(data, "account_id"),
      counts: counts(data.counts),
      added: counts(data.added),
      skipped: counts(data.skipped),
    };
  }
}
export const personalDataApi = new PersonalDataApi();
