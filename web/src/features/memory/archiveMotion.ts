import { animate } from "motion";

export type ArchiveJumpDirection = "older" | "newer" | null;
export interface ArchiveJumpRequest {
  id: number;
  search: string;
  direction: ArchiveJumpDirection;
  repeat?: boolean;
  restore?: { key: string; top: number };
}

export function archiveRestorePosition(
  state: unknown,
): ArchiveJumpRequest["restore"] {
  const position =
    state && typeof state === "object" && "momentArchiveAnchor" in state
      ? state.momentArchiveAnchor
      : undefined;
  if (
    position &&
    typeof position === "object" &&
    "key" in position &&
    typeof position.key === "string" &&
    "top" in position &&
    typeof position.top === "number"
  )
    return { key: position.key, top: position.top };
  return undefined;
}

export function archiveJumpDirection(
  from: string,
  to: string | null,
): ArchiveJumpDirection {
  if (from === to) return null;
  return to === null || to > from ? "newer" : "older";
}

/** Keep the loaded window's origin when a selected day reuses that window. */
export function archiveWindowSearch(search: string): string {
  const state: unknown = window.history.state;
  if (
    !state ||
    typeof state !== "object" ||
    !("momentArchiveWindowSearch" in state)
  )
    return search;
  return typeof state.momentArchiveWindowSearch === "string"
    ? state.momentArchiveWindowSearch
    : search;
}

export function rememberArchiveWindow(search: string) {
  window.history.replaceState(
    {
      ...window.history.state,
      momentArchiveWindowSearch: search,
    },
    "",
    window.location.href,
  );
}

export function archiveRowTop(row: HTMLElement, top?: number): number {
  return (
    row.getBoundingClientRect().top +
    window.scrollY -
    (top ??
      document.querySelector<HTMLElement>("#main-content > header")
        ?.offsetHeight ??
      0)
  );
}

function reserveArchiveTail(row: HTMLElement, list: HTMLElement, top?: number) {
  const destination = archiveRowTop(row, top);
  if (
    destination >
    Math.max(0, document.documentElement.scrollHeight - window.innerHeight) + 1
  ) {
    const tail =
      (list.lastElementChild?.getBoundingClientRect().bottom ??
        row.getBoundingClientRect().bottom) - row.getBoundingClientRect().top;
    const offset =
      top ??
      document.querySelector<HTMLElement>("#main-content > header")
        ?.offsetHeight ??
      0;
    list.style.paddingBlockEnd = `${Math.max(0, window.innerHeight - offset - tail)}px`;
  }
}

export function placeArchiveRow(
  row: HTMLElement,
  list: HTMLElement,
  top?: number,
) {
  reserveArchiveTail(row, list, top);
  window.scrollTo({ top: archiveRowTop(row, top), behavior: "auto" });
}

export function emphasizeArchiveRow(row: HTMLElement) {
  for (const highlighted of row
    .closest("section")
    ?.querySelectorAll("[data-archive-target]") ?? []) {
    highlighted.removeAttribute("data-archive-target");
  }
  // A genuine new arrival restarts feedback; an already aligned repeat never calls this.
  void getComputedStyle(row).animationName;
  row.dataset.archiveTarget = "true";
}

/** Scroll from the current viewport; never invent an offset near the destination. */
export function scrollToArchiveRow(
  row: HTMLElement,
  list: HTMLElement,
  onSettled: (arrived: boolean) => void,
): () => void {
  reserveArchiveTail(row, list);
  const start = window.scrollY;
  const distance = archiveRowTop(row) - start;
  const update = (progress: number) =>
    window.scrollTo({
      top: archiveRowTop(row) - distance * (1 - progress),
      behavior: "auto",
    });
  if (Math.abs(distance) < 1) {
    onSettled(false);
    return () => {};
  }
  row.removeAttribute("data-archive-target");
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    update(1);
    onSettled(true);
    return () => {};
  }
  list.dataset.archiveScrolling = distance > 0 ? "older" : "newer";
  const animation = animate(0, 1, {
    duration: Math.min(0.7, 0.16 + Math.abs(distance) / 3500),
    ease: [0.22, 1, 0.36, 1],
    onUpdate: update,
    onComplete: () => {
      cleanup();
      update(1);
      onSettled(true);
    },
  });
  const cancel = () => {
    animation.stop();
    cleanup();
    onSettled(false);
  };
  const keydown = (event: KeyboardEvent) => {
    if (
      [
        "ArrowUp",
        "ArrowDown",
        "PageUp",
        "PageDown",
        "Home",
        "End",
        " ",
        "Escape",
      ].includes(event.key)
    )
      cancel();
  };
  const cleanup = () => {
    delete list.dataset.archiveScrolling;
    window.removeEventListener("wheel", cancel);
    window.removeEventListener("touchstart", cancel);
    window.removeEventListener("keydown", keydown);
  };
  window.addEventListener("wheel", cancel, { passive: true });
  window.addEventListener("touchstart", cancel, { passive: true });
  window.addEventListener("keydown", keydown);
  return () => {
    animation.stop();
    cleanup();
  };
}

