import {
  decodeCommentResponse,
  decodeCreatedComment,
  decodeResponse,
  parseComment,
  parseDeletedCommentMeta,
  parsePost,
  parsePrivateAccount,
  parseSiteIdentity,
  PublicApiError,
  uploadMediaXHR,
  type Account,
  type AccountStatus,
  type CreatedComment,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
} from "./public";

export interface OwnerSession {
  csrfToken: string;
  expiresAt: string;
}

export interface OwnerStatus {
  initialized: boolean;
  passwordEnabled: boolean;
  sessionEpoch: number;
  initializedAt: string | null;
  rotatedAt: string | null;
}

export interface RotatedCredential {
  token: string;
  initialized: boolean;
  sessionEpoch: number;
  initializedAt: string;
  rotatedAt: string;
}

export interface OwnerLoginOptions {
  passwordEnabled: boolean;
}

export interface OAuthLoginProvider {
  name: string;
  label: string;
}

/**
 * What a valid `moment_session` cookie is worth: the CSRF token every unsafe
 * request needs, when it stops being worth anything, and who the browser is
 * signed in as.
 *
 * This is the only endpoint that can restore a signed-in browser after a
 * refresh. The owner status probe cannot: it answers 401 for a member, and
 * reading that as "signed out" would sign every member out of every tab.
 */
/**
 * The bounds the server enforces, published so a client need not keep a second
 * copy of them.
 *
 * The picture ceiling used to reach a browser only as details on a rejection,
 * which is after the upload has already been spent -- so anything that wanted
 * to refuse an oversized file before sending it had to carry its own number
 * and keep it in step by hand.
 */
export interface AccountLimits {
  profilePictureBytes: number;
  displayNameLength: number;
  bioLength: number;
}

export interface AccountSession {
  csrfToken: string;
  expiresAt: string;
  account: Account;
  limits: AccountLimits;
}

/** A third-party login bound to an account. */
export interface AccountIdentity {
  provider: string;
  /** What the provider calls this person. Empty when it published nothing. */
  displayName: string;
  /**
   * Empty unless the provider published an address the server is willing to
   * send to: a GitHub noreply alias reads as no address at all.
   */
  email: string;
  linkedAt: string;
  lastLoginAt: string;
}

/** One signed-in browser. */
export interface AccountSessionEntry {
  id: string;
  issuedAt: string;
  lastSeenAt: string;
  expiresAt: string;
  surface: string;
  /** True for the session making the request, which is never more than one. */
  current: boolean;
}

export interface AccountProfileUpdate {
  username?: string;
  displayName?: string;
  bio?: string;
  avatarMediaId?: string | null;
  coverMediaId?: string | null;
}

export interface SiteSettingsUpdate {
  username?: string;
  displayName?: string;
  title?: string;
  bio?: string;
  timezone?: string;
  publicBaseUrl?: string | null;
  avatarMediaId?: string | null;
  coverMediaId?: string | null;
  commentsEnabled?: boolean;
}

export interface DeletedComment {
  comment: PublicComment;
  repliesAffected: number;
}

export interface CommentPin {
  postId: string;
  pinnedCommentId: string | null;
}

export interface UploadedMedia {
  id: string;
  byteSize: number;
  width: number;
  height: number;
  mimeType: string;
  previewMimeType: string;
  animatedOriginal: boolean;
  previewUrl: string;
  originalUrl: string;
}

export interface PostMediaInput {
  mediaId: string;
  position: number;
  altText: string | null;
}

export type LocationProvider = "manual" | "amap" | "geoapify";
export type CoordinateSystem = "wgs84" | "gcj02";

/**
 * The owner-facing card shape. It is the complete snapshot, unlike the narrow
 * public projection, because publishing must send back exactly what the unfurl
 * produced. The server re-validates every field regardless.
 */
export interface OwnerLinkCard {
  source_url: string;
  canonical_url: string;
  provider: string;
  card_kind: string;
  title: string;
  description: string;
  site_name: string;
  author_name: string;
  duration_seconds: number | null;
  view_count: number | null;
  comment_count: number | null;
  like_count: number | null;
  favicon_media_id: string | null;
  thumbnail_media_id: string | null;
  thumbnail_mime_type: string | null;
  thumbnail_width: number | null;
  thumbnail_height: number | null;
  embed_url: string | null;
  captured_at_ms: number | null;
}

