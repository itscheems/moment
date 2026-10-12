import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { EditorContext, type Editor } from "@tiptap/react";
import { Toolbar } from "@notion/components/primitives/toolbar";
import { useIsBreakpoint } from "@notion/hooks/use-is-breakpoint";
import { useWindowSize } from "@notion/hooks/use-window-size";
import { useCursorVisibility } from "@notion/hooks/use-cursor-visibility";
import { SearchAndReplace } from "@notion/components/ui/search-and-replace";
import {
  MainToolbarContent,
  MobileToolbarContent,
} from "@notion/components/templates/simple/components/simple-toolbar-content";

const SEARCH_AND_REPLACE_SCROLL_OPTIONS: ScrollIntoViewOptions = {
  block: "center",
};

export function ArticleFormatToolbar({
  editor,
  loading = false,
}: {
  editor: Editor;
  loading?: boolean;
}) {
  const isMobile = useIsBreakpoint("max", 481);
  const { height } = useWindowSize();
  const [mobileView, setMobileView] = useState<"main" | "highlighter" | "link">(
    "main",
  );
  const [isSearchAndReplaceOpen, setIsSearchAndReplaceOpen] = useState(false);
  const toolbarRef = useRef<HTMLDivElement>(null);
  const searchAndReplaceButtonRef = useRef<HTMLButtonElement>(null);
  const rect = useCursorVisibility({
    editor,
    overlayHeight: toolbarRef.current?.getBoundingClientRect().height ?? 0,
  });

  useEffect(() => {
    if (!loading) return;
    setMobileView("main");
    setIsSearchAndReplaceOpen(false);
  }, [loading]);

  useEffect(() => {
    if (!isMobile && mobileView !== "main") {
      setMobileView("main");
    }
  }, [isMobile, mobileView]);

  const openSearchAndReplace = useCallback(() => {
    if (loading) return;
    setMobileView("main");
    setIsSearchAndReplaceOpen(true);
  }, [loading]);

  const closeSearchAndReplace = useCallback(() => {
    setIsSearchAndReplaceOpen(false);
    searchAndReplaceButtonRef.current?.focus();
  }, []);

  const toggleSearchAndReplace = useCallback(() => {
    if (isSearchAndReplaceOpen) {
      closeSearchAndReplace();
      return;
    }
    openSearchAndReplace();
  }, [closeSearchAndReplace, isSearchAndReplaceOpen, openSearchAndReplace]);

  const toolbar = (
    <Toolbar
      ref={toolbarRef}
      aria-label="文章格式"
      aria-disabled={loading}
      inert={loading}
      tabIndex={0}
      className="moment-article-format"
      style={{
        ...(isMobile ? { bottom: `calc(100% - ${height - rect.y}px)` } : {}),
      }}
    >
      {mobileView === "main" ? (
        <MainToolbarContent
          onHighlighterClick={() => setMobileView("highlighter")}
          onLinkClick={() => setMobileView("link")}
          onSearchAndReplaceClick={toggleSearchAndReplace}
          isSearchAndReplaceOpen={isSearchAndReplaceOpen}
          searchAndReplaceButtonRef={searchAndReplaceButtonRef}
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

  return (
    <EditorContext.Provider value={{ editor }}>
      {isMobile ? createPortal(toolbar, document.body) : toolbar}
      <SearchAndReplace
        className="moment-article-search"
        open={!loading && isSearchAndReplaceOpen}
        enableShortcut={!loading}
        onOpen={openSearchAndReplace}
        onClose={closeSearchAndReplace}
        scrollIntoViewOptions={SEARCH_AND_REPLACE_SCROLL_OPTIONS}
      />
    </EditorContext.Provider>
  );
}
