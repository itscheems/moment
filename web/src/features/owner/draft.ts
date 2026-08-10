const DRAFT_PREFIX = "moment.compose.draft.v1:";
const MAX_AGE_MS = 30 * 24 * 60 * 60 * 1_000;
export const COMPOSE_DRAFT_CLEARED_EVENT = "moment:compose-draft-cleared";
export const COMPOSE_DRAFT_CLEARED_SCHEMA = "moment.compose-draft-cleared.v1";
export const COMPOSE_DRAFT_CLEAR_ALL_SCHEMA =
  "moment.compose-draft-clear-all.v1";

export interface ComposeDraft {
  contentMd: string;
  savedAt: string;
}

export interface ComposeDraftClearedIdentity {
  schema: typeof COMPOSE_DRAFT_CLEARED_SCHEMA;
  instance_id: string;
  content_md: string;
  saved_at: string;
}

export interface ComposeDraftClearAll {
  schema: typeof COMPOSE_DRAFT_CLEAR_ALL_SCHEMA;
  instance_id: string;
  reason: "logout";
}

export type ComposeDraftClearEvent =
  ComposeDraftClearedIdentity | ComposeDraftClearAll;

export type ComposeDraftClearRequest =
  | { reason: "published"; contentMd: string }
  | { reason: "discarded"; draft: ComposeDraft }
  | { reason: "logout" };

function key(instanceId: string): string {
  return `${DRAFT_PREFIX}${instanceId}`;
}

function storage(): Storage | null {
  try {
    const candidate = window.localStorage;
    const probe = `${DRAFT_PREFIX}probe`;
    candidate.setItem(probe, "1");
    candidate.removeItem(probe);
    return candidate;
  } catch {
    return null;
  }
}

function hasExactKeys(
  value: Record<string, unknown>,
  expected: readonly string[],
): boolean {
  const keys = Object.keys(value);
  return (
    keys.length === expected.length &&
    expected.every((key) => Object.prototype.hasOwnProperty.call(value, key))
  );
}

function parseStoredDraft(value: string | null): ComposeDraft | null {
  if (!value) return null;
  try {
    const parsed: unknown = JSON.parse(value);
    if (
      typeof parsed !== "object" ||
      parsed === null ||
      Array.isArray(parsed) ||
      !hasExactKeys(parsed as Record<string, unknown>, [
        "content_md",
        "saved_at",
      ]) ||
      typeof (parsed as Record<string, unknown>).content_md !== "string" ||
      typeof (parsed as Record<string, unknown>).saved_at !== "string" ||
      !Number.isFinite(
        Date.parse((parsed as Record<string, unknown>).saved_at as string),
      )
    ) {
      return null;
    }
    return {
      contentMd: (parsed as Record<string, unknown>).content_md as string,
      savedAt: (parsed as Record<string, unknown>).saved_at as string,
    };
  } catch {
    return null;
  }
}

export function composeDraftClearedIdentityFromStorage(
  instanceId: string,
  value: string | null,
): ComposeDraftClearedIdentity | null {
  if (!/^[0-9a-f]{32}$/.test(instanceId)) return null;
  const draft = parseStoredDraft(value);
  if (!draft) return null;
  return {
    schema: COMPOSE_DRAFT_CLEARED_SCHEMA,
    instance_id: instanceId,
    content_md: draft.contentMd,
    saved_at: draft.savedAt,
  };
}

function composeDraftClearedIdentity(
  instanceId: string,
  contentMd: string,
  savedAt: string,
): ComposeDraftClearedIdentity | null {
  if (
    !/^[0-9a-f]{32}$/.test(instanceId) ||
    typeof contentMd !== "string" ||
    typeof savedAt !== "string" ||
    !Number.isFinite(Date.parse(savedAt))
  ) {
    return null;
  }
  return {
    schema: COMPOSE_DRAFT_CLEARED_SCHEMA,
    instance_id: instanceId,
    content_md: contentMd,
    saved_at: savedAt,
  };
}