const ownerLinkCardKeys = [
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

function parseOwnerLinkCard(value: unknown): OwnerLinkCard {
  // mirrors linkdto.Card (internal/transport/linkdto/dto.go)
  if (
    typeof value !== "object" ||
    value === null ||
    !hasExactKeys(value as Record<string, unknown>, ownerLinkCardKeys)
  ) {
    throw invalidResponse("link card contains an unsupported or missing field");
  }
  return value as unknown as OwnerLinkCard;
}

export interface OwnerLocation {
  name: string;
  displayName: string;
  countryCode: string;
  country: string;
  region: string;
  city: string;
  district: string;
  category: string;
  provider: LocationProvider;
  providerPlaceId: string;
  latitude: number | null;
  longitude: number | null;
  coordinateSystem: CoordinateSystem | null;
}

export interface LocationBias {
  latitude: number;
  longitude: number;
}

export interface ImportCounts {
  entries: number;
  comments: number;
  media: number;
  /**
   * Accounts carried by the archive, and the third-party logins bound to
   * them. A restore replaces the account graph wholesale, so these are the
   * two numbers that say who disappears -- which makes them the ones an owner
   * needs before an irreversible decision, not after it.
   */
  users: number;
  identities: number;
}

export interface ImportDiskEstimate {
  archiveBytes: number;
  expandedBytes: number;
  stagingBytes: number;
  preImportBackupBytes: number;
  rollbackReserveBytes: number;
  safetyMarginBytes: number;
  requiredAdditionalBytes: number;
  estimatedPeakFootprintBytes: number;
  availableBytes: number;
  spaceOK: boolean;
}

export interface ImportPlan {
  formatVersion: number;
  archiveSHA256: string;
  counts: ImportCounts;
  timeRange: { from: string | null; to: string | null };
  mediaBytes: number;
  missingAlt: number;
  disk: ImportDiskEstimate;
  replacement: {
    counts: ImportCounts;
    mediaBytes: number;
    dataBytes: number;
  };
  warnings: string[];
}

export type MergeClassification =
  "unchanged" | "incoming_only" | "local_only" | "conflict";
export type MergeAction =
  "skip" | "add" | "keep_local" | "use_incoming" | "keep_both";
/**
 * What a merge plan can name.
 *
 * `user` and `identity` are reported but never selectable. An account is not
 * content: "keep both" would fork a person into two, and "use incoming" would
 * let a backup rename or re-admit somebody on the live instance. They are
 * resolved by a fixed rule -- local wins, unknown incoming accounts are added
 * -- and appear so a reviewer can see what a restore brings with it.
 */
export type MergeEntityKind =
  "entry" | "comment" | "media" | "user" | "identity";

export interface MergeSelection {
  plan_id: string;
  archive_sha256: string;
  base_generation: string;
  base_authority_sha256: string;
  items: Array<{ kind: MergeEntityKind; id: string; action: MergeAction }>;
  site_fields: Array<{ field: string; action: MergeAction }>;
}

export interface MergePlan {
  version: number;
  plan_id: string;
  archive_sha256: string;
  base_generation: string;
  base_authority_sha256: string;
  incoming_authority_sha256: string;
  owner_id: string;
  expires_at: string;
  summary: {
    unchanged: number;
    incoming_only: number;
    local_only: number;
    conflict: number;
  };
  items: Array<{
    kind: MergeEntityKind;
    id: string;
    classification: MergeClassification;
    changed_fields: string[];
    dependencies: Array<{ kind: MergeEntityKind; id: string }>;
    selectable: boolean;
    matched_local_id?: string;
  }>;
  site_fields: Array<{
    field: string;
    classification: MergeClassification;
    local_value: unknown;
    incoming_value: unknown;
    dependencies: Array<{ kind: MergeEntityKind; id: string }>;
  }>;
  default_selection: MergeSelection;
}

export type MergeReviewSource = "local" | "incoming";

export interface MergeReviewMedia {
  source: MergeReviewSource;
  id: string;
  original_name: string;
  mime_type: string;
  byte_size: number;
  width: number;
  height: number;
  position: number;
  alt_text: string | null;
}

export interface MergeReviewLinkCard {
  position: number;
  source_url: string;
  canonical_url: string;
  provider: string;
  card_kind: string;
  title: string;
  site_name: string;
}

/**
 * One row of a merge review, whatever kind of thing it describes.
 *
 * A single struct serves posts, comments, accounts and identities, and each
 * projection fills a different part of it -- so all but the four fields below
 * are absent on most rows rather than merely null. That is why this is
 * validated as a whitelist and not as an exact key set: an exact set would
 * have to enumerate one combination per kind per nullable column, and would
 * break again the first time a projection learned to omit one more.
 */
export interface MergeReviewEntity {
  id: string;
  /** Empty on an account or identity row, which the Go field still emits. */
  content_md: string;
  created_at: string;
  media: MergeReviewMedia[];

  // A post.
  kind?: string;
  updated_at?: string;
  published_at?: string;
  reposted_at?: string;
  quoted_entry_id?: string;
  quote_unavailable?: boolean;
  link_cards?: MergeReviewLinkCard[];

  // A comment.
  entry_id?: string;
  parent_id?: string;
  author_kind?: string;
  author_name?: string;
  author_user_id?: string;
  reply_to_comment_id?: string;
  pinned?: boolean;

  // A post or a comment.
  location?: OwnerLocation;

  // An account.
  username?: string;
  display_name?: string;
  bio?: string;
  role?: string;
  status?: string;
  avatar_media_id?: string;
  cover_media_id?: string;
  username_set_at?: string;
  avatar?: MergeReviewMedia;
  cover?: MergeReviewMedia;

  // A third-party login bound to an account.
  provider?: string;
  provider_user_id?: string;
  user_id?: string;
  email?: string;
  email_verified?: boolean;
  email_deliverable?: boolean;
  avatar_url?: string;
  last_login_at?: string;
}

export interface MergeReviewItem {
  kind: Exclude<MergeEntityKind, "media">;
  id: string;
  classification: Exclude<MergeClassification, "unchanged">;
  changed_fields: string[];
  local?: MergeReviewEntity;
  incoming?: MergeReviewEntity;
}

export interface MergeReviewBucket {
  total: number;
  truncated: boolean;
  items: MergeReviewItem[];
}

export interface MergeReviewSiteField {
  field: string;
  classification: MergeClassification;
  local_value: unknown;
  incoming_value: unknown;
  local_media?: MergeReviewMedia;
  incoming_media?: MergeReviewMedia;
  local_entry?: MergeReviewEntity;
  incoming_entry?: MergeReviewEntity;
}

export interface MergeReview {
  incoming_only: MergeReviewBucket;
  local_only: MergeReviewBucket;
  conflicts: MergeReviewBucket;
  site_fields: MergeReviewSiteField[];
}

export interface MergeInspection {
  plan: MergePlan;
  review: MergeReview;
  incomingCounts: ImportCounts;
  incomingMediaBytes: number;
  disk: ImportDiskEstimate;
  replacement: {
    counts: ImportCounts;
    mediaBytes: number;
    dataBytes: number;
  };
  mergeAllowed: boolean;
  mergeBlockedReason: string | null;
  warnings: string[];
  expiresAt: string;
  stagingToken: string;
}

export type ImportPhase =
  | "claimed"
  | "backing_up"
  | "finalizing"
  | "switching"
  | "verifying"
  | "rolling_back"
  | "succeeded"
  | "failed"
  | "rolled_back"
  | "needs_attention";

export interface ImportStatus {
  operationId: string;
  phase: ImportPhase;
  activeGeneration: string;
  previousGeneration: string;
  newGeneration: string;
  backupVerified: boolean;
  rollbackAttempted: boolean;
  rollbackSucceeded: boolean;
  errorCode: string | null;
  startedAt: string;
  updatedAt: string;
  completedAt: string | null;
}

type Fetcher = typeof fetch;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function invalidResponse(message: string): PublicApiError {
  return new PublicApiError(message, {
    code: "API_RESPONSE_INVALID",
    status: 0,
  });
}

function stringField(record: Record<string, unknown>, field: string): string {
  const value = record[field];
  if (typeof value !== "string") {
    throw invalidResponse(`response field ${field} must be a string`);
  }
  return value;
}

function booleanField(record: Record<string, unknown>, field: string): boolean {
  const value = record[field];
  if (typeof value !== "boolean") {
    throw invalidResponse(`response field ${field} must be a boolean`);
  }
  return value;
}

function objectData(data: unknown): Record<string, unknown> {
  if (!isRecord(data)) throw invalidResponse("response data must be an object");
  return data;
}

function hasExactKeys(record: Record<string, unknown>, expected: string[]) {
  const actual = Object.keys(record).sort();
  const sortedExpected = [...expected].sort();
  return (
    actual.length === sortedExpected.length &&
    actual.every((value, index) => value === sortedExpected[index])
  );
}

function positiveIntegerField(
  record: Record<string, unknown>,
  field: string,
): number {
  const value = record[field];
  if (!Number.isSafeInteger(value) || Number(value) <= 0) {
    throw invalidResponse(`response field ${field} must be a positive integer`);
  }
  return Number(value);
}

function nonNegativeIntegerField(
  record: Record<string, unknown>,
  field: string,
): number {
  const value = record[field];
  if (!Number.isSafeInteger(value) || Number(value) < 0) {
    throw invalidResponse(
      `response field ${field} must be a non-negative safe integer`,
    );
  }
  return Number(value);
}

function utcInstantField(
  record: Record<string, unknown>,
  field: string,
): string {
  const value = stringField(record, field);
  const match = value.match(
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/,
  );
  if (!match) {
    throw invalidResponse(`response field ${field} must be a UTC instant`);
  }
  const [, yearText, monthText, dayText, hourText, minuteText, secondText] =
    match;
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const hour = Number(hourText);
  const minute = Number(minuteText);
  const second = Number(secondText);
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const monthDays = [
    31,
    leapYear ? 29 : 28,
    31,
    30,
    31,
    30,
    31,
    31,
    30,
    31,
    30,
    31,
  ];
  if (
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > monthDays[month - 1] ||
    hour > 23 ||
    minute > 59 ||
    second > 59
  ) {
    throw invalidResponse(`response field ${field} must be a UTC instant`);
  }
  return value;
}

function optionalInstantField(
  record: Record<string, unknown>,
  field: string,
): string | null {
  return record[field] === undefined ? null : utcInstantField(record, field);
}

function canonicalUTCField(
  record: Record<string, unknown>,
  field: string,
): string {
  const value = utcInstantField(record, field);
  const fraction = value.match(/\.(\d+)Z$/)?.[1];
  if (fraction?.endsWith("0")) {
    throw invalidResponse(`response field ${field} must be canonical UTC`);
  }
  return value;
}

function canonicalUTCKey(value: string): string {
  const match = value.match(
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/,
  );
  if (!match) throw invalidResponse("UTC instant cannot be ordered");
  const [, year, month, day, hour, minute, second, fraction = ""] = match;
  return `${year}${month}${day}${hour}${minute}${second}${fraction.padEnd(9, "0")}`;
}

function compareCanonicalUTC(left: string, right: string): number {
  return canonicalUTCKey(left).localeCompare(canonicalUTCKey(right));
}

function parseImportCounts(value: unknown): ImportCounts {
  const record = objectData(value);
  // mirrors archive.ImportCounts (internal/archive/import.go)
  if (
    !hasExactKeys(record, [
      "entries",
      "comments",
      "media",
      "users",
      "identities",
    ])
  ) {
    throw invalidResponse("import counts contain unsupported fields");
  }
  return {
    entries: nonNegativeIntegerField(record, "entries"),
    comments: nonNegativeIntegerField(record, "comments"),
    media: nonNegativeIntegerField(record, "media"),
    users: nonNegativeIntegerField(record, "users"),
    identities: nonNegativeIntegerField(record, "identities"),
  };
}

function parseMergeInspection(value: unknown): MergeInspection {
  const record = objectData(value);
  // mirrors archive.MergeInspection (internal/archive/merge_commit.go)
  if (
    !hasExactKeys(record, [
      "plan",
      "review",
      "incoming_counts",
      "incoming_media_bytes",
      "disk",
      "replacement",
      "merge_allowed",
      "merge_blocked_reason",
      "warnings",
      "expires_at",
      "staging_token",
    ])
  ) {
    throw invalidResponse("merge inspection contains unsupported fields");
  }
  const planRecord = objectData(record.plan);
  // mirrors archive.MergePlan (internal/archive/merge.go)
  if (
    !hasExactKeys(planRecord, [
      "version",
      "plan_id",
      "archive_sha256",
      "base_generation",
      "base_authority_sha256",
      "incoming_authority_sha256",
      "owner_id",
      "expires_at",
      "summary",
      "items",
      "site_fields",
      "default_selection",
    ])
  ) {
    throw invalidResponse("merge plan identity is invalid");
  }
  const version = positiveIntegerField(planRecord, "version");
  const planID = stringField(planRecord, "plan_id");
  const archiveSHA256 = stringField(planRecord, "archive_sha256");
  const baseGeneration = stringField(planRecord, "base_generation");
  const baseAuthoritySHA256 = stringField(planRecord, "base_authority_sha256");
  const incomingAuthoritySHA256 = stringField(
    planRecord,
    "incoming_authority_sha256",
  );
  const ownerID = stringField(planRecord, "owner_id");
  const planExpiresAt = canonicalUTCField(planRecord, "expires_at");
  if (
    version !== 1 ||
    !/^[0-9a-f]{64}$/.test(planID) ||
    !/^[0-9a-f]{64}$/.test(archiveSHA256) ||
    !/^[0-9a-f]{64}$/.test(baseAuthoritySHA256) ||
    !/^[0-9a-f]{64}$/.test(incomingAuthoritySHA256) ||
    !/^[0-9a-f]{32}$/.test(ownerID) ||
    !/^[A-Za-z0-9_-]{1,128}$/.test(baseGeneration)
  ) {
    throw invalidResponse("merge plan identity is invalid");
  }
  const summary = objectData(planRecord.summary);
  // mirrors archive.MergeSummary (internal/archive/merge.go)
  if (
    !hasExactKeys(summary, [
      "unchanged",
      "incoming_only",
      "local_only",
      "conflict",
    ])
  ) {
    throw invalidResponse("merge summary is invalid");
  }
  const parsedSummary = {
    unchanged: nonNegativeIntegerField(summary, "unchanged"),
    incoming_only: nonNegativeIntegerField(summary, "incoming_only"),
    local_only: nonNegativeIntegerField(summary, "local_only"),
    conflict: nonNegativeIntegerField(summary, "conflict"),
  };
  if (
    !Array.isArray(planRecord.items) ||
    !Array.isArray(planRecord.site_fields)
  ) {
    throw invalidResponse("merge plan entity lists are invalid");
  }
  const kinds = new Set<MergeEntityKind>([
    "entry",
    "comment",
    "media",
    "user",
    "identity",
  ]);
  const classifications = new Set<MergeClassification>([
    "unchanged",
    "incoming_only",
    "local_only",
    "conflict",
  ]);
  const parseDependencies = (value: unknown) => {
    if (!Array.isArray(value)) {
      throw invalidResponse("merge dependencies are invalid");
    }
    return value.map((dependency) => {
      const candidate = objectData(dependency);
      // mirrors archive.MergeRef (internal/archive/merge.go)
      if (!hasExactKeys(candidate, ["kind", "id"])) {
        throw invalidResponse("merge dependency contains unsupported fields");
      }
      const kind = stringField(candidate, "kind") as MergeEntityKind;
      const id = stringField(candidate, "id");
      if (!kinds.has(kind) || !id || id.length > 256) {
        throw invalidResponse("merge dependency identity is invalid");
      }
      return { kind, id };
    });
  };
  const seenItems = new Set<string>();
  const items: MergePlan["items"] = planRecord.items.map((item) => {
    const candidate = objectData(item);
    const required = [
      "kind",
      "id",
      "classification",
      "changed_fields",
      "dependencies",
      "selectable",
    ];
    const keys = Object.keys(candidate);
    if (
      !keys.every((key) => [...required, "matched_local_id"].includes(key)) ||
      !required.every((key) => key in candidate)
    ) {
      throw invalidResponse("merge item contains unsupported fields");
    }
    const kind = stringField(candidate, "kind") as MergeEntityKind;
    const id = stringField(candidate, "id");
    const classification = stringField(
      candidate,
      "classification",
    ) as MergeClassification;
    const selectable = booleanField(candidate, "selectable");
    if (
      !kinds.has(kind) ||
      !id ||
      id.length > 256 ||
      !classifications.has(classification) ||
      (kind === "media" && selectable) ||
      (classification === "local_only" && selectable)
    ) {
      throw invalidResponse("merge item identity is invalid");
    }
    const key = `${kind}\u0000${id}`;
    if (seenItems.has(key)) {
      throw invalidResponse("merge plan contains duplicate items");
    }
    seenItems.add(key);
    if (
      !Array.isArray(candidate.changed_fields) ||
      !candidate.changed_fields.every(
        (field) => typeof field === "string" && field.length <= 128,
      )
    ) {
      throw invalidResponse("merge changed fields are invalid");
    }
    const matchedLocalID =
      "matched_local_id" in candidate
        ? stringField(candidate, "matched_local_id")
        : undefined;
    if (
      matchedLocalID !== undefined &&
      (!matchedLocalID || matchedLocalID.length > 256)
    ) {
      throw invalidResponse("merge matched media identity is invalid");
    }
    return {
      kind,
      id,
      classification,
      changed_fields: [...candidate.changed_fields],
      dependencies: parseDependencies(candidate.dependencies),
      selectable,
      ...(matchedLocalID === undefined
        ? {}
        : { matched_local_id: matchedLocalID }),
    };
  });
  const seenSiteFields = new Set<string>();
  const siteFields: MergePlan["site_fields"] = planRecord.site_fields.map(
    (field) => {
      const candidate = objectData(field);
      // mirrors archive.MergeSiteField (internal/archive/merge.go)
      if (
        !hasExactKeys(candidate, [
          "field",
          "classification",
          "local_value",
          "incoming_value",
          "dependencies",
        ])
      ) {
        throw invalidResponse(
          "merge profile field contains unsupported fields",
        );
      }
      const fieldName = stringField(candidate, "field");
      const classification = stringField(
        candidate,
        "classification",
      ) as MergeClassification;
      if (
        !fieldName ||
        fieldName.length > 128 ||
        seenSiteFields.has(fieldName) ||
        !classifications.has(classification)
      ) {
        throw invalidResponse("merge profile field identity is invalid");
      }
      seenSiteFields.add(fieldName);
      return {
        field: fieldName,
        classification,
        local_value: candidate.local_value,
        incoming_value: candidate.incoming_value,
        dependencies: parseDependencies(candidate.dependencies),
      };
    },
  );
  const computedSummary = {
    unchanged: 0,
    incoming_only: 0,
    local_only: 0,
    conflict: 0,
  };
  for (const candidate of [...items, ...siteFields]) {
    computedSummary[candidate.classification] += 1;
  }
  if (
    Object.keys(computedSummary).some(
      (key) =>
        computedSummary[key as keyof typeof computedSummary] !==
        parsedSummary[key as keyof typeof parsedSummary],
    )
  ) {
    throw invalidResponse("merge summary does not match the plan");
  }
  const selection = objectData(planRecord.default_selection);
  // mirrors archive.MergeSelection (internal/archive/merge.go)
  if (
    !hasExactKeys(selection, [
      "plan_id",
      "archive_sha256",
      "base_generation",
      "base_authority_sha256",
      "items",
      "site_fields",
    ])
  ) {
    throw invalidResponse("merge default selection is invalid");
  }
  if (
    !Array.isArray(selection.items) ||
    !Array.isArray(selection.site_fields)
  ) {
    throw invalidResponse("merge default selection lists are invalid");
  }
  const selectionPlanID = stringField(selection, "plan_id");
  const selectionArchiveSHA = stringField(selection, "archive_sha256");
  const selectionGeneration = stringField(selection, "base_generation");
  const selectionAuthoritySHA = stringField(selection, "base_authority_sha256");
  if (
    selectionPlanID !== planID ||
    selectionArchiveSHA !== archiveSHA256 ||
    selectionGeneration !== baseGeneration ||
    selectionAuthoritySHA !== baseAuthoritySHA256
  ) {
    throw invalidResponse("merge default selection binding is invalid");
  }
  const selectableByKey = new Map<string, MergePlan["items"][number]>(
    items
      .filter((item) => item.selectable)
      .map((item) => [`${item.kind}\u0000${item.id}`, item] as const),
  );
  const selectedKeys = new Set<string>();
  const selectionItems: MergeSelection["items"] = selection.items.map(
    (item) => {
      const candidate = objectData(item);
      // mirrors archive.MergeSelectionItem (internal/archive/merge.go)
      if (!hasExactKeys(candidate, ["kind", "id", "action"])) {
        throw invalidResponse("merge default item decision is invalid");
      }
      const kind = stringField(candidate, "kind") as MergeEntityKind;
      const id = stringField(candidate, "id");
      const action = stringField(candidate, "action") as MergeAction;
      const key = `${kind}\u0000${id}`;
      const planned = selectableByKey.get(key);
      const allowed =
        planned?.classification === "unchanged"
          ? action === "skip"
          : planned?.classification === "incoming_only"
            ? action === "add" || action === "skip"
            : planned?.classification === "conflict"
              ? ["keep_local", "use_incoming", "keep_both"].includes(action)
              : false;
      if (!planned || selectedKeys.has(key) || !allowed) {
        throw invalidResponse("merge default item decision is invalid");
      }
      selectedKeys.add(key);
      return { kind, id, action };
    },
  );
  if (selectedKeys.size !== selectableByKey.size) {
    throw invalidResponse("merge default selection is incomplete");
  }
  const selectedSiteFields = new Set<string>();
  const selectionSiteFields: MergeSelection["site_fields"] =
    selection.site_fields.map((field) => {
      const candidate = objectData(field);
      // mirrors archive.MergeSiteSelection (internal/archive/merge.go)
      if (!hasExactKeys(candidate, ["field", "action"])) {
        throw invalidResponse("merge default profile decision is invalid");
      }
      const fieldName = stringField(candidate, "field");
      const action = stringField(candidate, "action") as MergeAction;
      if (
        !seenSiteFields.has(fieldName) ||
        selectedSiteFields.has(fieldName) ||
        (action !== "keep_local" && action !== "use_incoming")
      ) {
        throw invalidResponse("merge default profile decision is invalid");
      }
      selectedSiteFields.add(fieldName);
      return { field: fieldName, action };
    });
  if (selectedSiteFields.size !== seenSiteFields.size) {
    throw invalidResponse("merge default profile selection is incomplete");
  }
  const expiresAt = canonicalUTCField(record, "expires_at");
  if (expiresAt !== planExpiresAt) {
    throw invalidResponse("merge plan expiry is inconsistent");
  }
  const stagingToken = stringField(record, "staging_token");
  if (!/^[A-Za-z0-9_-]{43}$/.test(stagingToken)) {
    throw invalidResponse("merge staging token is invalid");
  }
  const warnings = record.warnings;
  if (
    !Array.isArray(warnings) ||
    !warnings.every((item) => typeof item === "string" && item.length <= 2_000)
  ) {
    throw invalidResponse("merge warnings are invalid");
  }
  const incomingCounts = parseImportCounts(record.incoming_counts);
  if (
    incomingCounts.entries > 100_000 ||
    incomingCounts.comments > 500_000 ||
    incomingCounts.media > 100_000 ||
    incomingCounts.users > 100_000 ||
    incomingCounts.identities > 500_000
  ) {
    throw invalidResponse("merge incoming counts exceed supported limits");
  }
  const plan: MergePlan = {
    version,
    plan_id: planID,
    archive_sha256: archiveSHA256,
    base_generation: baseGeneration,
    base_authority_sha256: baseAuthoritySHA256,
    incoming_authority_sha256: incomingAuthoritySHA256,
    owner_id: ownerID,
    expires_at: planExpiresAt,
    summary: parsedSummary,
    items,
    site_fields: siteFields,
    default_selection: {
      plan_id: selectionPlanID,
      archive_sha256: selectionArchiveSHA,
      base_generation: selectionGeneration,
      base_authority_sha256: selectionAuthoritySHA,
      items: selectionItems,
      site_fields: selectionSiteFields,
    },
  };
  const replacementRecord = objectData(record.replacement);
  // mirrors archive.ReplacementImpact (internal/archive/import.go)
  if (
    !hasExactKeys(replacementRecord, ["counts", "media_bytes", "data_bytes"])
  ) {
    throw invalidResponse("merge replacement impact is invalid");
  }
  const mergeAllowed = booleanField(record, "merge_allowed");
  const mergeBlockedReason = record.merge_blocked_reason;
  if (
    mergeBlockedReason !== null &&
    (typeof mergeBlockedReason !== "string" || mergeBlockedReason.length > 128)
  ) {
    throw invalidResponse("merge availability reason is invalid");
  }
  if (mergeAllowed !== (mergeBlockedReason === null)) {
    throw invalidResponse("merge availability is inconsistent");
  }
  return {
    plan,
    review: parseMergeReview(record.review, plan),
    incomingCounts,
    incomingMediaBytes: nonNegativeIntegerField(record, "incoming_media_bytes"),
    disk: parseImportDisk(record.disk),
    replacement: {
      counts: parseImportCounts(replacementRecord.counts),
      mediaBytes: nonNegativeIntegerField(replacementRecord, "media_bytes"),
      dataBytes: nonNegativeIntegerField(replacementRecord, "data_bytes"),
    },
    mergeAllowed,
    mergeBlockedReason,
    warnings: [...warnings],
    expiresAt,
    stagingToken,
  };
}

function parseMergeReviewMedia(value: unknown): MergeReviewMedia {
  const record = objectData(value);
  // mirrors archive.MergeReviewMedia (internal/archive/merge_review.go)
  if (
    !hasExactKeys(record, [
      "source",
      "id",
      "original_name",
      "mime_type",
      "byte_size",
      "width",
      "height",
      "position",
      "alt_text",
    ])
  ) {
    throw invalidResponse("merge review media contains unsupported fields");
  }
  const source = stringField(record, "source") as MergeReviewSource;
  const altText = record.alt_text;
  if (
    (source !== "local" && source !== "incoming") ||
    (altText !== null && typeof altText !== "string")
  ) {
    throw invalidResponse("merge review media is invalid");
  }
  return {
    source,
    id: stringField(record, "id"),
    original_name: stringField(record, "original_name"),
    mime_type: stringField(record, "mime_type"),
    byte_size: nonNegativeIntegerField(record, "byte_size"),
    width: positiveIntegerField(record, "width"),
    height: positiveIntegerField(record, "height"),
    position: nonNegativeIntegerField(record, "position"),
    alt_text: altText,
  };
}

// mirrors archive.MergeReviewLinkCard (internal/archive/merge_review.go)
function parseMergeReviewLinkCard(value: unknown): MergeReviewLinkCard {
  const record = objectData(value);
  if (
    !hasExactKeys(record, [
      "position",
      "source_url",
      "canonical_url",
      "provider",
      "card_kind",
      "title",
      "site_name",
    ])
  ) {
    throw invalidResponse("merge review link card contains unsupported fields");
  }
  return {
    position: nonNegativeIntegerField(record, "position"),
    source_url: stringField(record, "source_url"),
    canonical_url: stringField(record, "canonical_url"),
    provider: stringField(record, "provider"),
    card_kind: stringField(record, "card_kind"),
    title: stringField(record, "title"),
    site_name: stringField(record, "site_name"),
  };
}

/**
 * Every key `archive.MergeReviewEntity` can carry, grouped by how it decodes.
 *
 * Four of them are always on the wire because their Go fields carry no
 * `omitempty`: a zero `content_md` still serialises, which is how an account
 * row satisfies a field that means nothing to it. The rest are pointers or
 * slices that vanish when unset, and which of them appear is decided by the
 * projection that built the row.
 */
/**
 * The kinds a review bucket can contain.
 *
 * Media never reaches one -- the Go builder skips it -- while accounts and
 * identities do, precisely so a reviewer can see who a restore brings with it.
 */
const REVIEW_ITEM_KINDS = new Set(["entry", "comment", "user", "identity"]);

const REVIEW_ENTITY_REQUIRED = ["id", "content_md", "created_at", "media"];
const REVIEW_ENTITY_STRINGS = [
  "entry_id",
  "parent_id",
  "kind",
  "author_kind",
  "author_name",
  "author_user_id",
  "reply_to_comment_id",
  "quoted_entry_id",
  "username",
  "display_name",
  "bio",
  "role",
  "status",
  "avatar_media_id",
  "cover_media_id",
  "provider",
  "provider_user_id",
  "user_id",
  "email",
  "avatar_url",
];
const REVIEW_ENTITY_BOOLEANS = [
  "pinned",
  "quote_unavailable",
  "email_verified",
  "email_deliverable",
];
const REVIEW_ENTITY_INSTANTS = [
  "reposted_at",
  "updated_at",
  "published_at",
  "username_set_at",
  "last_login_at",
];
const REVIEW_ENTITY_ALLOWED = new Set([
  ...REVIEW_ENTITY_REQUIRED,
  ...REVIEW_ENTITY_STRINGS,
  ...REVIEW_ENTITY_BOOLEANS,
  ...REVIEW_ENTITY_INSTANTS,
  "location",
  "link_cards",
  "avatar",
  "cover",
]);

/**
 * mirrors archive.MergeReviewEntity (internal/archive/merge_review.go)
 *
 * Checked as a whitelist rather than as an exact key set, which is the one
 * place in this client where that distinction is forced. One struct serves
 * posts, comments, accounts and identities; each projection fills a different
 * quarter of it, and every field outside the four required ones disappears
 * when unset. An exact set would need one entry per kind per nullable column,
 * and would break again the next time a column went null.
 */
function parseMergeReviewEntity(value: unknown): MergeReviewEntity {
  const record = objectData(value);
  if (
    !REVIEW_ENTITY_REQUIRED.every((field) => field in record) ||
    !Object.keys(record).every((field) => REVIEW_ENTITY_ALLOWED.has(field)) ||
    !Array.isArray(record.media)
  ) {
    throw invalidResponse("merge review entity contains unsupported fields");
  }
  // Assembled through an index signature because the value type differs per
  // key; the interface above states the shape and the checks below enforce it.
  const optional: Record<string, unknown> = {};
  for (const field of REVIEW_ENTITY_STRINGS) {
    if (field in record) optional[field] = stringField(record, field);
  }
  for (const field of REVIEW_ENTITY_BOOLEANS) {
    if (field in record) optional[field] = booleanField(record, field);
  }
  for (const field of REVIEW_ENTITY_INSTANTS) {
    if (field in record) optional[field] = canonicalUTCField(record, field);
  }
  if ("location" in record) {
    optional.location = parseOwnerLocation(record.location);
  }
  if ("link_cards" in record) {
    if (!Array.isArray(record.link_cards)) {
      throw invalidResponse("merge review link cards must be an array");
    }
    optional.link_cards = record.link_cards.map(parseMergeReviewLinkCard);
  }
  if ("avatar" in record) {
    optional.avatar = parseMergeReviewMedia(record.avatar);
  }
  if ("cover" in record) {
    optional.cover = parseMergeReviewMedia(record.cover);
  }
  return {
    ...optional,
    id: stringField(record, "id"),
    content_md: stringField(record, "content_md"),
    created_at: canonicalUTCField(record, "created_at"),
    media: record.media.map(parseMergeReviewMedia),
  } as MergeReviewEntity;
}

function parseMergeReview(value: unknown, plan: MergePlan): MergeReview {
  const record = objectData(value);
  // mirrors archive.MergeReview (internal/archive/merge_review.go)
  if (
    !hasExactKeys(record, [
      "incoming_only",
      "local_only",
      "conflicts",
      "site_fields",
    ]) ||
    !Array.isArray(record.site_fields)
  ) {
    throw invalidResponse("merge review contains unsupported fields");
  }
  const planItems = new Map(
    plan.items.map((item) => [`${item.kind}\u0000${item.id}`, item] as const),
  );
  const parseBucket = (
    candidate: unknown,
    classification: "incoming_only" | "local_only" | "conflict",
  ): MergeReviewBucket => {
    const bucket = objectData(candidate);
    // mirrors archive.MergeReviewBucket (internal/archive/merge_review.go)
    if (
      !hasExactKeys(bucket, ["total", "truncated", "items"]) ||
      !Array.isArray(bucket.items)
    ) {
      throw invalidResponse("merge review bucket is invalid");
    }
    const total = nonNegativeIntegerField(bucket, "total");
    const truncated = booleanField(bucket, "truncated");
    if (
      bucket.items.length > 200 ||
      total < bucket.items.length ||
      truncated !== total > bucket.items.length
    ) {
      throw invalidResponse("merge review bucket bounds are invalid");
    }
    const items = bucket.items.map((item): MergeReviewItem => {
      const current = objectData(item);
      const required = ["kind", "id", "classification", "changed_fields"];
      const optional = ["local", "incoming"];
      if (
        !required.every((field) => field in current) ||
        !Object.keys(current).every((field) =>
          [...required, ...optional].includes(field),
        ) ||
        !Array.isArray(current.changed_fields)
      ) {
        throw invalidResponse("merge review item is invalid");
      }
      const named = stringField(current, "kind");
      if (!REVIEW_ITEM_KINDS.has(named)) {
        throw invalidResponse("merge review item does not match its plan");
      }
      const kind = named as Exclude<MergeEntityKind, "media">;
      const id = stringField(current, "id");
      const planned = planItems.get(`${kind}\u0000${id}`);
      if (
        current.classification !== classification ||
        planned?.classification !== classification ||
        !current.changed_fields.every((field) => typeof field === "string")
      ) {
        throw invalidResponse("merge review item does not match its plan");
      }
      const local =
        current.local === undefined
          ? undefined
          : parseMergeReviewEntity(current.local);
      const incoming =
        current.incoming === undefined
          ? undefined
          : parseMergeReviewEntity(current.incoming);
      if (
        (classification === "incoming_only" &&
          (local !== undefined || incoming === undefined)) ||
        (classification === "local_only" &&
          (local === undefined || incoming !== undefined)) ||
        (classification === "conflict" &&
          (local === undefined || incoming === undefined))
      ) {
        throw invalidResponse("merge review item sides are invalid");
      }
      return {
        kind,
        id,
        classification,
        changed_fields: [...current.changed_fields],
        ...(local ? { local } : {}),
        ...(incoming ? { incoming } : {}),
      };
    });
    return { total, truncated, items };
  };
  const planFields = new Map(
    plan.site_fields.map((field) => [field.field, field]),
  );
  const siteFields = record.site_fields.map((item): MergeReviewSiteField => {
    const current = objectData(item);
    const required = [
      "field",
      "classification",
      "local_value",
      "incoming_value",
    ];
    const optional = [
      "local_media",
      "incoming_media",
      "local_entry",
      "incoming_entry",
    ];
    if (
      !required.every((field) => field in current) ||
      !Object.keys(current).every((field) =>
        [...required, ...optional].includes(field),
      )
    ) {
      throw invalidResponse("merge review profile field is invalid");
    }
    const field = stringField(current, "field");
    const classification = stringField(
      current,
      "classification",
    ) as MergeClassification;
    if (planFields.get(field)?.classification !== classification) {
      throw invalidResponse(
        "merge review profile field does not match its plan",
      );
    }
    return {
      field,
      classification,
      local_value: current.local_value,
      incoming_value: current.incoming_value,
      ...(current.local_media === undefined
        ? {}
        : { local_media: parseMergeReviewMedia(current.local_media) }),
      ...(current.incoming_media === undefined
        ? {}
        : { incoming_media: parseMergeReviewMedia(current.incoming_media) }),
      ...(current.local_entry === undefined
        ? {}
        : { local_entry: parseMergeReviewEntity(current.local_entry) }),
      ...(current.incoming_entry === undefined
        ? {}
        : { incoming_entry: parseMergeReviewEntity(current.incoming_entry) }),
    };
  });
  if (siteFields.length !== plan.site_fields.length) {
    throw invalidResponse("merge review profile fields are incomplete");
  }
  return {
    incoming_only: parseBucket(record.incoming_only, "incoming_only"),
    local_only: parseBucket(record.local_only, "local_only"),
    conflicts: parseBucket(record.conflicts, "conflict"),
    site_fields: siteFields,
  };
}

function parseImportDisk(value: unknown): ImportDiskEstimate {
  const record = objectData(value);
  const fields = [
    "archive_bytes",
    "expanded_bytes",
    "staging_bytes",
    "pre_import_backup_bytes",
    "rollback_reserve_bytes",
    "safety_margin_bytes",
    "required_additional_bytes",
    "estimated_peak_footprint_bytes",
    "available_bytes",
    "space_ok",
  ];
  // mirrors archive.ImportTimeRange (internal/archive/import.go)
  if (!hasExactKeys(record, fields)) {
    throw invalidResponse("import disk estimate contains unsupported fields");
  }
  const disk = {
    archiveBytes: nonNegativeIntegerField(record, "archive_bytes"),
    expandedBytes: nonNegativeIntegerField(record, "expanded_bytes"),
    stagingBytes: nonNegativeIntegerField(record, "staging_bytes"),
    preImportBackupBytes: nonNegativeIntegerField(
      record,
      "pre_import_backup_bytes",
    ),
    rollbackReserveBytes: nonNegativeIntegerField(
      record,
      "rollback_reserve_bytes",
    ),
    safetyMarginBytes: nonNegativeIntegerField(record, "safety_margin_bytes"),
    requiredAdditionalBytes: nonNegativeIntegerField(
      record,
      "required_additional_bytes",
    ),
    estimatedPeakFootprintBytes: nonNegativeIntegerField(
      record,
      "estimated_peak_footprint_bytes",
    ),
    availableBytes: nonNegativeIntegerField(record, "available_bytes"),
    spaceOK: booleanField(record, "space_ok"),
  };
  const required =
    disk.stagingBytes +
    disk.preImportBackupBytes +
    disk.rollbackReserveBytes +
    disk.safetyMarginBytes;
  if (
    !Number.isSafeInteger(required) ||
    required !== disk.requiredAdditionalBytes ||
    disk.spaceOK !== disk.availableBytes >= disk.requiredAdditionalBytes ||
    disk.estimatedPeakFootprintBytes < disk.requiredAdditionalBytes
  ) {
    throw invalidResponse("import disk estimate is internally inconsistent");
  }
  return disk;
}

function parseImportPlan(
  data: unknown,
  expectedArchiveBytes: number,
): ImportPlan {
  const record = objectData(data);
  // mirrors archive.ImportPlan (internal/archive/import.go)
  if (
    !hasExactKeys(record, [
      "dry_run",
      "format_version",
      "archive_sha256",
      "counts",
      "time_range",
      "media_bytes",
      "missing_alt",
      "disk",
      "replacement",
      "warnings",
    ]) ||
    record.dry_run !== true ||
    record.format_version !== 1
  ) {
    throw invalidResponse("import plan contract is invalid");
  }
  const archiveSHA256 = stringField(record, "archive_sha256");
  if (!/^[0-9a-f]{64}$/.test(archiveSHA256)) {
    throw invalidResponse("import plan archive SHA-256 is invalid");
  }
  const timeRange = objectData(record.time_range);
  // mirrors archive.ImportTimeRange (internal/archive/import.go); both tags
  // carry omitempty, so the expected list is filtered to what actually arrived
  if (
    !hasExactKeys(
      timeRange,
      ["from", "to"].filter((field) => field in timeRange),
    )
  ) {
    throw invalidResponse("import time range contains unsupported fields");
  }
  const from =
    "from" in timeRange ? canonicalUTCField(timeRange, "from") : null;
  const to = "to" in timeRange ? canonicalUTCField(timeRange, "to") : null;
  if (
    (from === null) !== (to === null) ||
    (from && to && compareCanonicalUTC(from, to) > 0)
  ) {
    throw invalidResponse("import time range is inconsistent");
  }
  const replacement = objectData(record.replacement);
  // mirrors archive.ReplacementImpact (internal/archive/import.go)
  if (!hasExactKeys(replacement, ["counts", "media_bytes", "data_bytes"])) {
    throw invalidResponse(
      "import replacement impact contains unsupported fields",
    );
  }
  if (
    !Array.isArray(record.warnings) ||
    record.warnings.some(
      (warning) => typeof warning !== "string" || warning.length > 2_000,
    )
  ) {
    throw invalidResponse("import warnings are invalid");
  }
  const counts = parseImportCounts(record.counts);
  const mediaBytes = nonNegativeIntegerField(record, "media_bytes");
  const missingAlt = nonNegativeIntegerField(record, "missing_alt");
  const disk = parseImportDisk(record.disk);
  const replacementCounts = parseImportCounts(replacement.counts);
  const replacementMediaBytes = nonNegativeIntegerField(
    replacement,
    "media_bytes",
  );
  const replacementDataBytes = nonNegativeIntegerField(
    replacement,
    "data_bytes",
  );
  const warnings = [...record.warnings];
  const jsonBytes = disk.expandedBytes - mediaBytes;
  const stagingBytes = mediaBytes + jsonBytes + jsonBytes;
  const baseRequired =
    stagingBytes + replacementDataBytes + replacementDataBytes;
  const safetyMargin = Math.max(Math.floor(baseRequired / 10), 2 ** 31);
  const requiredAdditional = baseRequired + safetyMargin;
  const peak = expectedArchiveBytes + replacementDataBytes + requiredAdditional;
  if (
    expectedArchiveBytes <= 0 ||
    counts.entries > 100_000 ||
    counts.comments > 500_000 ||
    counts.media > 100_000 ||
    replacementMediaBytes > replacementDataBytes ||
    mediaBytes > disk.expandedBytes ||
    disk.archiveBytes !== expectedArchiveBytes ||
    disk.expandedBytes <= 0 ||
    disk.stagingBytes !== stagingBytes ||
    disk.preImportBackupBytes !== replacementDataBytes ||
    disk.rollbackReserveBytes !== replacementDataBytes ||
    disk.safetyMarginBytes !== safetyMargin ||
    disk.requiredAdditionalBytes !== requiredAdditional ||
    disk.estimatedPeakFootprintBytes !== peak ||
    !disk.spaceOK ||
    ![stagingBytes, baseRequired, safetyMargin, requiredAdditional, peak].every(
      Number.isSafeInteger,
    )
  ) {
    throw invalidResponse("import preview relationships are invalid");
  }
  if (
    counts.entries === 0
      ? counts.comments !== 0 ||
        missingAlt !== 0 ||
        from !== null ||
        to !== null
      : from === null || to === null
  ) {
    throw invalidResponse("import preview time range is invalid");
  }
  const expectedWarning = `${missingAlt} media usages have no image description`;
  if (
    warnings.length > 1 ||
    (missingAlt === 0
      ? warnings.length !== 0
      : warnings.length !== 1 || warnings[0] !== expectedWarning)
  ) {
    throw invalidResponse("import warnings do not match the preview");
  }
  return {
    formatVersion: 1,
    archiveSHA256,
    counts,
    timeRange: { from, to },
    mediaBytes,
    missingAlt,
    disk,
    replacement: {
      counts: replacementCounts,
      mediaBytes: replacementMediaBytes,
      dataBytes: replacementDataBytes,
    },
    warnings,
  };
}

const importPhases = new Set<ImportPhase>([
  "claimed",
  "backing_up",
  "finalizing",
  "switching",
  "verifying",
  "rolling_back",
  "succeeded",
  "failed",
  "rolled_back",
  "needs_attention",
]);

function validImportOperationId(value: string): boolean {
  const match = value.match(
    /^import-(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})(\d{3})Z-([0-9a-f]{16})$/,
  );
  if (!match) return false;
  const [, year, month, day, hour, minute, second, milliseconds] = match;
  const instant = new Date(
    `${year}-${month}-${day}T${hour}:${minute}:${second}.${milliseconds}Z`,
  );
  return (
    Number.isFinite(instant.valueOf()) &&
    instant.toISOString() ===
      `${year}-${month}-${day}T${hour}:${minute}:${second}.${milliseconds}Z`
  );
}

