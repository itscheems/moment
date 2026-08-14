import { useLayoutEffect } from "react";

type StyleSnapshot = {
  html: {
    overflowX: string;
    overflowY: string;
    overscrollBehavior: string;
    scrollBehavior: string;
  };
  body: {
    left: string;
    overflowX: string;
    overflowY: string;
    overscrollBehavior: string;
    position: string;
    right: string;
    scrollBehavior: string;
    top: string;
    width: string;
  };
};

type DocumentLock = {
  count: number;
  scrollX: number;
  scrollY: number;
  styles: StyleSnapshot;
};

const locks = new WeakMap<Document, DocumentLock>();

function acquireDocumentLock(document: Document): () => void {
  const active = locks.get(document);
  if (active) {
    active.count += 1;
    return () => releaseDocumentLock(document);
  }

  const html = document.documentElement;
  const body = document.body;
  const view = document.defaultView;
  const lock: DocumentLock = {
    count: 1,
    scrollX: view?.scrollX ?? html.scrollLeft,
    scrollY: view?.scrollY ?? html.scrollTop,
    styles: {
      html: {
        overflowX: html.style.overflowX,
        overflowY: html.style.overflowY,
        overscrollBehavior: html.style.overscrollBehavior,
        scrollBehavior: html.style.scrollBehavior,
      },
      body: {
        left: body.style.left,
        overflowX: body.style.overflowX,
        overflowY: body.style.overflowY,
        overscrollBehavior: body.style.overscrollBehavior,
        position: body.style.position,
        right: body.style.right,
        scrollBehavior: body.style.scrollBehavior,
        top: body.style.top,
        width: body.style.width,
      },
    },
  };
  locks.set(document, lock);

  html.style.overflowX = "hidden";
  html.style.overflowY = "hidden";
  html.style.overscrollBehavior = "none";
  html.style.scrollBehavior = "auto";
  body.style.position = "fixed";
  body.style.top = `${-lock.scrollY}px`;
  body.style.left = `${-lock.scrollX}px`;
  body.style.right = "0";
  body.style.width = "100%";
  body.style.overflowX = "hidden";
  body.style.overflowY = "hidden";
  body.style.overscrollBehavior = "none";
  body.style.scrollBehavior = "auto";

  return () => releaseDocumentLock(document);
}

function releaseDocumentLock(document: Document): void {
  const lock = locks.get(document);
  if (!lock) return;
  lock.count -= 1;
  if (lock.count > 0) return;

  const html = document.documentElement;
  const body = document.body;
  Object.assign(html.style, lock.styles.html);
  Object.assign(body.style, lock.styles.body);
  locks.delete(document);
  document.defaultView?.scrollTo(lock.scrollX, lock.scrollY);
}

export function useDocumentScrollLock(active: boolean): void {
  useLayoutEffect(() => {
    if (!active || typeof document === "undefined") return undefined;
    return acquireDocumentLock(document);
  }, [active]);
}
