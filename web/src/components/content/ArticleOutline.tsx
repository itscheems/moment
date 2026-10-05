import { Popover } from "@base-ui/react/popover";
import {
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type RefObject,
} from "react";
import styles from "./ArticleOutline.module.css";

export type OutlineItem = {
  id: string;
  title: string;
  level: number;
  element: HTMLElement;
  activate?: () => void;
};

/** A shared navigation surface for native editor anchors and statically rendered anchors. */
export function OutlineRail({ items }: { items: readonly OutlineItem[] }) {
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState("");
  const initialPath = useRef("");
  useEffect(() => {
    let frame = 0;
    const update = () => {
      frame = 0;
      let current = items[0]?.id ?? "";
      for (const item of items) {
        if (item.element.getBoundingClientRect().top <= 140) current = item.id;
        else break;
      }
      setActive(current);
    };
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(update);
    };
    update();
    window.addEventListener("scroll", schedule, true);
    window.addEventListener("resize", schedule);
    const route = window.location.pathname;
    if (items.length && initialPath.current !== route) {
      initialPath.current = route;
      const hash = window.location.hash.slice(1);
      items
        .find((item) => item.id === hash)
        ?.element.scrollIntoView({ block: "start" });
    }
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("scroll", schedule, true);
      window.removeEventListener("resize", schedule);
    };
  }, [items]);
  if (items.length < 2) return null;
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger
        aria-label="文章目录"
        className={styles.rail}
        openOnHover
        delay={100}
        closeDelay={180}
      >
        <span aria-hidden="true" className={styles.lines}>
          {items.map((item) => (
            <span
              key={item.id}
              className={styles.line}
              data-active={active === item.id}
              style={{ "--heading-level": item.level } as CSSProperties}
            />
          ))}
        </span>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner
          side="left"
          align="start"
          sideOffset={4}
          collisionPadding={12}
          className={styles.positioner}
        >
          <Popover.Popup
            className={styles.popup}
            aria-label="文章目录"
            initialFocus={false}
          >
            <Popover.Title className={styles.title}>目录</Popover.Title>
            <nav aria-label="文章标题">
              {items.map((item) => (
                <a
                  key={item.id}
                  href={`#${item.id}`}
                  className={styles.item}
                  aria-current={active === item.id ? "location" : undefined}
                  style={{ "--heading-level": item.level } as CSSProperties}
                  onClick={(event) => {
                    event.preventDefault();
                    item.activate?.();
                    const reduced = window.matchMedia(
                      "(prefers-reduced-motion: reduce)",
                    ).matches;
                    item.element.scrollIntoView({
                      behavior: reduced ? "instant" : "smooth",
                      block: "start",
                    });
                    if (!item.activate) {
                      item.element.tabIndex = -1;
                      item.element.focus({ preventScroll: true });
                    }
                    window.history.replaceState(
                      window.history.state,
                      "",
                      `#${item.id}`,
                    );
                    setActive(item.id);
                    setOpen(false);
                  }}
                >
                  {item.title}
                </a>
              ))}
            </nav>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

/** Read-only pages observe only the headings produced by Tiptap's static renderer. */
export function StaticArticleOutline({
  container,
}: {
  container: RefObject<HTMLDivElement | null>;
}) {
  const [items, setItems] = useState<OutlineItem[]>([]);
  useEffect(() => {
    const root = container.current;
    if (!root) return;
    const collect = () =>
      setItems(
        Array.from(root.querySelectorAll<HTMLElement>("[data-toc-id]"))
          .filter(
            (element) =>
              !element.closest("[hidden]") && element.textContent?.trim(),
          )
          .map((element) => ({
            id: element.id,
            title: element.textContent!.trim(),
            level: Number(element.tagName.slice(1)),
            element,
          })),
      );
    collect();
    const observer = new MutationObserver(collect);
    observer.observe(root, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: ["id", "data-toc-id", "hidden"],
    });
    return () => observer.disconnect();
  }, [container]);
  return <OutlineRail items={items} />;
}
