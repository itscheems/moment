import { useCallback, useEffect, useRef, useState } from "react";
import { useMergedRef, useResizeObserver } from "@mantine/hooks";

/** Shared by the outline and reading excerpts; one-pixel tolerance handles zoom. */
export function useScrollEdges() {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [observeViewport, viewportRect] = useResizeObserver<HTMLDivElement>();
  const [contentRef, contentRect] = useResizeObserver<HTMLDivElement>();
  const viewportRef = useMergedRef(scrollRef, observeViewport);
  const [scrollEdges, setScrollEdges] = useState({ top: false, bottom: false });
  const updateScrollEdges = useCallback(() => {
    const viewport = scrollRef.current;
    if (!viewport) return;
    const top = viewport.scrollTop > 1;
    const bottom =
      viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop > 1;
    setScrollEdges((previous) =>
      previous.top === top && previous.bottom === bottom
        ? previous
        : { top, bottom },
    );
  }, []);
  useEffect(updateScrollEdges, [
    updateScrollEdges,
    viewportRect.width,
    viewportRect.height,
    contentRect.width,
    contentRect.height,
  ]);
  return { viewportRef, contentRef, scrollRef, scrollEdges, updateScrollEdges };
}