function parseImportStatus(data: unknown): ImportStatus {
  const record = objectData(data);
  const required = [
    "operation_id",
    "phase",
    "active_generation",
    "previous_generation",
    "new_generation",
    "backup_verified",
    "rollback_attempted",
    "rollback_succeeded",
    "started_at",
    "updated_at",
  ];
  const optional = ["backup_name", "error_code", "completed_at"];
  // mirrors archive.CommitStatus (internal/archive/commit.go)
  if (
    !hasExactKeys(record, [
      ...required,
      ...optional.filter((field) => field in record),
    ])
  ) {
    throw invalidResponse("import status contains unsupported fields");
  }
  const phase = stringField(record, "phase") as ImportPhase;
  if (!importPhases.has(phase)) {
    throw invalidResponse("import status phase is invalid");
  }
  const operationId = stringField(record, "operation_id");
  const activeGeneration = stringField(record, "active_generation");
  const previousGeneration = stringField(record, "previous_generation");
  const newGeneration = stringField(record, "new_generation");
  const generationPattern = /^[A-Za-z0-9_-]{1,128}$/;
  if (
    !validImportOperationId(operationId) ||
    operationId !== newGeneration ||
    !generationPattern.test(activeGeneration) ||
    !generationPattern.test(previousGeneration) ||
    previousGeneration === newGeneration ||
    (activeGeneration !== previousGeneration &&
      activeGeneration !== newGeneration)
  ) {
    throw invalidResponse("import status generation binding is invalid");
  }
  const completedAt =
    "completed_at" in record ? canonicalUTCField(record, "completed_at") : null;
  const completionRequired = ["succeeded", "failed", "rolled_back"].includes(
    phase,
  );
  const completionForbidden = activePhasesForResponse.has(phase);
  if (
    (completionRequired && completedAt === null) ||
    (completionForbidden && completedAt !== null)
  ) {
    throw invalidResponse("import status completion is inconsistent");
  }
  const startedAt = canonicalUTCField(record, "started_at");
  const updatedAt = canonicalUTCField(record, "updated_at");
  if (
    compareCanonicalUTC(updatedAt, startedAt) < 0 ||
    (completedAt && compareCanonicalUTC(completedAt, updatedAt) < 0)
  ) {
    throw invalidResponse("import status timestamps are inconsistent");
  }
  const backupVerified = booleanField(record, "backup_verified");
  const rollbackAttempted = booleanField(record, "rollback_attempted");
  const rollbackSucceeded = booleanField(record, "rollback_succeeded");
  const backupName =
    "backup_name" in record ? stringField(record, "backup_name") : null;
  if (
    backupVerified !== (backupName !== null) ||
    (backupName !== null &&
      (!backupName.endsWith(".zip") ||
        backupName.includes("/") ||
        backupName.includes("\\")))
  ) {
    throw invalidResponse("import status backup proof is invalid");
  }
  const errorCode =
    "error_code" in record ? stringField(record, "error_code") : null;
  if (
    (errorCode !== null && !/^[A-Z0-9_]{1,128}$/.test(errorCode)) ||
    (errorCode === null) !== !("error_code" in record) ||
    (rollbackSucceeded && !rollbackAttempted)
  ) {
    throw invalidResponse("import status rollback proof is invalid");
  }
  const terminalTimeMatches = completedAt === updatedAt;
  const invalidPhase = (() => {
    switch (phase) {
      case "claimed":
      case "backing_up":
        return (
          backupVerified ||
          rollbackAttempted ||
          completedAt !== null ||
          errorCode !== null ||
          activeGeneration !== previousGeneration
        );
      case "finalizing":
        return (
          !backupVerified ||
          rollbackAttempted ||
          completedAt !== null ||
          errorCode !== null ||
          activeGeneration !== previousGeneration
        );
      case "switching":
        return (
          !backupVerified ||
          rollbackAttempted ||
          completedAt !== null ||
          errorCode !== null
        );
      case "verifying":
        return (
          !backupVerified ||
          rollbackAttempted ||
          completedAt !== null ||
          errorCode !== null ||
          activeGeneration !== newGeneration
        );
      case "rolling_back":
        return (
          !backupVerified ||
          !rollbackAttempted ||
          rollbackSucceeded ||
          completedAt !== null ||
          errorCode !== null
        );
      case "succeeded":
        return (
          !backupVerified ||
          rollbackAttempted ||
          errorCode !== null ||
          !terminalTimeMatches ||
          activeGeneration !== newGeneration
        );
      case "failed":
        return (
          rollbackAttempted ||
          errorCode === null ||
          !terminalTimeMatches ||
          activeGeneration !== previousGeneration
        );
      case "rolled_back":
        return (
          !backupVerified ||
          !rollbackAttempted ||
          !rollbackSucceeded ||
          errorCode === null ||
          !terminalTimeMatches ||
          activeGeneration !== previousGeneration
        );
      case "needs_attention":
        return errorCode === null || rollbackSucceeded;
    }
  })();
  if (invalidPhase) {
    throw invalidResponse("import status fields contradict its phase");
  }
  return {
    operationId,
    phase,
    activeGeneration,
    previousGeneration,
    newGeneration,
    backupVerified,
    rollbackAttempted,
    rollbackSucceeded,
    errorCode,
    startedAt,
    updatedAt,
    completedAt,
  };
}

