import { useEffect } from "react";

/** Dismiss focus decoration without changing a dialog's keyboard focus owner. */
export function useEscapeFocus() {
  useEffect(() => {
    const root = document.documentElement;
    const restore = () => root.removeAttribute("data-focus-dismissed");
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.isComposing || event.key === "Process") return;
      if (event.key === "Escape") root.setAttribute("data-focus-dismissed", "");
      else if (!["Shift", "Control", "Alt", "Meta"].includes(event.key))
        restore();
    };
    document.addEventListener("keydown", onKeyDown, true);
    document.addEventListener("pointerdown", restore, true);
    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      document.removeEventListener("pointerdown", restore, true);
      restore();
    };
  }, []);
}
