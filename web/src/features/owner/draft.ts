import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";

const DRAFT_PREFIX = "moment.compose.draft.v1:";
const CLEARANCE_PREFIX = "moment.compose.clearance.v1:";
const CLEAR_ALL_PREFIX = "moment.compose.clear-all.v1:";
const CLEARANCE_CHANNEL = "moment.compose.clearance.sync.v1";
const CLEARANCE_CHANNEL_SCHEMA = "moment.compose-clearance-message.v1";
const MAX_AGE_MS = 30 * 24 * 60 * 60 * 1_000;
const INSTANCE_PATTERN = /^[0-9a-f]{32}$/;
const FINGERPRINT_PATTERN = /^[0-9a-f]{64}$/;
const CHANNEL_SENDER_PATTERN = /^[0-9a-f]{32}$/;
let lastDraftTime = 0;

export const COMPOSE_DRAFT_CLEARED_EVENT = "moment:compose-draft-cleared";
export const COMPOSE_DRAFT_CLEARANCE_SCHEMA = "moment.compose-clearance.v1";
export const COMPOSE_DRAFT_CLEAR_ALL_SCHEMA =
  "moment.compose-draft-clear-all.v1";
export const COMPOSE_DRAFT_CLEARANCE_CHANNEL = CLEARANCE_CHANNEL;

export interface ComposeDraftIdentity {
  fingerprint: string;
  savedAt: string;
}

export interface ComposeDraft extends ComposeDraftIdentity {
  contentMd: string;
}

export interface ComposeDraftClearance {
  schema: typeof COMPOSE_DRAFT_CLEARANCE_SCHEMA;
  instance_id: string;
  draft_fingerprint: string;
  draft_saved_at: string;
  reason: "published" | "discarded";
  cleared_at: string;
}

export interface ComposeDraftClearAll {
  schema: typeof COMPOSE_DRAFT_CLEAR_ALL_SCHEMA;
  instance_id: string;
  reason: "logout";
  cleared_at: string;
}

export type ComposeDraftClearEvent =
  ComposeDraftClearance | ComposeDraftClearAll;

export type ComposeDraftClearRequest =
  | {
      reason: "published" | "discarded";
      draft: ComposeDraftIdentity;
    }
  | { reason: "logout" };

interface ComposeDraftClearanceMessage {
  schema: typeof CLEARANCE_CHANNEL_SCHEMA;
  sender_id: string;
  clearance: ComposeDraftClearEvent;
}

function newChannelSenderID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (value) =>
    value.toString(16).padStart(2, "0"),
  ).join("");
}

const channelSenderID = newChannelSenderID();

function draftKey(instanceId: string): string {
  return `${DRAFT_PREFIX}${instanceId}`;
}

function clearanceKey(instanceId: string): string {
  return `${CLEARANCE_PREFIX}${instanceId}`;
}

function clearAllKey(instanceId: string): string {
  return `${CLEAR_ALL_PREFIX}${instanceId}`;
}

