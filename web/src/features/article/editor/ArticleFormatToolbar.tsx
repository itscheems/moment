import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { EditorContext, type Editor } from "@tiptap/react";
import { Toolbar } from "@notion/components/primitives/toolbar";
import { useIsBreakpoint } from "@notion/hooks/use-is-breakpoint";
import { useWindowSize } from "@notion/hooks/use-window-size";
import { useCursorVisibility } from "@notion/hooks/use-cursor-visibility";
import { ArticleSearch } from "../ArticleSearch";
import {
  MainToolbarContent,
  MobileToolbarContent,
} from "@notion/components/templates/simple/components/simple-toolbar-content";

export function ArticleFormatToolbar({ editor }: { editor: Editor }) {
  const isMobile = useIsBreakpoint("max", 481);
  const { height } = useWindowSize();
  const [mobileView, setMobileView] = useState<"main" | "highlighter" | "link">(
    "main",
  );
  const toolbarRef = useRef<HTMLDivElement>(null);
  const showMainToolbar = useCallback(() => setMobileView("main"), []);
  const rect = useCursorVisibility({
    editor,
    overlayHeight: toolbarRef.current?.getBoundingClientRect().height ?? 0,
  });

  useEffect(() => {
    if (!isMobile && mobileView !== "main") {
      setMobileView("main");
    }
  }, [isMobile, mobileView]);

  return (
    <EditorContext.Provider value={{ editor }}>
      <ArticleSearch editor={editor} editing onOpen={showMainToolbar}>
        {({ open, toggle, buttonRef }) => {
          const toolbar = (
            <Toolbar
              ref={toolbarRef}
              aria-label="文章格式"
              tabIndex={0}
              className="moment-article-format"
              style={{
                ...(isMobile
                  ? { bottom: `calc(100% - ${height - rect.y}px)` }
                  : {}),
              }}
            >
              {mobileView === "main" ? (
                <MainToolbarContent
                  onHighlighterClick={() => setMobileView("highlighter")}
                  onLinkClick={() => setMobileView("link")}
                  onSearchAndReplaceClick={toggle}
                  isSearchAndReplaceOpen={open}
                  searchAndReplaceButtonRef={buttonRef}
                  isMobile={isMobile}
                />
              ) : (
                <MobileToolbarContent
                  type={mobileView === "highlighter" ? "highlighter" : "link"}
                  onBack={() => setMobileView("main")}
                />
              )}
            </Toolbar>
          );

          return isMobile ? createPortal(toolbar, document.body) : toolbar;
        }}
      </ArticleSearch>
    </EditorContext.Provider>
  );
}
