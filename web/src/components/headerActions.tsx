import { createContext, useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";

/** Pages fill the shell's header, just as they fill its existing rail slot. */
export const HeaderActionsContext = createContext<HTMLElement | null>(null);

export function HeaderActions({ children }: { children: ReactNode }) {
  const slot = useContext(HeaderActionsContext);
  return slot ? createPortal(children, slot) : null;
}