async function decodeImportSuccess(
  response: Response,
  expectedStatus: number,
): Promise<unknown> {
  const text = await response.text();
  assertNoDuplicateJSONKeys(text);
  const replay = new Response(text, {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  });
  const { data, meta } = await decodeResponse(replay, {
    strictSuccessEnvelope: response.ok,
    requireJsonSuccess: response.ok,
  });
  // mirrors the Success envelope's meta (internal/transport/http/gin/response.go)
  const requestID = hasExactKeys(meta, ["request_id"])
    ? stringField(meta, "request_id")
    : "";
  if (
    response.status !== expectedStatus ||
    !/^req_[A-Za-z0-9_-]{1,124}$/.test(requestID)
  ) {
    throw invalidResponse("import response metadata is invalid");
  }
  return data;
}

const activePhasesForResponse = new Set<ImportPhase>([
  "claimed",
  "backing_up",
  "finalizing",
  "switching",
  "verifying",
  "rolling_back",
]);

function assertNoDuplicateJSONKeys(source: string): void {
  let index = 0;
  const whitespace = () => {
    while (/\s/.test(source[index] ?? "")) index += 1;
  };
  const stringToken = (): string => {
    if (source[index] !== '"') throw invalidResponse("JSON string is invalid");
    const start = index;
    index += 1;
    while (index < source.length) {
      const character = source[index];
      if (character === '"') {
        index += 1;
        try {
          return JSON.parse(source.slice(start, index)) as string;
        } catch {
          throw invalidResponse("JSON string is invalid");
        }
      }
      if (character === "\\") index += 1;
      index += 1;
    }
    throw invalidResponse("JSON string is incomplete");
  };
  const value = (): void => {
    whitespace();
    if (source[index] === "{") {
      index += 1;
      whitespace();
      const keys = new Set<string>();
      if (source[index] === "}") {
        index += 1;
        return;
      }
      while (index < source.length) {
        const key = stringToken();
        if (keys.has(key))
          throw invalidResponse("JSON object has duplicate keys");
        keys.add(key);
        whitespace();
        if (source[index] !== ":")
          throw invalidResponse("JSON object is invalid");
        index += 1;
        value();
        whitespace();
        if (source[index] === "}") {
          index += 1;
          return;
        }
        if (source[index] !== ",")
          throw invalidResponse("JSON object is invalid");
        index += 1;
        whitespace();
      }
      throw invalidResponse("JSON object is incomplete");
    }
    if (source[index] === "[") {
      index += 1;
      whitespace();
      if (source[index] === "]") {
        index += 1;
        return;
      }
      while (index < source.length) {
        value();
        whitespace();
        if (source[index] === "]") {
          index += 1;
          return;
        }
        if (source[index] !== ",")
          throw invalidResponse("JSON array is invalid");
        index += 1;
      }
      throw invalidResponse("JSON array is incomplete");
    }
    if (source[index] === '"') {
      stringToken();
      return;
    }
    const match = source
      .slice(index)
      .match(
        /^(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/,
      );
    if (!match) throw invalidResponse("JSON value is invalid");
    index += match[0].length;
  };
  value();
  whitespace();
  if (index !== source.length) throw invalidResponse("JSON has trailing data");
}

function uploadArchive(
  path: string,
  csrfToken: string,
  file: File,
  expectedSHA256: string | null,
  onProgress: (percent: number | null) => void,
  expectedStatus: number,
  signal?: AbortSignal,
): Promise<unknown> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(
        new PublicApiError("archive upload was cancelled", {
          code: "CANCELLED",
          status: 0,
        }),
      );
      return;
    }
    const request = new XMLHttpRequest();
    let settled = false;
    const finish = (action: () => void) => {
      if (settled) return;
      settled = true;
      signal?.removeEventListener("abort", abort);
      action();
    };
    const abort = () => request.abort();
    request.open("POST", path);
    request.withCredentials = true;
    request.setRequestHeader("Accept", "application/json");
    request.setRequestHeader("Content-Type", "application/zip");
    request.setRequestHeader("X-CSRF-Token", csrfToken);
    if (expectedSHA256) {
      request.setRequestHeader("X-Moment-Archive-SHA256", expectedSHA256);
    }
    request.upload.addEventListener("progress", (event) => {
      if (event.lengthComputable && event.total > 0) {
        onProgress(
          Math.min(99, Math.round((event.loaded / event.total) * 100)),
        );
      } else {
        onProgress(null);
      }
    });
    request.addEventListener("error", () =>
      finish(() =>
        reject(
          new PublicApiError("archive upload connection was lost", {
            code: "NETWORK_ERROR",
            status: 0,
          }),
        ),
      ),
    );
    request.addEventListener("abort", () =>
      finish(() =>
        reject(
          new PublicApiError("archive upload was cancelled", {
            code: "CANCELLED",
            status: 0,
          }),
        ),
      ),
    );
    request.addEventListener("load", () => {
      if (
        request.status < 200 ||
        request.status > 599 ||
        request.status === 204 ||
        request.status === 205 ||
        request.status === 304
      ) {
        finish(() =>
          reject(invalidResponse("archive upload response status is invalid")),
        );
        return;
      }
      const headers = new Headers();
      const contentType = request.getResponseHeader("Content-Type");
      if (contentType) headers.set("Content-Type", contentType);
      let response: Response;
      try {
        response = new Response(request.responseText, {
          status: request.status,
          headers,
        });
      } catch {
        finish(() =>
          reject(invalidResponse("archive upload response is invalid")),
        );
        return;
      }
      void decodeImportSuccess(response, expectedStatus)
        .then((data) => finish(() => resolve(data)))
        .catch((error: unknown) => finish(() => reject(error)));
    });
    signal?.addEventListener("abort", abort, { once: true });
    onProgress(null);
    request.send(file);
  });
}