/** Capture the current list only after the replacement window is ready. */
let activeTransition: ViewTransition | null = null;

export async function transitionArchiveWindow(
  list: HTMLElement | null,
  direction: ArchiveJumpDirection,
  update: () => void,
  signal: AbortSignal,
): Promise<boolean> {
  if (activeTransition) {
    activeTransition.skipTransition();
    await activeTransition.finished;
  }
  if (signal.aborted) return false;
  if (
    !list ||
    !direction ||
    !document.startViewTransition ||
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  ) {
    if (!signal.aborted) update();
    return !signal.aborted;
  }
  const root = document.documentElement;
  // The inline calendar scrolls with the list; the desktop rail is outside this column.
  const surface = list.closest<HTMLElement>("[data-archive-page]") ?? list;
  const oldRootName = root.style.viewTransitionName;
  const oldListName = surface.style.viewTransitionName;
  const oldBackground = surface.style.backgroundColor;
  const bounds = surface.getBoundingClientRect();
  const header =
    document.querySelector("#main-content > header")?.getBoundingClientRect()
      .bottom ?? 0;
  const nav = document
    .querySelector('nav[aria-label="移动导航"]')
    ?.getBoundingClientRect();
  const bottom = nav && nav.height > 0 ? Math.max(0, innerHeight - nav.top) : 0;
  root.style.viewTransitionName = "none";
  surface.style.viewTransitionName = "archive-timeline";
  surface.style.backgroundColor = "var(--surface)";
  root.dataset.archiveTransition = direction ?? "older";
  root.style.setProperty(
    "--archive-clip",
    `${header}px ${Math.max(0, innerWidth - bounds.right)}px ${bottom}px ${bounds.left}px`,
  );
  root.style.setProperty(
    "--archive-shift",
    `${(direction === "newer" ? -1 : 1) * (innerHeight - header - bottom)}px`,
  );
  const crop = (bounds: DOMRect) =>
    `${Math.max(0, header - bounds.top)}px 0px ${Math.max(0, bounds.bottom - (innerHeight - bottom))}px 0px`;
  root.style.setProperty("--archive-old-clip", crop(bounds));
  let cancelled = false;
  const transition = document.startViewTransition(() => {
    if (signal.aborted || cancelled) return;
    update();
    const nextBounds = surface.getBoundingClientRect();
    root.style.setProperty(
      "--archive-old-offset",
      `${bounds.top - nextBounds.top}px`,
    );
    root.style.setProperty("--archive-new-clip", crop(nextBounds));
  });
  activeTransition = transition;
  const cancel = () => {
    cancelled = true;
    transition.skipTransition();
  };
  const keydown = (event: KeyboardEvent) => {
    if (
      [
        "ArrowUp",
        "ArrowDown",
        "PageUp",
        "PageDown",
        "Home",
        "End",
        " ",
        "Escape",
      ].includes(event.key)
    )
      cancel();
  };
  signal.addEventListener("abort", cancel, { once: true });
  window.addEventListener("wheel", cancel, { passive: true });
  window.addEventListener("touchstart", cancel, { passive: true });
  window.addEventListener("keydown", keydown);
  void transition.ready.catch((error: unknown) => {
    if (!cancelled && !signal.aborted)
      console.warn("Archive view transition was skipped", error);
  });
  try {
    await transition.finished;
    return !cancelled && !signal.aborted;
  } finally {
    if (activeTransition === transition) activeTransition = null;
    signal.removeEventListener("abort", cancel);
    window.removeEventListener("wheel", cancel);
    window.removeEventListener("touchstart", cancel);
    window.removeEventListener("keydown", keydown);
    root.style.viewTransitionName = oldRootName;
    surface.style.viewTransitionName = oldListName;
    surface.style.backgroundColor = oldBackground;
    delete root.dataset.archiveTransition;
    for (const name of [
      "--archive-clip",
      "--archive-shift",
      "--archive-old-offset",
      "--archive-old-clip",
      "--archive-new-clip",
    ])
      root.style.removeProperty(name);
  }
}