function storage(): Storage | null {
  try {
    return window.localStorage;
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

function isTimestamp(value: unknown): value is string {
  if (typeof value !== "string") return false;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) && new Date(parsed).toISOString() === value;
}

function isCurrentOrPastTimestamp(value: unknown): value is string {
  return isTimestamp(value) && Date.parse(value) <= Date.now() + 60_000;
}

function parseJSON(value: string | null): unknown {
  if (!value) return null;
  try {
    return JSON.parse(value) as unknown;
  } catch {
    return null;
  }
}

export function fingerprintComposeDraft(contentMd: string): string {
  return bytesToHex(sha256(new TextEncoder().encode(contentMd)));
}

function observeDraftTime(savedAt: string): void {
  lastDraftTime = Math.max(lastDraftTime, Date.parse(savedAt));
}

export function createComposeDraft(
  contentMd: string,
  previousSavedAt?: string | null,
  now = new Date(),
): ComposeDraft {
  const previous = previousSavedAt ? Date.parse(previousSavedAt) : 0;
  const nextTime = Math.max(now.getTime(), lastDraftTime + 1, previous + 1);
  lastDraftTime = nextTime;
  return {
    contentMd,
    fingerprint: fingerprintComposeDraft(contentMd),
    savedAt: new Date(nextTime).toISOString(),
  };
}

function parseStoredDraft(value: string | null): ComposeDraft | null {
  const parsed = parseJSON(value);
  if (
    typeof parsed !== "object" ||
    parsed === null ||
    Array.isArray(parsed) ||
    !hasExactKeys(parsed as Record<string, unknown>, ["content_md", "saved_at"])
  ) {
    return null;
  }
  const draft = parsed as Record<string, unknown>;
  if (typeof draft.content_md !== "string" || !isTimestamp(draft.saved_at)) {
    return null;
  }
  return {
    contentMd: draft.content_md,
    fingerprint: fingerprintComposeDraft(draft.content_md),
    savedAt: draft.saved_at,
  };
}

export function parseComposeDraftClearance(
  value: unknown,
): ComposeDraftClearance | null {
  if (
    typeof value !== "object" ||
    value === null ||
    Array.isArray(value) ||
    !hasExactKeys(value as Record<string, unknown>, [
      "schema",
      "instance_id",
      "draft_fingerprint",
      "draft_saved_at",
      "reason",
      "cleared_at",
    ])
  ) {
    return null;
  }
  const clearance = value as Record<string, unknown>;
  if (
    clearance.schema !== COMPOSE_DRAFT_CLEARANCE_SCHEMA ||
    typeof clearance.instance_id !== "string" ||
    !INSTANCE_PATTERN.test(clearance.instance_id) ||
    typeof clearance.draft_fingerprint !== "string" ||
    !FINGERPRINT_PATTERN.test(clearance.draft_fingerprint) ||
    !isTimestamp(clearance.draft_saved_at) ||
    (clearance.reason !== "published" && clearance.reason !== "discarded") ||
    !isCurrentOrPastTimestamp(clearance.cleared_at) ||
    Date.parse(clearance.draft_saved_at) > Date.parse(clearance.cleared_at)
  ) {
    return null;
  }
  return clearance as unknown as ComposeDraftClearance;
}

export function parseComposeDraftClearEvent(
  value: unknown,
): ComposeDraftClearEvent | null {
  const clearance = parseComposeDraftClearance(value);
  if (clearance) return clearance;
  if (
    typeof value !== "object" ||
    value === null ||
    Array.isArray(value) ||
    !hasExactKeys(value as Record<string, unknown>, [
      "schema",
      "instance_id",
      "reason",
      "cleared_at",
    ])
  ) {
    return null;
  }
  const clearAll = value as Record<string, unknown>;
  if (
    clearAll.schema !== COMPOSE_DRAFT_CLEAR_ALL_SCHEMA ||
    typeof clearAll.instance_id !== "string" ||
    !INSTANCE_PATTERN.test(clearAll.instance_id) ||
    clearAll.reason !== "logout" ||
    !isCurrentOrPastTimestamp(clearAll.cleared_at)
  ) {
    return null;
  }
  return clearAll as unknown as ComposeDraftClearAll;
}

export function parseComposeDraftClearEventValue(
  value: string | null,
): ComposeDraftClearEvent | null {
  return parseComposeDraftClearEvent(parseJSON(value));
}

function parseComposeDraftClearanceMessage(
  value: unknown,
): ComposeDraftClearanceMessage | null {
  if (
    typeof value !== "object" ||
    value === null ||
    Array.isArray(value) ||
    !hasExactKeys(value as Record<string, unknown>, [
      "schema",
      "sender_id",
      "clearance",
    ])
  ) {
    return null;
  }
  const message = value as Record<string, unknown>;
  const clearance = parseComposeDraftClearEvent(message.clearance);
  if (
    message.schema !== CLEARANCE_CHANNEL_SCHEMA ||
    typeof message.sender_id !== "string" ||
    !CHANNEL_SENDER_PATTERN.test(message.sender_id) ||
    !clearance
  ) {
    return null;
  }
  return {
    schema: CLEARANCE_CHANNEL_SCHEMA,
    sender_id: message.sender_id,
    clearance,
  };
}

function clearanceMessage(
  clearance: ComposeDraftClearEvent,
): ComposeDraftClearanceMessage {
  return {
    schema: CLEARANCE_CHANNEL_SCHEMA,
    sender_id: channelSenderID,
    clearance,
  };
}

function broadcastComposeDraftClearance(
  clearance: ComposeDraftClearEvent,
): void {
  if (typeof BroadcastChannel === "undefined") return;
  let channel: BroadcastChannel | null = null;
  try {
    channel = new BroadcastChannel(CLEARANCE_CHANNEL);
    channel.postMessage(clearanceMessage(clearance));
  } catch {
    // Storage events and delayed draft repair remain as safe fallbacks.
  } finally {
    channel?.close();
  }
}

export function subscribeComposeDraftClearance(
  listener: (clearance: ComposeDraftClearEvent) => void,
): () => void {
  if (typeof BroadcastChannel === "undefined") return () => undefined;
  let channel: BroadcastChannel;
  try {
    channel = new BroadcastChannel(CLEARANCE_CHANNEL);
  } catch {
    return () => undefined;
  }
  const receive = (event: MessageEvent<unknown>) => {
    const message = parseComposeDraftClearanceMessage(event.data);
    if (!message || message.sender_id === channelSenderID) return;
    listener(message.clearance);
  };
  channel.addEventListener("message", receive);
  return () => {
    channel.removeEventListener("message", receive);
    channel.close();
  };
}

function clearedBy(
  draft: ComposeDraft,
  clearance: ComposeDraftClearance,
): boolean {
  return (
    draft.fingerprint === clearance.draft_fingerprint &&
    Date.parse(draft.savedAt) <= Date.parse(clearance.cleared_at)
  );
}

function matchingClearance(
  target: Storage,
  instanceId: string,
  draft: ComposeDraft,
): ComposeDraftClearance | null {
  try {
    const event = parseComposeDraftClearEventValue(
      target.getItem(clearanceKey(instanceId)),
    );
    if (
      event?.schema === COMPOSE_DRAFT_CLEARANCE_SCHEMA &&
      event.instance_id === instanceId &&
      clearedBy(draft, event)
    ) {
      return event;
    }
  } catch {
    // A denied marker read leaves the in-memory draft usable.
  }
  return null;
}

function isClearedByLogout(
  target: Storage,
  instanceId: string,
  draft: ComposeDraft,
): ComposeDraftClearAll | null {
  try {
    const event = parseComposeDraftClearEventValue(
      target.getItem(clearAllKey(instanceId)),
    );
    if (
      event?.schema === COMPOSE_DRAFT_CLEAR_ALL_SCHEMA &&
      event.instance_id === instanceId &&
      Date.parse(draft.savedAt) <= Date.parse(event.cleared_at)
    ) {
      return event;
    }
  } catch {
    // A denied marker read leaves the in-memory draft usable.
  }
  return null;
}

function removeStoredDraftIfMatching(
  target: Storage,
  instanceId: string,
  event: ComposeDraftClearEvent,
): boolean {
  try {
    const current = parseStoredDraft(target.getItem(draftKey(instanceId)));
    const matches =
      event.schema === COMPOSE_DRAFT_CLEAR_ALL_SCHEMA
        ? Date.parse(current?.savedAt ?? "") <= Date.parse(event.cleared_at)
        : current
          ? clearedBy(current, event)
          : false;
    if (!current || !matches) return false;
    target.removeItem(draftKey(instanceId));
    return true;
  } catch {
    return false;
  }
}

export function removeComposeDraftForClearance(
  instanceId: string | null,
  value: unknown,
): boolean {
  if (!instanceId || !INSTANCE_PATTERN.test(instanceId)) return false;
  const clearance = parseComposeDraftClearEvent(value);
  if (!clearance || clearance.instance_id !== instanceId) return false;
  const target = storage();
  if (target && clearance.schema === COMPOSE_DRAFT_CLEAR_ALL_SCHEMA) {
    try {
      target.removeItem(draftKey(instanceId));
      return true;
    } catch {
      return false;
    }
  }
  return target
    ? removeStoredDraftIfMatching(target, instanceId, clearance)
    : false;
}

export function loadComposeDraft(
  instanceId: string | null,
  now = Date.now(),
): ComposeDraft | null {
  if (!instanceId || !INSTANCE_PATTERN.test(instanceId)) return null;
  const target = storage();
  if (!target) return null;
  let raw: string | null;
  try {
    raw = target.getItem(draftKey(instanceId));
  } catch {
    return null;
  }
  if (raw === null) return null;
  const draft = parseStoredDraft(raw);
  if (!draft) {
    try {
      target.removeItem(draftKey(instanceId));
    } catch {
      // Invalid local data is ignored when storage cleanup is denied.
    }
    return null;
  }
  const savedTime = Date.parse(draft.savedAt);
  if (savedTime > now + 60_000 || now - savedTime > MAX_AGE_MS) {
    try {
      target.removeItem(draftKey(instanceId));
    } catch {
      // Expired or future local data is ignored when cleanup is denied.
    }
    return null;
  }
  observeDraftTime(draft.savedAt);
  const clearance = matchingClearance(target, instanceId, draft);
  if (clearance) {
    removeStoredDraftIfMatching(target, instanceId, clearance);
    return null;
  }
  const logout = isClearedByLogout(target, instanceId, draft);
  if (logout) {
    removeStoredDraftIfMatching(target, instanceId, logout);
    return null;
  }
  return draft;
}

export function saveComposeDraft(
  instanceId: string | null,
  draft: ComposeDraft,
): boolean {
  if (
    !instanceId ||
    !INSTANCE_PATTERN.test(instanceId) ||
    !isTimestamp(draft.savedAt) ||
    !FINGERPRINT_PATTERN.test(draft.fingerprint) ||
    fingerprintComposeDraft(draft.contentMd) !== draft.fingerprint
  ) {
    return false;
  }
  const target = storage();
  if (!target) return false;
  try {
    target.setItem(
      draftKey(instanceId),
      JSON.stringify({
        content_md: draft.contentMd,
        saved_at: draft.savedAt,
      }),
    );
    return true;
  } catch {
    return false;
  }
}

export function clearComposeDraft(
  instanceId: string | null,
  request: ComposeDraftClearRequest,
  now = new Date(),
): void {
  if (
    !instanceId ||
    !INSTANCE_PATTERN.test(instanceId) ||
    !Number.isFinite(now.getTime())
  ) {
    return;
  }
  let detail: ComposeDraftClearEvent;
  if (request.reason === "logout") {
    const clearedAt = now.toISOString();
    detail = {
      schema: COMPOSE_DRAFT_CLEAR_ALL_SCHEMA,
      instance_id: instanceId,
      reason: "logout",
      cleared_at: clearedAt,
    };
    broadcastComposeDraftClearance(detail);
    dispatchComposeDraftClear(detail);
    try {
      const target = window.localStorage;
      let markerWritten = false;
      try {
        target.setItem(clearAllKey(instanceId), JSON.stringify(detail));
        markerWritten = true;
      } catch {
        // Same-document logout still clears every mounted draft surface.
      }
      try {
        target.removeItem(draftKey(instanceId));
      } catch {
        // The marker can still suppress a draft that cannot be removed.
      }
      if (!markerWritten) {
        try {
          target.setItem(clearAllKey(instanceId), JSON.stringify(detail));
        } catch {
          // The clearance channel already propagated the logout identity.
        }
      }
    } catch {
      // The clearance channel and same-document event remain available.
    }
    return;
  } else {
    if (
      !FINGERPRINT_PATTERN.test(request.draft.fingerprint) ||
      !isTimestamp(request.draft.savedAt)
    ) {
      return;
    }
    const clearedAt = new Date(
      Math.max(now.getTime(), Date.parse(request.draft.savedAt)),
    ).toISOString();
    detail = {
      schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
      instance_id: instanceId,
      draft_fingerprint: request.draft.fingerprint,
      draft_saved_at: request.draft.savedAt,
      reason: request.reason,
      cleared_at: clearedAt,
    };
    broadcastComposeDraftClearance(detail);
    dispatchComposeDraftClear(detail);
    let target: Storage | null = null;
    try {
      target = window.localStorage;
    } catch {
      // Same-document identity clearing remains available without persistence.
    }
    let markerWritten = false;
    try {
      target?.setItem(clearanceKey(instanceId), JSON.stringify(detail));
      markerWritten = target !== null;
    } catch {
      // A failed marker write must not retain already published local text.
    }
    try {
      if (target) removeStoredDraftIfMatching(target, instanceId, detail);
    } catch {
      // Same-document identity clearing remains available without persistence.
    }
    if (target && !markerWritten) {
      try {
        target.setItem(clearanceKey(instanceId), JSON.stringify(detail));
      } catch {
        // The clearance channel already propagated the draft identity.
      }
    }
  }
}

function dispatchComposeDraftClear(detail: ComposeDraftClearEvent): void {
  window.dispatchEvent(
    new CustomEvent(COMPOSE_DRAFT_CLEARED_EVENT, {
      detail,
    }),
  );
}

export function composeDraftKey(instanceId: string): string {
  return draftKey(instanceId);
}

export function composeDraftClearanceKey(instanceId: string): string {
  return clearanceKey(instanceId);
}

export function composeDraftClearAllKey(instanceId: string): string {
  return clearAllKey(instanceId);
}
