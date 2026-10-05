import { useEffect, useMemo, useRef } from "react";

/**
 * BubbleMenu owns selection, focus and positioning. Its hide callback runs after
 * detaching the element, so briefly retain that same inert surface for the exit.
 * Reopening cancels the exit; no cloned controls or second selection model.
 */
export function useBubbleMotion() {
  const ref = useRef<HTMLDivElement>(null);
  const lifecycle = useRef<{
    parent: HTMLElement | null;
    animation: Animation | null;
    exiting: boolean;
  }>({ parent: null, animation: null, exiting: false });
  const options = useMemo(() => {
    const duration = () =>
      window.matchMedia("(prefers-reduced-motion: reduce)").matches ? 0 : 150;
    return {
      placement: "top" as const,
      offset: 8,
      onShow: () => {
        const element = ref.current;
        if (!element) return;
        const state = lifecycle.current;
        state.animation?.cancel();
        state.exiting = false;
        state.parent = element.parentElement;
        element.inert = false;
        element.style.pointerEvents = "";
        state.animation =
          element.animate?.(
            [
              { opacity: 0, translate: "0 4px" },
              { opacity: 1, translate: "0 0" },
            ],
            { duration: duration(), easing: "cubic-bezier(0.16, 1, 0.3, 1)" },
          ) ?? null;
      },
      onHide: () => {
        const element = ref.current;
        const state = lifecycle.current;
        state.animation?.cancel();
        if (
          !element ||
          !state.parent?.isConnected ||
          !duration() ||
          !element.animate
        )
          return;
        state.exiting = true;
        element.inert = true;
        element.style.pointerEvents = "none";
        state.parent.appendChild(element);
        element.style.visibility = "visible";
        element.style.opacity = "0";
        state.animation = element.animate(
          [
            { opacity: 1, translate: "0 0" },
            { opacity: 0, translate: "0 3px" },
          ],
          { duration: duration(), easing: "ease-out" },
        );
        state.animation.onfinish = () => {
          if (state.exiting) element.remove();
          state.animation = null;
        };
      },
    };
  }, []);
  useEffect(() => {
    const state = lifecycle.current;
    const element = ref.current;
    return () => {
      state.animation?.cancel();
      if (state.exiting) element?.remove();
      state.parent = null;
    };
  }, []);
  return { ref, options };
}
