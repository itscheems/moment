import { mapCardPayload, parseMapCard, type MapCardData } from "../maps/types";
import {
  fingerprintComposeDraft,
  parseComposeQuoteTarget,
  type ComposeQuoteTarget,
} from "./draft";

const KEPT_PREFIX = "moment.compose.kept-drafts.v2:";
const KEPT_SCHEMA = "moment.compose-kept-drafts.v1";
const MAX_KEPT = 30;
const MAX_AGE_MS = 30 * 24 * 60 * 60 * 1_000;
const INSTANCE_PATTERN = /^[0-9a-f]{32}\.[0-9a-f]{32}$/;
const ID_PATTERN = /^[0-9a-f]{32}$/;

export interface KeptDraft {
  quote?: ComposeQuoteTarget;
  mapCard?: MapCardData;
  id: string;
  contentMd: string;
  fingerprint: string;
  savedAt: string;
}

function keptKey(instanceId: string): string {
  return `${KEPT_PREFIX}${instanceId}`;
}

function storage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function isTimestamp(value: unknown): value is string {
  if (typeof value !== "string") return false;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) && new Date(parsed).toISOString() === value;
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

function newDraftID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (value) =>
    value.toString(16).padStart(2, "0"),
  ).join("");
}

function parseEntry(value: unknown, now: number): KeptDraft | null {
  if (
    typeof value !== "object" ||
    value === null ||
    Array.isArray(value) ||
    !hasExactKeys(value as Record<string, unknown>, [
      "id",
      "content_md",
      "saved_at",
      ...(Object.hasOwn(value, "map_card") ? ["map_card"] : []),
      ...(Object.hasOwn(value, "quote") ? ["quote"] : []),
    ])
  ) {
    return null;
  }
  const entry = value as Record<string, unknown>;
  if (
    typeof entry.id !== "string" ||
    !ID_PATTERN.test(entry.id) ||
    typeof entry.content_md !== "string" ||
    (entry.content_md.trim().length === 0 &&
      entry.map_card == null &&
      entry.quote == null) ||
    !isTimestamp(entry.saved_at)
  ) {
    return null;
  }
  const savedTime = Date.parse(entry.saved_at);
  if (savedTime > now + 60_000 || now - savedTime > MAX_AGE_MS) return null;
  let quote: ComposeQuoteTarget | undefined;
  let mapCard: MapCardData | undefined;
  try {
    quote = parseComposeQuoteTarget(entry.quote);
    mapCard = entry.map_card == null ? undefined : parseMapCard(entry.map_card);
  } catch {
    return null;
  }
  return {
    ...(mapCard ? { mapCard } : {}),
    id: entry.id,
    contentMd: entry.content_md,
    fingerprint: fingerprintComposeDraft(entry.content_md, mapCard, quote),
    ...(quote ? { quote } : {}),
    savedAt: entry.saved_at,
  };
}

function read(instanceId: string, now: number): KeptDraft[] {
  const target = storage();
  if (!target) return [];
  let raw: string | null;
  try {
    raw = target.getItem(keptKey(instanceId));
  } catch {
    return [];
  }
  if (raw === null) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw) as unknown;
  } catch {
    return [];
  }
  if (
    typeof parsed !== "object" ||
    parsed === null ||
    Array.isArray(parsed) ||
    !hasExactKeys(parsed as Record<string, unknown>, ["schema", "drafts"])
  ) {
    return [];
  }
  const stored = parsed as Record<string, unknown>;
  if (stored.schema !== KEPT_SCHEMA || !Array.isArray(stored.drafts)) return [];
  const drafts: KeptDraft[] = [];
  const seen = new Set<string>();
  for (const candidate of stored.drafts) {
    const entry = parseEntry(candidate, now);
    // A duplicate id would make removal ambiguous, and a duplicate
    // fingerprint would list the same text twice.
    if (!entry || seen.has(entry.id) || seen.has(entry.fingerprint)) continue;
    seen.add(entry.id);
    seen.add(entry.fingerprint);
    drafts.push(entry);
  }
  return drafts
    .sort((left, right) => Date.parse(right.savedAt) - Date.parse(left.savedAt))
    .slice(0, MAX_KEPT);
}

function write(instanceId: string, drafts: KeptDraft[]): boolean {
  const target = storage();
  if (!target) return false;
  try {
    if (drafts.length === 0) {
      target.removeItem(keptKey(instanceId));
      return true;
    }
    target.setItem(
      keptKey(instanceId),
      JSON.stringify({
        schema: KEPT_SCHEMA,
        drafts: drafts.slice(0, MAX_KEPT).map((draft) => ({
          id: draft.id,
          content_md: draft.contentMd,
          ...(draft.quote ? { quote: draft.quote } : {}),
          ...(draft.mapCard ? { map_card: mapCardPayload(draft.mapCard) } : {}),
          saved_at: draft.savedAt,
        })),
      }),
    );
    return true;
  } catch {
    return false;
  }
}

export function listKeptDrafts(
  instanceId: string | null,
  now = Date.now(),
): KeptDraft[] {
  if (!instanceId || !INSTANCE_PATTERN.test(instanceId)) return [];
  return read(instanceId, now);
}

/**
 * File a copy of `contentMd` under 草稿. Text already kept keeps its id and
 * moves to the front rather than arriving a second time.
 */
export function keepDraft(
  instanceId: string | null,
  contentMd: string,
  now = new Date(),
  mapCard?: MapCardData | null,
  quote?: ComposeQuoteTarget | null,
): KeptDraft | null {
  if (!instanceId || !INSTANCE_PATTERN.test(instanceId)) return null;
  if (contentMd.trim().length === 0 && !mapCard && !quote) return null;
  const fingerprint = fingerprintComposeDraft(contentMd, mapCard, quote);
  const current = read(instanceId, now.getTime());
  const existing = current.find((draft) => draft.fingerprint === fingerprint);
  const kept: KeptDraft = {
    ...(quote ? { quote } : {}),
    ...(mapCard ? { mapCard } : {}),
    id: existing?.id ?? newDraftID(),
    contentMd,
    fingerprint,
    savedAt: now.toISOString(),
  };
  const next = [kept, ...current.filter((draft) => draft.id !== kept.id)];
  return write(instanceId, next) ? kept : null;
}

export function dropKeptDraft(
  instanceId: string | null,
  id: string,
  now = Date.now(),
): void {
  if (!instanceId || !INSTANCE_PATTERN.test(instanceId)) return;
  const current = read(instanceId, now);
  const next = current.filter((draft) => draft.id !== id);
  if (next.length !== current.length) write(instanceId, next);
}

/** Drop whatever was kept for this exact text, published or discarded. */
export function dropKeptDraftByContent(
  instanceId: string | null,
  contentMd: string,
  now = Date.now(),
  mapCard?: MapCardData | null,
  quote?: ComposeQuoteTarget | null,
): void {
  if (!instanceId || !INSTANCE_PATTERN.test(instanceId)) return;
  const fingerprint = fingerprintComposeDraft(contentMd, mapCard, quote);
  const current = read(instanceId, now);
  const next = current.filter((draft) => draft.fingerprint !== fingerprint);
  if (next.length !== current.length) write(instanceId, next);
}

export function clearKeptDrafts(instanceId: string | null): void {
  if (!instanceId || !INSTANCE_PATTERN.test(instanceId)) return;
  const target = storage();
  try {
    target?.removeItem(keptKey(instanceId));
  } catch {
    // A denied removal leaves drafts that only this browser can see.
  }
}

export function keptDraftsKey(instanceId: string): string {
  return keptKey(instanceId);
}