async function decodeExactOwnerSuccess(
  response: Response,
  dataKeys: string[],
  optionalDataKeys: string[] = [],
): Promise<Record<string, unknown>> {
  const result = await decodeResponse(response, {
    strictSuccessEnvelope: response.ok,
    requireJsonSuccess: response.ok,
  });
  if (response.status !== 200) {
    throw invalidResponse("owner settings success must return HTTP 200");
  }
  // mirrors the Success envelope's meta (internal/transport/http/gin/response.go)
  if (
    !hasExactKeys(result.meta, ["request_id"]) ||
    !stringField(result.meta, "request_id")
  ) {
    throw invalidResponse("owner settings metadata is invalid");
  }
  const data = objectData(result.data);
  const expectedDataKeys = [
    ...dataKeys,
    ...optionalDataKeys.filter((key) => key in data),
  ];
  // mirrors ginhttp.identityView (internal/transport/http/gin/accounts.go)
  if (!hasExactKeys(data, expectedDataKeys)) {
    throw invalidResponse("owner settings data contains unsupported fields");
  }
  return data;
}

function parseAccountIdentity(value: unknown): AccountIdentity {
  // mirrors ginhttp.identityView (internal/transport/http/gin/accounts.go)
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      "provider",
      "display_name",
      "email",
      "linked_at",
      "last_login_at",
    ])
  ) {
    throw invalidResponse("account identity contains unsupported fields");
  }
  const provider = stringField(value, "provider");
  if (!/^[a-z][a-z0-9_]{0,31}$/.test(provider)) {
    throw invalidResponse("account identity provider is invalid");
  }
  return {
    provider,
    // A provider that published no name, and an address the server will not
    // send to, both arrive as empty strings. That is something to render
    // around, never a reason to reject the response.
    displayName: stringField(value, "display_name"),
    email: stringField(value, "email"),
    linkedAt: utcInstantField(value, "linked_at"),
    lastLoginAt: utcInstantField(value, "last_login_at"),
  };
}

