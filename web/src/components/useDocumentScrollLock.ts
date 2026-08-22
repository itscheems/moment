import { useLayoutEffect } from "react";

type StyleSnapshot = {
  overflow: string;
  overflowX: string;
  overflowY: string;
  overscrollBehavior: string;
  paddingInlineEnd: string;
  scrollBehavior: string;
};

type DocumentLock = {
  count: number;
  styles: StyleSnapshot;
};

const locks = new WeakMap<Document, DocumentLock>();

/**
 * Lock background scrolling while a blocking overlay is open.
 *
 * The lock lives entirely on the document element. An earlier version also
 * pinned the body with `position: fixed; top: -scrollY`, which is the common
 * recipe, but it silently destroys every `position: sticky` element on the
 * page: with the body taken out of flow there is no scrollport left to stick
 * to, so the navigation rails and the timeline header fell back to their
 * document position and jumped off-screen by exactly the scroll offset. That
 * was visible as the left navigation disappearing whenever a reply dialog was
 * opened from a scrolled page.
 *
 * Hiding overflow on the document element alone blocks real wheel and key
 * scrolling, keeps the scroll offset untouched so nothing needs restoring, and
 * leaves the scrollport intact so sticky positioning keeps working. The same
 * property must not be set on the body: that would turn the body into its own
 * scroll container and reintroduce the identical sticky breakage.
 */
function acquireDocumentLock(document: Document): () => void {
  const active = locks.get(document);
  if (active) {
    active.count += 1;
    return () => releaseDocumentLock(document);
  }

  const html = document.documentElement;
  const view = document.defaultView;
  const lock: DocumentLock = {
    count: 1,
    styles: {
      overflow: html.style.overflow,
      overflowX: html.style.overflowX,
      overflowY: html.style.overflowY,
      overscrollBehavior: html.style.overscrollBehavior,
      paddingInlineEnd: html.style.paddingInlineEnd,
      scrollBehavior: html.style.scrollBehavior,
    },
  };
  locks.set(document, lock);

  // Hiding overflow removes a classic scrollbar, which would widen the
  // viewport and shift the centred column sideways. Reserve the width it gave
  // back so the page does not visibly jump.
  const gutter = Math.max(0, (view?.innerWidth ?? 0) - html.clientWidth);

  html.style.overflow = "hidden";
  html.style.overscrollBehavior = "none";
  html.style.scrollBehavior = "auto";
  if (gutter > 0) {
    html.style.paddingInlineEnd = `${gutter}px`;
  }

  return () => releaseDocumentLock(document);
}

function releaseDocumentLock(document: Document): void {
  const lock = locks.get(document);
  if (!lock) return;
  lock.count -= 1;
  if (lock.count > 0) return;

  Object.assign(document.documentElement.style, lock.styles);
  locks.delete(document);
}

export function useDocumentScrollLock(active: boolean): void {
  useLayoutEffect(() => {
    if (!active || typeof document === "undefined") return undefined;
    return acquireDocumentLock(document);
  }, [active]);
}