export function parseComposeDraftClearedIdentity(
  value: unknown,
): ComposeDraftClearedIdentity | null {
  if (
    typeof value !== "object" ||
    value === null ||
    Array.isArray(value) ||
    !hasExactKeys(value as Record<string, unknown>, [
      "schema",
      "instance_id",
      "content_md",
      "saved_at",
    ])
  ) {
    return null;
  }
  const identity = value as Record<string, unknown>;
  if (
    identity.schema !== COMPOSE_DRAFT_CLEARED_SCHEMA ||
    typeof identity.instance_id !== "string" ||
    !/^[0-9a-f]{32}$/.test(identity.instance_id) ||
    typeof identity.content_md !== "string" ||
    typeof identity.saved_at !== "string" ||
    !Number.isFinite(Date.parse(identity.saved_at))
  ) {
    return null;
  }
  return identity as unknown as ComposeDraftClearedIdentity;
}

export function parseComposeDraftClearEvent(
  value: unknown,
): ComposeDraftClearEvent | null {
  const identity = parseComposeDraftClearedIdentity(value);
  if (identity) return identity;
  if (
    typeof value !== "object" ||
    value === null ||
    Array.isArray(value) ||
    !hasExactKeys(value as Record<string, unknown>, [
      "schema",
      "instance_id",
      "reason",
    ])
  ) {
    return null;
  }
  const clearAll = value as Record<string, unknown>;
  if (
    clearAll.schema !== COMPOSE_DRAFT_CLEAR_ALL_SCHEMA ||
    typeof clearAll.instance_id !== "string" ||
    !/^[0-9a-f]{32}$/.test(clearAll.instance_id) ||
    clearAll.reason !== "logout"
  ) {
    return null;
  }
  return clearAll as unknown as ComposeDraftClearAll;
}

export function loadComposeDraft(
  instanceId: string | null,
  now = Date.now(),
): ComposeDraft | null {
  if (!instanceId) return null;
  const target = storage();
  if (!target) return null;
  const value = target.getItem(key(instanceId));
  if (!value) return null;
  const draft = parseStoredDraft(value);
  if (!draft) {
    target.removeItem(key(instanceId));
    return null;
  }
  const savedTime = Date.parse(draft.savedAt);
  if (savedTime > now + 60_000 || now - savedTime > MAX_AGE_MS) {
    target.removeItem(key(instanceId));
    return null;
  }
  return draft;
}

export function saveComposeDraft(
  instanceId: string | null,
  contentMd: string,
  now = new Date(),
): boolean {
  if (!instanceId) return false;
  const target = storage();
  if (!target) return false;
  try {
    target.setItem(
      key(instanceId),
      JSON.stringify({ content_md: contentMd, saved_at: now.toISOString() }),
    );
    return true;
  } catch {
    return false;
  }
}

export function clearComposeDraft(
  instanceId: string | null,
  request: ComposeDraftClearRequest,
): void {
  if (!instanceId || !/^[0-9a-f]{32}$/.test(instanceId)) return;
  let storedIdentity: ComposeDraftClearedIdentity | null = null;
  try {
    const target = window.localStorage;
    try {
      storedIdentity = composeDraftClearedIdentityFromStorage(
        instanceId,
        target.getItem(key(instanceId)),
      );
    } catch {
      // Removal is still worth attempting when reads are denied independently.
    }
    try {
      target.removeItem(key(instanceId));
    } catch {
      // Same-document clearing must not depend on persistent storage access.
    }
  } catch {
    // Access to the localStorage object itself can be denied.
  }

  let detail: ComposeDraftClearEvent | null;
  if (request.reason === "logout") {
    detail = {
      schema: COMPOSE_DRAFT_CLEAR_ALL_SCHEMA,
      instance_id: instanceId,
      reason: "logout",
    };
  } else if (request.reason === "published") {
    detail = composeDraftClearedIdentity(
      instanceId,
      request.contentMd,
      new Date().toISOString(),
    );
  } else {
    detail = composeDraftClearedIdentity(
      instanceId,
      request.draft.contentMd,
      request.draft.savedAt,
    );
  }
  detail ??= storedIdentity;
  if (!detail) return;
  window.dispatchEvent(
    new CustomEvent(COMPOSE_DRAFT_CLEARED_EVENT, {
      detail,
    }),
  );
}

export function composeDraftKey(instanceId: string): string {
  return key(instanceId);
}
