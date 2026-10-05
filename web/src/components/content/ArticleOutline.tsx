import { useEffect, type RefObject } from "react";
import {
  useToc,
  type TableOfContentData,
} from "@content-notion/components/nodes/toc-node/context/toc-context";
import { TocSidebar } from "@content-notion/components/nodes/toc-node/ui/toc-sidebar/toc-sidebar";

/** Adapt static-renderer headings into the template's existing TOC data contract. */
function StaticCatalog({
  container,
}: {
  container: RefObject<HTMLDivElement | null>;
}) {
  const { setTocContent } = useToc();
  useEffect(() => {
    const body = container.current;
    if (!body) return;
    const update = () => {
      const headings = Array.from(
        (
          body.querySelector("[data-content-renderer]") ?? body
        ).querySelectorAll<HTMLHeadingElement>(
          "h1[id],h2[id],h3[id],h4[id],h5[id],h6[id]",
        ),
      );
      let active = 0;
      headings.forEach((heading, index) => {
        if (heading.getBoundingClientRect().top <= 140) active = index;
      });
      const data: TableOfContentData = headings.map((dom, index) => ({
        id: dom.id,
        textContent: dom.textContent ?? "",
        level: Number(dom.tagName.slice(1)),
        originalLevel: Number(dom.tagName.slice(1)),
        itemIndex: index + 1,
        dom,
        isActive: index === active,
        isScrolledOver: index < active,
      }));
      setTocContent(data);
    };
    update();
    const observer = new MutationObserver(update);
    observer.observe(body, {
      childList: true,
      subtree: true,
      characterData: true,
    });
    window.addEventListener("scroll", update, { passive: true });
    return () => {
      observer.disconnect();
      window.removeEventListener("scroll", update);
    };
  }, [container, setTocContent]);
  return <TocSidebar topOffset={92} maxShowCount={100} />;
}
export function StaticArticleOutline(props: {
  container: RefObject<HTMLDivElement | null>;
}) {
  return <StaticCatalog {...props} />;
}
