/*!
 * Copyright (c) Magic UI — MIT License.
 * Circle reveal adapted from AnimatedThemeToggler:
 * https://magicui.design/docs/components/animated-theme-toggler
 * Permission notice: /licenses/magicui-theme-toggler-LICENSE.txt
 */
import { useCallback, useEffect, useRef } from "react";
import { flushSync } from "react-dom";
import "./theme-transition.css";

function reportTransitionError(error: unknown) {
  if (error instanceof DOMException && error.name === "AbortError") return;
  console.error("Theme transition failed", error);
}

/** Theme state and persistence stay with ThemeProvider; this only reveals its update. */
export function useThemeTransition() {
  const active = useRef<{
    transition: ViewTransition;
    animation: Animation | null;
    release: () => void;
  } | null>(null);

  useEffect(
    () => () => {
      const current = active.current;
      if (!current) return;
      current.transition.skipTransition();
      current.release();
    },
    [],
  );

  return useCallback((button: HTMLElement, update: () => void) => {
    const root = document.documentElement;
    if (root.dataset.themeTransition === "active") return;
    if (
      typeof document.startViewTransition !== "function" ||
      typeof root.animate !== "function" ||
      window.matchMedia("(prefers-reduced-motion: reduce)").matches
    ) {
      update();
      return;
    }

    const bounds = button.getBoundingClientRect();
    const x = bounds.left + bounds.width / 2;
    const y = bounds.top + bounds.height / 2;
    const width = window.innerWidth;
    const height = window.innerHeight;
    const radius = Math.hypot(Math.max(x, width - x), Math.max(y, height - y));
    // Percentages keep the snapshot origin correct at fractional display scales.
    const center = `${(x / width) * 100}% ${(y / height) * 100}%`;
    const extent = (radius / (Math.hypot(width, height) / Math.SQRT2)) * 100;
    const clipPath = [
      `circle(0% at ${center})`,
      `circle(${extent}% at ${center})`,
    ];
    root.dataset.themeTransition = "active";
    root.style.setProperty("--theme-transition-from", clipPath[0]);

    const transition = document.startViewTransition(() => flushSync(update));
    const release = () => {
      if (active.current?.transition !== transition) return;
      active.current.animation?.cancel();
      active.current = null;
      delete root.dataset.themeTransition;
      root.style.removeProperty("--theme-transition-from");
    };
    active.current = { transition, animation: null, release };

    void transition.ready
      .then(() => {
        if (active.current?.transition !== transition) return;
        active.current.animation = root.animate(
          { clipPath },
          {
            duration: 400,
            easing: "ease-in-out",
            fill: "forwards",
            pseudoElement: "::view-transition-new(root)",
          },
        );
      })
      .catch((error: unknown) => {
        transition.skipTransition();
        release();
        reportTransitionError(error);
      });
    void transition.finished.then(release, (error: unknown) => {
      release();
      reportTransitionError(error);
    });
  }, []);
}
