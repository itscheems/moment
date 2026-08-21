import { useCallback, useEffect, useRef, useState } from "react";

import type { OwnerApi, OwnerLinkCard } from "../../api/owner";

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
  card: OwnerLinkCard | null;
  dismiss: () => void;
  /**
   * True once this composer has actually decided something about the card:
   * either one resolved, or the owner removed it.
   *
   * An edit must omit the field otherwise. Sending an empty list on every save
   * would silently strip a card from a post whose body still carries the URL,
   * just because the lookup had not finished.
   */
  changed: boolean;
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
  api: OwnerApi,
  content: string,
  enabled: boolean,
): LinkPreviewState {
  const [status, setStatus] = useState<LinkPreviewStatus>("idle");
  const [card, setCard] = useState<OwnerLinkCard | null>(null);
  const [dismissed, setDismissed] = useState<Set<string>>(() => new Set());
  const [changed, setChanged] = useState(false);
  const controller = useRef<AbortController | null>(null);

  const url = enabled ? detectLinkUrl(content) : null;
  const suppressed = url !== null && dismissed.has(url);

  useEffect(() => {
    controller.current?.abort();
    if (!url || suppressed) {
      controller.current = null;
      setStatus("idle");
      setCard(null);
      return;
    }
    if (card && card.source_url === url) return;
    const abort = new AbortController();
    controller.current = abort;
    setStatus("loading");
    const timer = window.setTimeout(() => {
      api
        .unfurlLink(url, abort.signal)
        .then((resolved) => {
          if (abort.signal.aborted) return;
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
    // `card` is deliberately read but not depended on: reacting to it would
    // refetch immediately after every successful resolve.
  }, [api, url, suppressed, card]);

  useEffect(() => () => controller.current?.abort(), []);

  const dismiss = useCallback(() => {
    if (!url) return;
    setDismissed((previous) => new Set(previous).add(url));
    setCard(null);
    setStatus("idle");
    setChanged(true);
  }, [url]);

  return {
    status: suppressed ? "idle" : status,
    card: suppressed ? null : card,
    dismiss,
    changed,
  };
}
