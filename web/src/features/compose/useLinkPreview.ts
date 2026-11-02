import { type PublicLinkCard } from "../../api/public";
import { type LinkCardInput } from "../../api/client";
import { useCallback, useEffect, useRef, useState } from "react";

import { LinksApi } from "../../api/links";

/**
 * Matches a bare http(s) URL using the ASCII character set RFC 3986 permits.
 *
 * Restricting it to ASCII is what keeps the composer aligned with the server's
 * linkifier, which stops at the first non-ASCII byte. A looser pattern would
 * swallow the Chinese text after a URL and resolve an address that never
 * appears in the rendered body.
 */
const urlPattern = /https?:\/\/[A-Za-z0-9\-._~:/?#[\]@!$&'()*+,;=%]+/g;

/**
 * Pick the URL a card should describe.
 *
 * The last URL in the body wins, because that is the one the renderer may hide
 * and the one a writer most likely just pasted.
 */
export function detectLinkUrl(content: string): string | null {
  const matches = content.match(urlPattern);
  if (!matches || matches.length === 0) return null;
  // Trailing punctuation is prose, not part of the address. Full-width marks
  // count too: the server's linkifier stops at the first non-ASCII character,
  // so keeping one here would make the composer resolve a URL that does not
  // match the one in the rendered body.
  const candidate = matches[matches.length - 1].replace(/[),.;:!?'"]+$/, "");
  try {
    const parsed = new URL(candidate);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:")
      return null;
    return candidate;
  } catch {
    return null;
  }
}

export type LinkPreviewStatus = "idle" | "loading" | "ready" | "failed";

export interface LinkPreviewState {
  status: LinkPreviewStatus;
  card: LinkCardInput | null;
  storedCard: PublicLinkCard | null;
  dismiss: () => void;
  dismissed: Set<string>;
  restoreDismissed: (urls: Set<string>) => void;

  changed: boolean;
  reset: () => void;
  restore: (snapshot?: LinkDraftSnapshot) => void;
}

export interface LinkDraftSnapshot {
  card: LinkCardInput | null;
  dismissed: string[];
}

const DEBOUNCE_MS = 400;

/**
 * Resolve a card for whatever URL the body currently ends with.
 *
 * Requests are debounced and the previous one is aborted, matching how mention
 * and location lookups already behave. A failure is silent by design: an
 * unreachable link must never block publishing, exactly as a missing alt text
 * does not.
 */
export function useLinkPreview(
  api: LinksApi,
  content: string,
  enabled: boolean,
  initial?: { card: PublicLinkCard; content: string },
): LinkPreviewState {
  const saved = useRef(initial);
  const savedUrl = saved.current ? detectLinkUrl(saved.current.content) : null;
  const [status, setStatus] = useState<LinkPreviewStatus>("idle");
  const [card, setCard] = useState<LinkCardInput | null>(null);
  const [dismissed, setDismissed] = useState<Set<string>>(() => new Set());
  const [changed, setChanged] = useState(false);
  const controller = useRef<AbortController | null>(null);
  const resolvedCards = useRef(new Map<string, LinkCardInput>());

  const url = enabled
    ? (detectLinkUrl(content) ??
      (saved.current && !savedUrl ? saved.current.card.canonicalUrl : null))
    : null;
  const savedMatches =
    !!saved.current &&
    (url === savedUrl || url === saved.current.card.canonicalUrl);
  const suppressed = url !== null && dismissed.has(url);

  useEffect(() => {
    // A detached saved card has no body URL to remove. Its explicit dismissal
    // survives text edits; body-bound previews can be requested again on paste.
    if (saved.current && !savedUrl) return;
    const present = new Set(
      (content.match(urlPattern) ?? []).map((value) =>
        value.replace(/[),.;:!?'"]+$/, ""),
      ),
    );
    setDismissed((previous) => {
      const next = new Set([...previous].filter((value) => present.has(value)));
      return next.size === previous.size ? previous : next;
    });
  }, [content, savedUrl]);

  useEffect(() => {
    controller.current?.abort();
    if (!url || suppressed) {
      controller.current = null;
      setStatus("idle");
      setCard(null);
      return;
    }
    if (savedMatches) {
      setStatus("ready");
      setCard(null);
      return;
    }
    if (card && card.source_url === url) return;
    const resolved = resolvedCards.current.get(url);
    if (resolved) {
      setCard(resolved);
      setStatus("ready");
      return;
    }
    const abort = new AbortController();
    controller.current = abort;
    setStatus("loading");
    const timer = window.setTimeout(() => {
      api
        .unfurlLink(url, abort.signal)
        .then((resolved) => {
          if (abort.signal.aborted) return;
          resolvedCards.current.set(url, resolved);
          setCard(resolved);
          setStatus("ready");
          setChanged(true);
        })
        .catch(() => {
          if (abort.signal.aborted) return;
          setCard(null);
          setStatus("failed");
        });
    }, DEBOUNCE_MS);
    return () => {
      window.clearTimeout(timer);
      abort.abort();
    };
  }, [api, url, suppressed, card, savedMatches]);

  useEffect(() => () => controller.current?.abort(), []);

  const dismiss = useCallback(() => {
    if (!url) return;
    setDismissed((previous) => {
      const next = new Set(previous).add(url);
      if (saved.current) {
        next.add(saved.current.card.canonicalUrl);
        if (savedUrl) next.add(savedUrl);
      }
      return next;
    });
    setCard(null);
    setStatus("idle");
    setChanged(true);
  }, [url, savedUrl]);

  const reset = useCallback(() => {
    controller.current?.abort();
    saved.current = undefined;
    setDismissed(new Set());
    setCard(null);
    setStatus("idle");
    setChanged(false);
  }, []);

  const restore = useCallback((snapshot?: LinkDraftSnapshot) => {
    controller.current?.abort();
    saved.current = undefined;
    const next = snapshot?.card ?? null;
    if (next) resolvedCards.current.set(next.source_url, next);
    setDismissed(new Set(snapshot?.dismissed ?? []));
    setCard(next);
    setStatus(next ? "ready" : "idle");
    setChanged(false);
  }, []);

  const current = !suppressed && card?.source_url === url ? card : null;
  const savedDismissed =
    saved.current && dismissed.has(savedUrl ?? saved.current.card.canonicalUrl);
  const storedCard =
    enabled &&
    !suppressed &&
    !savedDismissed &&
    url &&
    saved.current &&
    !current
      ? saved.current.card
      : null;
  return {
    storedCard,
    status: suppressed ? "idle" : status,
    card: current,
    dismiss,
    dismissed,
    restoreDismissed: setDismissed,
    changed: storedCard ? false : changed || (!!saved.current && url === null),
    reset,
    restore,
  };
}
