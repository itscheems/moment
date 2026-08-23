/**
 * The archive payload shapes the server actually sends, in one place.
 *
 * The owner API validates every response against an exact key set, so a
 * fixture that is a field behind the server does not fail -- it passes, while
 * the real page throws on every real response. That is precisely how the
 * `users` and `identities` dimensions reached production with five green
 * hand-written counts objects standing behind them.
 *
 * So the fixtures share one factory. When the server grows a sixth dimension,
 * this file is the only place that has to learn about it, and every test that
 * decodes an archive starts exercising the new shape at once instead of five
 * transcriptions drifting apart.
 */

/** An archive counts object as it appears on the wire. */
export interface ImportCountsWire {
  entries: number;
  comments: number;
  media: number;
  users: number;
  identities: number;
}

/**
 * Counts in the current wire shape.
 *
 * Accounts default to one because every archive carries at least the owner;
 * a fixture claiming zero accounts describes an archive that cannot exist.
 */
export function makeImportCounts(
  overrides: Partial<ImportCountsWire> = {},
): ImportCountsWire {
  return {
    entries: 0,
    comments: 0,
    media: 0,
    users: 1,
    identities: 1,
    ...overrides,
  };
}

/**
 * One merge review entity, in the shape the matching Go projection emits.
 *
 * `MergeReviewEntity` is one struct serving four kinds, and every field beyond
 * `id`, `content_md`, `created_at` and `media` carries `omitempty` -- so the
 * key set on the wire depends on which projection built the row and on which
 * of its nullable columns were set. Four factories rather than one because
 * that is four different shapes, and a fixture that blends them describes a
 * response the server cannot produce.
 *
 * Note `content_md` on the two account shapes: the Go field is a plain string
 * with no `omitempty`, so an account row still carries it, empty. Fixtures
 * that leave it out would be testing a response that never arrives.
 */
export type ReviewEntityWire = Record<string, unknown>;

/** What `archive.reviewEntry` emits (internal/archive/merge_review.go). */
export function makeReviewEntry(
  overrides: ReviewEntityWire = {},
): ReviewEntityWire {
  return {
    id: "post-1",
    content_md: "一条 Moment",
    kind: "moment",
    created_at: "2026-08-10T00:00:00Z",
    updated_at: "2026-08-10T00:00:00Z",
    published_at: "2026-08-10T00:00:00Z",
    quote_unavailable: false,
    media: [],
    ...overrides,
  };
}

/** What `archive.reviewComment` emits. */
export function makeReviewComment(
  overrides: ReviewEntityWire = {},
): ReviewEntityWire {
  return {
    id: "comment-1",
    content_md: "一条回应",
    entry_id: "post-1",
    author_kind: "guest",
    author_name: "路人",
    pinned: false,
    created_at: "2026-08-11T00:00:00Z",
    media: [],
    ...overrides,
  };
}

/** What `archive.reviewUser` emits. */
export function makeReviewUser(
  overrides: ReviewEntityWire = {},
): ReviewEntityWire {
  return {
    id: "11111111111111111111111111111111",
    content_md: "",
    username: "linlin",
    display_name: "林林",
    bio: "在这里记下路过的天气",
    role: "member",
    status: "active",
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-09T00:00:00Z",
    media: [],
    ...overrides,
  };
}

/** What `archive.reviewIdentity` emits. */
export function makeReviewIdentity(
  overrides: ReviewEntityWire = {},
): ReviewEntityWire {
  return {
    id: "github:4242",
    content_md: "",
    provider: "github",
    provider_user_id: "4242",
    user_id: "11111111111111111111111111111111",
    display_name: "linlin",
    email: "linlin@example.com",
    email_verified: true,
    email_deliverable: true,
    last_login_at: "2026-08-20T10:00:00Z",
    created_at: "2026-08-01T00:00:00Z",
    media: [],
    ...overrides,
  };
}