function parseAccountSessionEntry(value: unknown): AccountSessionEntry {
  // mirrors ginhttp.sessionListItem (internal/transport/http/gin/accounts.go)
  if (
    !isRecord(value) ||
    !hasExactKeys(value, [
      "id",
      "issued_at",
      "last_seen_at",
      "expires_at",
      "surface",
      "current",
    ])
  ) {
    throw invalidResponse("account session contains unsupported fields");
  }
  const id = stringField(value, "id");
  if (!id) throw invalidResponse("account session id cannot be empty");
  return {
    id,
    issuedAt: utcInstantField(value, "issued_at"),
    lastSeenAt: utcInstantField(value, "last_seen_at"),
    expiresAt: utcInstantField(value, "expires_at"),
    surface: stringField(value, "surface"),
    current: booleanField(value, "current"),
  };
}

// mirrors ginhttp.accountLimitsView (internal/transport/http/gin/accounts.go)
function parseAccountLimits(value: unknown): AccountLimits {
  const record = objectData(value);
  if (
    !hasExactKeys(record, [
      "profile_picture_bytes",
      "display_name_length",
      "bio_length",
    ])
  ) {
    throw invalidResponse("account limits contain unsupported fields");
  }
  return {
    profilePictureBytes: positiveIntegerField(record, "profile_picture_bytes"),
    displayNameLength: positiveIntegerField(record, "display_name_length"),
    bioLength: positiveIntegerField(record, "bio_length"),
  };
}

function accountList(
  data: unknown,
  subject: string,
): Record<string, unknown>[] {
  if (!Array.isArray(data)) {
    throw invalidResponse(`${subject} must be an array`);
  }
  return data as Record<string, unknown>[];
}

function accountProfilePayload(
  update: AccountProfileUpdate,
): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  if (update.username !== undefined) payload.username = update.username;
  if (update.displayName !== undefined) {
    payload.display_name = update.displayName;
  }
  if (update.bio !== undefined) payload.bio = update.bio;
  if (update.avatarMediaId !== undefined) {
    payload.avatar_media_id = update.avatarMediaId;
  }
  if (update.coverMediaId !== undefined) {
    payload.cover_media_id = update.coverMediaId;
  }
  return payload;
}

function sitePayload(update: SiteSettingsUpdate): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  if (update.username !== undefined) payload.username = update.username;
  if (update.displayName !== undefined)
    payload.display_name = update.displayName;
  if (update.title !== undefined) payload.title = update.title;
  if (update.bio !== undefined) payload.bio = update.bio;
  if (update.timezone !== undefined) payload.timezone = update.timezone;
  if (update.publicBaseUrl !== undefined) {
    payload.public_base_url = update.publicBaseUrl;
  }
  if (update.avatarMediaId !== undefined) {
    payload.avatar_media_id = update.avatarMediaId;
  }
  if (update.coverMediaId !== undefined) {
    payload.cover_media_id = update.coverMediaId;
  }
  if (update.commentsEnabled !== undefined) {
    payload.comments_enabled = update.commentsEnabled;
  }
  return payload;
}

function mediaPayload(media: PostMediaInput[]): Record<string, unknown>[] {
  return media.map((usage, position) => ({
    media_id: usage.mediaId,
    position,
    alt_text: usage.altText,
  }));
}

function nullableNumberField(
  record: Record<string, unknown>,
  field: string,
): number | null {
  const value = record[field];
  if (
    value !== null &&
    (typeof value !== "number" || !Number.isFinite(value))
  ) {
    throw invalidResponse(`response field ${field} must be a number or null`);
  }
  return value as number | null;
}

function parseOwnerLocation(value: unknown): OwnerLocation {
  if (!isRecord(value)) throw invalidResponse("location must be an object");
  const keys = [
    "name",
    "display_name",
    "country_code",
    "country",
    "region",
    "city",
    "district",
    "category",
    "provider",
    "provider_place_id",
    "latitude",
    "longitude",
    "coordinate_system",
  ];
  // mirrors the caller's Go type; see each exactObject call
  if (!hasExactKeys(value, keys)) {
    throw invalidResponse("location contains unsupported fields");
  }
  const provider = stringField(value, "provider");
  if (!["manual", "amap", "geoapify"].includes(provider)) {
    throw invalidResponse("location provider is invalid");
  }
  const coordinate = value.coordinate_system;
  if (coordinate !== null && coordinate !== "wgs84" && coordinate !== "gcj02") {
    throw invalidResponse("location coordinate system is invalid");
  }
  const latitude = nullableNumberField(value, "latitude");
  const longitude = nullableNumberField(value, "longitude");
  if (
    (latitude === null) !== (longitude === null) ||
    (latitude === null) !== (coordinate === null)
  ) {
    throw invalidResponse(
      "location coordinate fields must be supplied together",
    );
  }
  return {
    name: stringField(value, "name"),
    displayName: stringField(value, "display_name"),
    countryCode: stringField(value, "country_code"),
    country: stringField(value, "country"),
    region: stringField(value, "region"),
    city: stringField(value, "city"),
    district: stringField(value, "district"),
    category: stringField(value, "category"),
    provider: provider as LocationProvider,
    providerPlaceId: stringField(value, "provider_place_id"),
    latitude,
    longitude,
    coordinateSystem: coordinate as CoordinateSystem | null,
  };
}

function locationPayload(location: OwnerLocation): Record<string, unknown> {
  return {
    name: location.name,
    display_name: location.displayName,
    country_code: location.countryCode,
    country: location.country,
    region: location.region,
    city: location.city,
    district: location.district,
    category: location.category,
    provider: location.provider,
    provider_place_id: location.providerPlaceId,
    latitude: location.latitude,
    longitude: location.longitude,
    coordinate_system: location.coordinateSystem,
  };
}

