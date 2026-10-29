"use client";

import { useTranslation } from "react-i18next";

import { useCallback, useMemo } from "react";
import type { ElementType, HTMLAttributes } from "react";
import type {
  TableOfContentData,
  TableOfContentDataItem,
} from "@content-notion/components/nodes/toc-node/context/toc-context";

import { useToc } from "@content-notion/components/nodes/toc-node/context/toc-context";

import "@content-notion/components/nodes/toc-node/toc-node.scss";

type TocTreeNode = {
  item: TableOfContentDataItem;
  depth: number;
};

export function TocContent(props: {
  attrs: Record<string, unknown>;
  wrapper?: ElementType;
  options?: { topOffset?: number; maxShowCount?: number; showTitle?: boolean };
  HTMLAttributes?: HTMLAttributes<HTMLDivElement>;
}) {
  const { t } = useTranslation();

  const Wrapper = props.wrapper ?? "div";
  const { tocContent, navigateToHeading, normalizeHeadingDepths } = useToc();

  const {
    topOffset: optionTopOffset = 0,
    maxShowCount: optionMaxShowCount = 20,
    showTitle: optionShowTitle = true,
  } = props.options ?? {};

  const attrs = props.attrs;
  const backgroundColor =
    typeof attrs.backgroundColor === "string"
      ? attrs.backgroundColor
      : undefined;
  const topOffset =
    typeof attrs.topOffset === "number" ? attrs.topOffset : optionTopOffset;
  const maxShowCount =
    typeof attrs.maxShowCount === "number"
      ? attrs.maxShowCount
      : optionMaxShowCount;
  const showTitle =
    typeof attrs.showTitle === "boolean" ? attrs.showTitle : optionShowTitle;

  const headingList = useMemo<TableOfContentData>(
    () => tocContent ?? [],
    [tocContent],
  );

  const visibleHeadings = useMemo(
    () => headingList.slice(0, maxShowCount),
    [headingList, maxShowCount],
  );

  const normalizedDepths = useMemo<number[]>(
    () => normalizeHeadingDepths(visibleHeadings),
    [visibleHeadings, normalizeHeadingDepths],
  );

  const flatTocList = useMemo<TocTreeNode[]>(() => {
    return visibleHeadings.map((item, index) => ({
      item,
      depth: normalizedDepths[index] ?? 1,
    }));
  }, [visibleHeadings, normalizedDepths]);

  const handleContentClick = useCallback(
    (e: React.MouseEvent, item: TableOfContentDataItem) => {
      e.preventDefault();
      e.stopPropagation();
      navigateToHeading(item, { topOffset });
    },
    [navigateToHeading, topOffset],
  );

  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { style: _, ...HTMLAttributes } = props.HTMLAttributes ?? {};

  const wrapperProps = {
    className: "tiptap-table-of-contents-node",
    style: { backgroundColor: backgroundColor || undefined },
    ...HTMLAttributes,
  };

  if (!visibleHeadings.length) {
    return (
      <Wrapper {...wrapperProps}>
        <div className="tiptap-table-of-contents-empty">
          {t("添加标题后，这里会显示目录。")}
        </div>
      </Wrapper>
    );
  }

  return (
    <Wrapper {...wrapperProps}>
      {showTitle && (
        <div className="tiptap-table-of-contents-title">{t("目录")}</div>
      )}
      <nav aria-label={t("目录")} className="tiptap-table-of-contents-list">
        {flatTocList.map((node, index) => (
          <a
            key={node.item.id ?? `${index}-${node.item.textContent}`}
            href={`#${node.item.id}`}
            rel="noopener noreferrer"
            className="tiptap-table-of-contents-item notranslate"
            data-depth={node.depth}
            style={{ "--toc-depth": node.depth } as React.CSSProperties}
            onClick={(e) => handleContentClick(e, node.item)}
          >
            {node.item.textContent}
          </a>
        ))}
      </nav>
    </Wrapper>
  );
}

export default TocContent;
