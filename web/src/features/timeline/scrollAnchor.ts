function rowElements(root: ParentNode = document): HTMLElement[] {
  return Array.from(
    root.querySelectorAll<HTMLElement>("[data-timeline-item-key]"),
  );
}

export function rowElement(
  key: string,
  root: ParentNode = document,
): HTMLElement | undefined {
  return rowElements(root).find(
    (element) => element.dataset.timelineItemKey === key,
  );
}

/**
 * Rows off screen skip their layout (`content-visibility: auto`) and stand at
 * a placeholder height until they come near, so a position computed across
 * them is wrong by the difference -- measured: an anchored row placed under
 * the header at 53px was 380px above it a frame later, once the twenty rows
 * over it had taken their real height. Rendering every row once fixes its
 * real height in the browser's memory (`contain-intrinsic-size: auto`), so
 * whatever is measured against them afterwards holds; the override is lifted
 * on the next frame and the rows go back to skipping.
 */
export function withRowsRendered(
  measureAndScroll: () => void,
  root: ParentNode = document,
) {
  const rows = rowElements(root);
  for (const row of rows) {
    row.style.setProperty("content-visibility", "visible");
  }
  measureAndScroll();
  window.requestAnimationFrame(() => {
    for (const row of rows) row.style.removeProperty("content-visibility");
  });
}