function requestInit(
  method: string,
  csrfToken?: string,
  body?: unknown,
): RequestInit {
  const headers = new Headers({ Accept: "application/json" });
  if (body !== undefined) headers.set("Content-Type", "application/json");
  if (csrfToken) headers.set("X-CSRF-Token", csrfToken);
  return {
    method,
    credentials: "same-origin",
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  };
}

export class OwnerApi {
  private readonly fetcher: Fetcher;

  constructor(fetcher: Fetcher = window.fetch.bind(window)) {
    this.fetcher = fetcher;
  }

  async exchange(
    credential: string,
    method: "password" | "token" = "password",
  ): Promise<OwnerSession> {
    const response = await this.fetcher(
      "/api/v1/owner/session",
      requestInit("POST", undefined, { [method]: credential }),
    );
    const { data } = await decodeResponse(response);
    const record = objectData(data);
    const expiresAt = stringField(record, "expires_at");
    if (!Number.isFinite(Date.parse(expiresAt))) {
      throw invalidResponse("response field expires_at must be an instant");
    }
    const csrfToken = stringField(record, "csrf_token");
    if (!csrfToken) throw invalidResponse("csrf_token cannot be empty");
    return { csrfToken, expiresAt };
  }

  async loginOptions(signal?: AbortSignal): Promise<OwnerLoginOptions> {
    const response = await this.fetcher("/api/v1/owner/login-options", {
      ...requestInit("GET"),
      signal,
    });
    const record = objectData((await decodeResponse(response)).data);
    // mirrors the map built by ginhttp.loginOptions (internal/transport/http/gin/owner_auth.go)
    if (!hasExactKeys(record, ["password_enabled"])) {
      throw invalidResponse("login options response has unexpected fields");
    }
    return { passwordEnabled: booleanField(record, "password_enabled") };
  }

  // Third-party sign-in is advertised through its own endpoint so that the
  // password/token login options above stay a separate contract.
  async loginProviders(signal?: AbortSignal): Promise<OAuthLoginProvider[]> {
    const response = await this.fetcher("/api/v1/login/oauth/providers", {
      ...requestInit("GET"),
      signal,
    });
    const data = await decodeExactOwnerSuccess(response, ["providers"]);
    const providers = data.providers;
    if (!Array.isArray(providers)) {
      throw invalidResponse("login providers must be an array");
    }
    return providers.map((entry) => {
      // mirrors oauth.Descriptor (internal/auth/oauth/provider.go)
      if (!isRecord(entry) || !hasExactKeys(entry, ["name", "label"])) {
        throw invalidResponse("login provider contains unsupported fields");
      }
      const name = stringField(entry, "name");
      const label = stringField(entry, "label");
      if (!/^[a-z][a-z0-9_]{0,31}$/.test(name) || !label.trim()) {
        throw invalidResponse("login provider identity is invalid");
      }
      return { name, label };
    });
  }

  /**
   * Read what the session cookie is currently worth.
   *
   * Safe method, so it needs no CSRF token -- which is what makes it the only
   * way a browser can pick up a session it never exchanged itself, the way an
   * OAuth callback leaves one behind.
   */
  async accountSession(signal?: AbortSignal): Promise<AccountSession> {
    const response = await this.fetcher("/api/v1/account/session", {
      ...requestInit("GET"),
      signal,
    });
    const record = objectData((await decodeResponse(response)).data);
    // mirrors ginhttp.accountSessionView (internal/transport/http/gin/accounts.go)
    if (
      !hasExactKeys(record, ["expires_at", "csrf_token", "account", "limits"])
    ) {
      throw invalidResponse("account session contains unsupported fields");
    }
    const expiresAt = stringField(record, "expires_at");
    if (!Number.isFinite(Date.parse(expiresAt))) {
      throw invalidResponse("response field expires_at must be an instant");
    }
    const csrfToken = stringField(record, "csrf_token");
    if (!csrfToken) throw invalidResponse("csrf_token cannot be empty");
    return {
      csrfToken,
      expiresAt,
      account: parsePrivateAccount(record.account),
      limits: parseAccountLimits(record.limits),
    };
  }

  async updateAccountProfile(
    csrfToken: string,
    update: AccountProfileUpdate,
  ): Promise<Account> {
    const response = await this.fetcher(
      "/api/v1/account/profile",
      requestInit("PATCH", csrfToken, accountProfilePayload(update)),
    );
    const { data } = await decodeResponse(response);
    return parsePrivateAccount(data);
  }

  /**
   * Put a picture on this account and keep it, in a single request.
   *
   * The owner's flow is two steps -- ingest into the media store, then point
   * the site row at what came back -- because owner media has a life of its
   * own that a post may also reference. An account picture has no such life:
   * the endpoint ingests and binds together, so there is no window in which
   * bytes sit on disk attached to nothing, and nothing to undo when a browser
   * walks away half way through an edit.
   *
   * Plain `fetch` rather than the XHR upload path on purpose. That path exists
   * to report progress and to parse an `UploadedMedia`; this returns an
   * account, and a picture small enough to be capped at 8 MiB finishes before
   * a progress bar would say anything a spinner does not.
   */
  private async uploadAccountPicture(
    kind: "avatar" | "cover",
    csrfToken: string,
    file: File,
    signal?: AbortSignal,
  ): Promise<Account> {
    const body = new FormData();
    body.append("file", file, file.name);
    const response = await this.fetcher(`/api/v1/account/${kind}`, {
      method: "POST",
      credentials: "same-origin",
      // Content-Type is left to the browser deliberately: only it knows the
      // multipart boundary it just generated, and naming the media type
      // without that boundary makes the body unreadable to the server.
      headers: { Accept: "application/json", "X-CSRF-Token": csrfToken },
      body,
      signal,
    });
    const { data } = await decodeResponse(response);
    return parsePrivateAccount(data);
  }

  async uploadAccountAvatar(
    csrfToken: string,
    file: File,
    signal?: AbortSignal,
  ): Promise<Account> {
    return this.uploadAccountPicture("avatar", csrfToken, file, signal);
  }

  async uploadAccountCover(
    csrfToken: string,
    file: File,
    signal?: AbortSignal,
  ): Promise<Account> {
    return this.uploadAccountPicture("cover", csrfToken, file, signal);
  }

  async accountIdentities(signal?: AbortSignal): Promise<AccountIdentity[]> {
    const response = await this.fetcher("/api/v1/account/identities", {
      ...requestInit("GET"),
      signal,
    });
    const { data } = await decodeResponse(response);
    return accountList(data, "account identities").map(parseAccountIdentity);
  }

  async unlinkIdentity(csrfToken: string, provider: string): Promise<void> {
    const response = await this.fetcher(
      `/api/v1/account/identities/${encodeURIComponent(provider)}`,
      requestInit("DELETE", csrfToken),
    );
    const record = objectData((await decodeResponse(response)).data);
    // mirrors the map built by ginhttp.unlinkOwnIdentity (internal/transport/http/gin/accounts.go)
    if (
      !hasExactKeys(record, ["unlinked"]) ||
      stringField(record, "unlinked") !== provider
    ) {
      throw invalidResponse("unlink response named a different provider");
    }
  }

  async accountSessions(signal?: AbortSignal): Promise<AccountSessionEntry[]> {
    const response = await this.fetcher("/api/v1/account/sessions", {
      ...requestInit("GET"),
      signal,
    });
    const { data } = await decodeResponse(response);
    return accountList(data, "account sessions").map(parseAccountSessionEntry);
  }

  /** Sign every browser out, this one included. Returns how many were ended. */
  async revokeAccountSessions(csrfToken: string): Promise<number> {
    const response = await this.fetcher(
      "/api/v1/account/sessions",
      requestInit("DELETE", csrfToken),
    );
    const record = objectData((await decodeResponse(response)).data);
    // mirrors the map built by ginhttp.revokeOwnSessions (internal/transport/http/gin/accounts.go)
    if (!hasExactKeys(record, ["revoked"])) {
      throw invalidResponse("session revocation contains unsupported fields");
    }
    return nonNegativeIntegerField(record, "revoked");
  }

  async ownerAccounts(signal?: AbortSignal): Promise<Account[]> {
    const response = await this.fetcher("/api/v1/owner/accounts", {
      ...requestInit("GET"),
      signal,
    });
    const { data } = await decodeResponse(response);
    return accountList(data, "accounts").map(parsePrivateAccount);
  }

  async updateAccountStatus(
    csrfToken: string,
    userId: string,
    status: AccountStatus,
  ): Promise<Account> {
    const response = await this.fetcher(
      `/api/v1/owner/accounts/${encodeURIComponent(userId)}`,
      requestInit("PATCH", csrfToken, { status }),
    );
    const { data } = await decodeResponse(response);
    return parsePrivateAccount(data);
  }

  async status(signal?: AbortSignal): Promise<OwnerStatus> {
    const response = await this.fetcher("/api/v1/owner/status", {
      ...requestInit("GET"),
      signal,
    });
    const preliminary = await decodeExactOwnerSuccess(
      response,
      ["initialized", "password_enabled", "session_epoch", "initialized_at"],
      ["rotated_at"],
    );
    if (booleanField(preliminary, "initialized") !== true) {
      throw invalidResponse("owner status must be initialized");
    }
    return {
      initialized: true,
      passwordEnabled: booleanField(preliminary, "password_enabled"),
      sessionEpoch: positiveIntegerField(preliminary, "session_epoch"),
      initializedAt: utcInstantField(preliminary, "initialized_at"),
      rotatedAt: optionalInstantField(preliminary, "rotated_at"),
    };
  }

  async setPassword(csrfToken: string, password: string): Promise<OwnerStatus> {
    const response = await this.fetcher(
      "/api/v1/owner/password",
      requestInit("PUT", csrfToken, { password }),
    );
    const record = await decodeExactOwnerSuccess(
      response,
      ["initialized", "password_enabled", "session_epoch", "initialized_at"],
      ["rotated_at"],
    );
    return {
      initialized: booleanField(record, "initialized"),
      passwordEnabled: booleanField(record, "password_enabled"),
      sessionEpoch: positiveIntegerField(record, "session_epoch"),
      initializedAt: utcInstantField(record, "initialized_at"),
      rotatedAt: optionalInstantField(record, "rotated_at"),
    };
  }

