const DRAFT_PREFIX = "moment.compose.draft.v1:";
const MAX_AGE_MS = 30 * 24 * 60 * 60 * 1_000;
export const COMPOSE_DRAFT_CLEARED_EVENT = "moment:compose-draft-cleared";

export interface ComposeDraft {
  contentMd: string;
  savedAt: string;
}

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

export function loadComposeDraft(
  instanceId: string | null,
  now = Date.now(),
): ComposeDraft | null {
  if (!instanceId) return null;
  const target = storage();
  if (!target) return null;
  const value = target.getItem(key(instanceId));
  if (!value) return null;
  try {
    const parsed: unknown = JSON.parse(value);
    if (
      typeof parsed !== "object" ||
      parsed === null ||
      Array.isArray(parsed) ||
      typeof (parsed as Record<string, unknown>).content_md !== "string" ||
      typeof (parsed as Record<string, unknown>).saved_at !== "string"
    ) {
      throw new Error("invalid draft");
    }
    const savedAt = (parsed as Record<string, unknown>).saved_at as string;
    const savedTime = Date.parse(savedAt);
    if (
      !Number.isFinite(savedTime) ||
      savedTime > now + 60_000 ||
      now - savedTime > MAX_AGE_MS
    ) {
      target.removeItem(key(instanceId));
      return null;
    }
    return {
      contentMd: (parsed as Record<string, unknown>).content_md as string,
      savedAt,
    };
  } catch {
    target.removeItem(key(instanceId));
    return null;
  }
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

export function clearComposeDraft(instanceId: string | null): void {
  if (!instanceId) return;
  storage()?.removeItem(key(instanceId));
  window.dispatchEvent(
    new CustomEvent(COMPOSE_DRAFT_CLEARED_EVENT, {
      detail: { instanceId },
    }),
  );
}

export function composeDraftKey(instanceId: string): string {
  return key(instanceId);
}
