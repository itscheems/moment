import { animate } from "motion";

export type ArchiveJumpDirection = "older" | "newer" | null;

export function archiveJumpDirection(
  from: string,
  to: string | null,
): ArchiveJumpDirection {
  if (from === to) return null;
  return to === null || to > from ? "newer" : "older";
}

export function scrollToArchiveRow(
  row: HTMLElement,
  list: HTMLElement,
  direction: ArchiveJumpDirection,
  onSettled: (arrived: boolean) => void,
): () => void {
  const destination = () =>
    row.getBoundingClientRect().top +
    window.scrollY -
    (document.querySelector<HTMLElement>("#main-content > header")
      ?.offsetHeight ?? 0);
  const scroll = (top: number) => window.scrollTo({ top, behavior: "auto" });
  if (
    !direction ||
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  ) {
    scroll(destination());
    onSettled(true);
    return () => {};
  }

  // A distant date loads a new window of the timeline. Read through a bounded
  // part of its neighboring rows, in chronological order, rather than animating
  // an unrelated old document offset or scrolling through years of history.
  const distance = Math.min(360, window.innerHeight * 0.55);
  const end = destination();
  const start = Math.max(
    0,
    Math.min(
      end + (direction === "older" ? -distance : distance),
      document.documentElement.scrollHeight - window.innerHeight,
    ),
  );
  const offset = start - end;
  const update = (progress: number) =>
    scroll(destination() + offset * (1 - progress));
  list.dataset.archiveScrolling = direction;
  update(0);
  const animation = animate(0, 1, {
    duration: 0.45,
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
  const onKeyDown = (event: KeyboardEvent) => {
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
    ) {
      cancel();
    }
  };
  const cleanup = () => {
    delete list.dataset.archiveScrolling;
    window.removeEventListener("wheel", cancel);
    window.removeEventListener("touchstart", cancel);
    window.removeEventListener("keydown", onKeyDown);
  };
  window.addEventListener("wheel", cancel, { passive: true });
  window.addEventListener("touchstart", cancel, { passive: true });
  window.addEventListener("keydown", onKeyDown);
  return () => {
    animation.stop();
    cleanup();
  };
}