  async updateSite(
    csrfToken: string,
    update: SiteSettingsUpdate,
  ): Promise<SiteIdentity> {
    const response = await this.fetcher(
      "/api/v1/owner/site",
      requestInit("PATCH", csrfToken, sitePayload(update)),
    );
    const data = await decodeExactOwnerSuccess(response, [
      "owner_id",
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

  async rotateToken(csrfToken: string): Promise<RotatedCredential> {
    const response = await this.fetcher(
      "/api/v1/owner/token/rotate",
      requestInit("POST", csrfToken),
    );
    const record = await decodeExactOwnerSuccess(response, [
      "token",
      "initialized",
      "session_epoch",
      "initialized_at",
      "rotated_at",
    ]);
    const token = stringField(record, "token");
    const rotatedAt = utcInstantField(record, "rotated_at");
    if (!token) throw invalidResponse("rotated owner token cannot be empty");
    if (booleanField(record, "initialized") !== true) {
      throw invalidResponse("rotated owner credential must be initialized");
    }
    return {
      token,
      initialized: true,
      sessionEpoch: positiveIntegerField(record, "session_epoch"),
      initializedAt: utcInstantField(record, "initialized_at"),
      rotatedAt,
    };
  }

  async planImport(
    csrfToken: string,
    file: File,
    onProgress: (percent: number | null) => void = () => undefined,
    signal?: AbortSignal,
  ): Promise<ImportPlan> {
    const data = await uploadArchive(
      "/api/v1/owner/import/plan",
      csrfToken,
      file,
      null,
      onProgress,
      200,
      signal,
    );
    onProgress(100);
    return parseImportPlan(data, file.size);
  }

  async restoreImport(
    csrfToken: string,
    file: File,
    expectedSHA256: string,
    onProgress: (percent: number | null) => void = () => undefined,
    signal?: AbortSignal,
  ): Promise<ImportStatus> {
    if (!/^[0-9a-f]{64}$/.test(expectedSHA256)) {
      throw invalidResponse("expected archive SHA-256 is invalid");
    }
    const data = objectData(
      await uploadArchive(
        "/api/v1/owner/import/restore",
        csrfToken,
        file,
        expectedSHA256,
        onProgress,
        200,
        signal,
      ),
    );
    // mirrors archive.CommitResult (internal/archive/commit.go)
    if (!hasExactKeys(data, ["status"])) {
      throw invalidResponse("restore result contains unsupported fields");
    }
    onProgress(100);
    const status = parseImportStatus(data.status);
    if (status.phase !== "succeeded") {
      throw invalidResponse(
        "restore response is not a successful terminal state",
      );
    }
    return status;
  }

  async planMergeImport(
    csrfToken: string,
    file: File,
    onProgress: (percent: number | null) => void = () => undefined,
    signal?: AbortSignal,
  ): Promise<MergeInspection> {
    const data = await uploadArchive(
      "/api/v1/owner/import/merge/plan",
      csrfToken,
      file,
      null,
      onProgress,
      201,
      signal,
    );
    onProgress(100);
    return parseMergeInspection(data);
  }

  async reviewImport(
    csrfToken: string,
    file: File,
    onProgress: (percent: number | null) => void = () => undefined,
    signal?: AbortSignal,
  ): Promise<MergeInspection> {
    const data = await uploadArchive(
      "/api/v1/owner/import/review",
      csrfToken,
      file,
      null,
      onProgress,
      201,
      signal,
    );
    onProgress(100);
    return parseMergeInspection(data);
  }

  async commitReviewedReplacement(
    csrfToken: string,
    stagingToken: string,
    planID: string,
    signal?: AbortSignal,
  ): Promise<ImportStatus> {
    const response = await this.fetcher("/api/v1/owner/import/review/replace", {
      ...requestInit("POST", csrfToken, {
        staging_token: stagingToken,
        plan_id: planID,
      }),
      signal,
    });
    const data = objectData(await decodeImportSuccess(response, 200));
    // mirrors archive.CommitResult (internal/archive/commit.go)
    if (!hasExactKeys(data, ["status"])) {
      throw invalidResponse(
        "reviewed replacement result contains unsupported fields",
      );
    }
    const status = parseImportStatus(data.status);
    if (status.phase !== "succeeded") {
      throw invalidResponse(
        "reviewed replacement is not a successful terminal state",
      );
    }
    return status;
  }

  async loadImportReviewMedia(
    csrfToken: string,
    stagingToken: string,
    planID: string,
    source: MergeReviewSource,
    mediaID: string,
    signal?: AbortSignal,
  ): Promise<Blob> {
    const response = await this.fetcher("/api/v1/owner/import/review/media", {
      ...requestInit("POST", csrfToken, {
        staging_token: stagingToken,
        plan_id: planID,
        source,
        media_id: mediaID,
      }),
      signal,
    });
    if (!response.ok) {
      await decodeImportSuccess(response, 200);
    }
    const contentType = response.headers.get("Content-Type") ?? "";
    if (response.status !== 200 || !contentType.startsWith("image/")) {
      throw invalidResponse("review media response is invalid");
    }
    return response.blob();
  }

  async commitMergeImport(
    csrfToken: string,
    stagingToken: string,
    selection: MergeSelection,
    signal?: AbortSignal,
  ): Promise<ImportStatus> {
    const response = await this.fetcher("/api/v1/owner/import/merge/commit", {
      ...requestInit("POST", csrfToken, {
        staging_token: stagingToken,
        selection,
      }),
      signal,
    });
    const data = objectData(await decodeImportSuccess(response, 200));
    // mirrors archive.CommitResult (internal/archive/commit.go)
    if (!hasExactKeys(data, ["status"])) {
      throw invalidResponse("merge result contains unsupported fields");
    }
    const status = parseImportStatus(data.status);
    if (status.phase !== "succeeded") {
      throw invalidResponse(
        "merge response is not a successful terminal state",
      );
    }
    return status;
  }

  async discardMergeImport(
    csrfToken: string,
    stagingToken: string,
    signal?: AbortSignal,
    keepalive = false,
  ): Promise<void> {
    const response = await this.fetcher("/api/v1/owner/import/merge/discard", {
      ...requestInit("POST", csrfToken, { staging_token: stagingToken }),
      keepalive,
      signal,
    });
    const data = objectData(await decodeImportSuccess(response, 200));
    // mirrors the gin.H built by ginhttp.discardMergeImport (internal/transport/http/gin/import.go)
    if (!hasExactKeys(data, ["discarded"]) || data.discarded !== true) {
      throw invalidResponse("merge discard result is invalid");
    }
  }

  async importStatus(signal?: AbortSignal): Promise<ImportStatus> {
    const response = await this.fetcher("/api/v1/owner/import/status", {
      ...requestInit("GET"),
      signal,
    });
    return parseImportStatus(await decodeImportSuccess(response, 200));
  }

  async logout(csrfToken: string): Promise<void> {
    const response = await this.fetcher(
      "/api/v1/owner/session",
      requestInit("DELETE", csrfToken),
    );
    const { data } = await decodeResponse(response);
    const record = objectData(data);
    if (record.logged_out !== true) {
      throw invalidResponse("logout response was not confirmed");
    }
  }

  async searchLocations(
    query: string,
    bias?: LocationBias,
    signal?: AbortSignal,
  ): Promise<OwnerLocation[]> {
    const params = new URLSearchParams({ q: query });
    if (bias) {
      params.set("latitude", bias.latitude.toString());
      params.set("longitude", bias.longitude.toString());
    }
    return this.locations(`/api/v1/owner/locations/search?${params}`, signal);
  }

  async nearbyLocations(
    point: LocationBias,
    signal?: AbortSignal,
  ): Promise<OwnerLocation[]> {
    const params = new URLSearchParams({
      latitude: point.latitude.toString(),
      longitude: point.longitude.toString(),
    });
    return this.locations(`/api/v1/owner/locations/nearby?${params}`, signal);
  }

  private async locations(
    endpoint: string,
    signal?: AbortSignal,
  ): Promise<OwnerLocation[]> {
    const response = await this.fetcher(endpoint, {
      ...requestInit("GET"),
      signal,
    });
    const { data, meta } = await decodeResponse(response);
    // mirrors the Success envelope's meta (internal/transport/http/gin/response.go)
    if (!hasExactKeys(meta, ["request_id"])) {
      throw invalidResponse("location metadata is invalid");
    }
    const record = objectData(data);
    // mirrors locationdto.Results (internal/transport/locationdto/dto.go)
    if (!hasExactKeys(record, ["items"]) || !Array.isArray(record.items)) {
      throw invalidResponse("location results are invalid");
    }
    return record.items.map(parseOwnerLocation);
  }

  /**
   * Unfurl one URL into a card snapshot.
   *
   * Only an owner can make the server request an arbitrary address, and the
   * server keeps every safety bound; the browser never contacts the origin
   * itself.
   */
  async unfurlLink(url: string, signal?: AbortSignal): Promise<OwnerLinkCard> {
    const params = new URLSearchParams({ url });
    const response = await this.fetcher(
      `/api/v1/owner/links/unfurl?${params}`,
      { ...requestInit("GET"), signal },
    );
    const { data } = await decodeResponse(response);
    return parseOwnerLinkCard(data);
  }

  async create(
    csrfToken: string,
    contentMd: string,
    idempotencyKey: string,
    media: PostMediaInput[] = [],
    location?: OwnerLocation | null,
    quotedPostId?: string,
    linkCards?: OwnerLinkCard[] | null,
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
    const init = requestInit("POST", csrfToken, body);
    const headers = new Headers(init.headers);
    headers.set("Idempotency-Key", idempotencyKey);
    const response = await this.fetcher("/api/v1/owner/posts", {
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
    location?: OwnerLocation | null,
    linkCards?: OwnerLinkCard[] | null,
  ): Promise<PublicPost> {
    const body: Record<string, unknown> = { content_md: contentMd };
    if (media) body.media = mediaPayload(media);
    if (location !== undefined) {
      body.location = location === null ? null : locationPayload(location);
    }
    if (linkCards !== undefined) body.link_cards = linkCards ?? [];
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(id)}`,
      requestInit("PATCH", csrfToken, body),
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async uploadMedia(
    csrfToken: string,
    file: File,
    onProgress: (percent: number) => void = () => undefined,
    signal?: AbortSignal,
    onProcessingPoster?: (objectUrl: string) => void,
  ): Promise<UploadedMedia> {
    return uploadMediaXHR(
      "/api/v1/owner/media",
      file,
      onProgress,
      signal,
      {
        "X-CSRF-Token": csrfToken,
      },
      onProcessingPoster,
    );
  }

  async delete(csrfToken: string, id: string): Promise<PublicPost> {
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(id)}`,
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
      `/api/v1/owner/posts/${encodeURIComponent(id)}/repost`,
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
      `/api/v1/owner/posts/${encodeURIComponent(id)}/pin`,
      requestInit(method, csrfToken),
    );
    const data = await decodeExactOwnerSuccess(response, [
      "owner_id",
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

  /**
   * `replyToCommentId` is an annotation, not a tree edge: it names the one
   * comment this answers, while `parentId` still decides where the row sits.
   * Only a reply may carry one, and only to its own root or a sibling under it.
   */
  async createComment(
    csrfToken: string,
    postId: string,
    contentMd: string,
    parentId: string | null,
    idempotencyKey: string,
    media: PostMediaInput[] = [],
    location: OwnerLocation | null = null,
    replyToCommentId: string | null = null,
  ): Promise<CreatedComment> {
    const init = requestInit("POST", csrfToken, {
      content_md: contentMd,
      parent_id: parentId,
      reply_to_comment_id: replyToCommentId,
      media: mediaPayload(media),
      location: location === null ? null : locationPayload(location),
    });
    const headers = new Headers(init.headers);
    headers.set("Idempotency-Key", idempotencyKey);
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(postId)}/comments`,
      { ...init, headers },
    );
    return decodeCreatedComment(response, postId, {
      parentId,
      authorKind: "owner",
    });
  }

  async deleteComment(
    csrfToken: string,
    postId: string,
    commentId: string,
  ): Promise<DeletedComment> {
    const response = await this.fetcher(
      `/api/v1/owner/comments/${encodeURIComponent(commentId)}`,
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
      `/api/v1/owner/posts/${encodeURIComponent(postId)}/comments/${encodeURIComponent(commentId)}/pin`,
      requestInit(method, csrfToken),
    );
    const data = await decodeExactOwnerSuccess(response, [
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
