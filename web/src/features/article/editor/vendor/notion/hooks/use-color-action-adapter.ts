import { createContext, useContext } from "react";
import type { Editor } from "@tiptap/core";

export type ColorActionType = "text" | "highlight";
export interface ActionColor {
  type: ColorActionType;
  label: string;
  value: string;
}

/** Explicit host adapter; absent in writing, where the native commands remain. */
export interface ColorActionAdapter {
  editor: Editor;
  canApply: boolean;
  activeColor: (type: ColorActionType) => string | undefined;
  applyColor: (type: ColorActionType, value: string) => boolean;
  recentColors: ActionColor[];
  addRecentColor: (color: ActionColor) => void;
}

export const ColorActionAdapterContext =
  createContext<ColorActionAdapter | null>(null);

export function useColorActionAdapter(editor?: Editor | null) {
  const adapter = useContext(ColorActionAdapterContext);
  return adapter && (!editor || adapter.editor === editor) ? adapter : null;
}
