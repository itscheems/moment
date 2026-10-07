import {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
  useRef,
  type ReactNode,
} from "react";
import { useTimeout } from "@mantine/hooks";
import type { TableOfContentDataItem as NativeTocItem } from "@tiptap/extension-table-of-contents";
export type TableOfContentDataItem = Omit<
  NativeTocItem,
  "editor" | "pos" | "node"
> &
  Partial<Pick<NativeTocItem, "editor" | "pos" | "node">>;
export type TableOfContentData = TableOfContentDataItem[];
import { selectNodeAndHideFloating } from "@content-notion/lib/selection";

type TocState = {
  tocContent: TableOfContentData | null;
  setTocContent: (value: TableOfContentData | null) => void;

  navigateToHeading: (
    item: TableOfContentDataItem,
    options?: {
      topOffset?: number;
      behavior?: ScrollBehavior;
    },
  ) => void;

  normalizeHeadingDepths: <
    T extends { level?: number; originalLevel?: number },
  >(
    headingList: T[],
  ) => number[];

  navigateToReadingBlocks: (blocks: HTMLElement[], topOffset?: number) => void;
  clearReadingEmphasis: () => void;
};

const TocContext = createContext<TocState | undefined>(undefined);

/**
 * Normalizes heading depths for a table of contents (TOC) structure.
 *
 * This function ensures proper hierarchical nesting where a heading can only be
 * a child of a previous heading with a smaller level number (higher priority).
 * It prevents incorrect structures like h2 being listed under h3.
 *
 * Algorithm:
 * 1. Rebases all levels so the minimum level becomes 1 (root level)
 * 2. For each heading, finds the most recent previous heading with a smaller level
 * 3. If found, nests it under that parent (parent depth + 1)
 * 4. If not found, treats it as a root-level item (depth = 1)
 *
 * @param items - Array of heading items with `level` or `originalLevel` properties
 * @returns Array of normalized depths corresponding to each heading item
 */
export function normalizeHeadingDepths<
  T extends { level?: number; originalLevel?: number },
>(items: T[]): number[] {
  if (items.length === 0) return [];

  const raw = items.map((h) => h.originalLevel ?? h.level ?? 1);

  // --- Determine root level ---
  const positives = raw.filter((l) => l > 0);
  const root = positives.includes(1) ? 1 : Math.min(...positives);

  // --- Rebase levels: root → 1 ---
  const lvl = raw.map((l) => Math.max(1, l - (root - 1)));

  const depths = new Array(items.length).fill(1);
  depths[0] = 1;

  for (let i = 1; i < lvl.length; i++) {
    const current = lvl[i] ?? 1;

    // Find the most recent heading with a smaller level (higher priority)
    let parentIdx = -1;
    for (let j = i - 1; j >= 0; j--) {
      const previous = lvl[j] ?? 1;
      if (previous < current) {
        parentIdx = j;
        break;
      }
    }

    // If we found a valid parent, nest under it
    // Otherwise, this is a root-level item
    depths[i] = parentIdx !== -1 ? depths[parentIdx] + 1 : 1;
  }

  return depths;
}

/**
 * Check if an element is visible in the viewport
 */
const isElementVisible = (element: HTMLElement, topOffset: number): boolean => {
  const rect = element.getBoundingClientRect();
  const viewportHeight =
    window.innerHeight || document.documentElement.clientHeight;

  // Element is visible if:
  // - Its top is below the topOffset
  // - Its bottom is above the viewport top
  // - Its top is above the viewport bottom
  return (
    rect.top >= topOffset &&
    rect.bottom > topOffset &&
    rect.top < viewportHeight
  );
};

const scrollToReadingBlock = (
  element: HTMLElement,
  topOffset: number,
  behavior: ScrollBehavior,
  fullyVisible = false,
) => {
  if (
    !isElementVisible(element, topOffset) ||
    (fullyVisible &&
      element.getBoundingClientRect().bottom > window.innerHeight - 16)
  ) {
    const top =
      element.getBoundingClientRect().top + window.scrollY - topOffset;
    window.scrollTo({
      top,
      behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches
        ? "auto"
        : behavior,
    });
  }
};

/**
 * Low-level navigate helper (not exported in context directly)
 */
const doNavigateToHeading = (
  item: TableOfContentDataItem,
  topOffset: number,
  behavior: ScrollBehavior = "smooth",
) => {
  if (!item.dom || typeof window === "undefined") return;

  scrollToReadingBlock(item.dom, topOffset, behavior);

  if (item.editor && typeof item.pos === "number") {
    selectNodeAndHideFloating(item.editor, item.pos);
  }

  if (item.id) {
    const url = new URL(window.location.href);
    url.hash = item.id;
    // Keep the host router's entry so leaving the reader restores its origin.
    window.history.replaceState(window.history.state, "", url.toString());
  }
};

export const TocProvider = ({ children }: { children: ReactNode }) => {
  const [tocContent, setTocContent] = useState<TableOfContentData | null>(null);
  const highlightedBlocks = useRef<HTMLElement[]>([]);
  const clearHeadingHighlight = useCallback(() => {
    highlightedBlocks.current.forEach((element) =>
      element.classList.remove("ProseMirror-selectednode"),
    );
    highlightedBlocks.current = [];
  }, []);
  const { start, clear } = useTimeout(clearHeadingHighlight, 2_000);
  useEffect(() => clearHeadingHighlight, [clearHeadingHighlight]);
  const clearReadingEmphasis = useCallback(() => {
    clear();
    clearHeadingHighlight();
  }, [clear, clearHeadingHighlight]);

  const emphasizeReadingBlocks = useCallback(
    (blocks: HTMLElement[]) => {
      clear();
      clearHeadingHighlight();
      // Only remove classes introduced by this read-only feedback controller.
      highlightedBlocks.current = blocks.filter(
        (element) => !element.classList.contains("ProseMirror-selectednode"),
      );
      highlightedBlocks.current.forEach((element) =>
        element.classList.add("ProseMirror-selectednode"),
      );
      start();
    },
    [clear, clearHeadingHighlight, start],
  );

  const navigateToReadingBlocks = useCallback(
    (blocks: HTMLElement[], topOffset = 92) => {
      if (!blocks.length) return;
      scrollToReadingBlock(blocks[0], topOffset, "smooth", true);
      emphasizeReadingBlocks(blocks);
    },
    [emphasizeReadingBlocks],
  );

  const navigateToHeading = useCallback<TocState["navigateToHeading"]>(
    (item, options) => {
      clear();
      clearHeadingHighlight();
      const topOffset = options?.topOffset ?? 0;
      const behavior = options?.behavior ?? "smooth";
      doNavigateToHeading(item, topOffset, behavior);

      // Static readers have no editor selection. Briefly reuse the template's
      // selected-node treatment without changing the document or text selection.
      if (item.dom && !item.editor) {
        emphasizeReadingBlocks([item.dom]);
      }
    },
    [clear, clearHeadingHighlight, emphasizeReadingBlocks],
  );

  return (
    <TocContext.Provider
      value={{
        tocContent,
        setTocContent,
        navigateToHeading,
        navigateToReadingBlocks,
        clearReadingEmphasis,
        normalizeHeadingDepths,
      }}
    >
      {children}
    </TocContext.Provider>
  );
};

export const useToc = () => {
  const ctx = useContext(TocContext);
  if (!ctx) {
    throw new Error("useToc must be used inside <TocProvider>");
  }
  return ctx;
};
