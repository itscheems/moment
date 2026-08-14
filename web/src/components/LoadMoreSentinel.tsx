import { useEffect, useRef } from "react";

export type LoadSource = "automatic" | "manual";

export function LoadMoreSentinel({
  className,
  cursor,
  label,
  loading,
  onLoad,
}: {
  className?: string;
  cursor: string;
  label: string;
  loading: boolean;
  onLoad: (source: LoadSource) => void;
}) {
  const sentinelRef = useRef<HTMLDivElement>(null);
  const onLoadRef = useRef(onLoad);
  const loadingRef = useRef(loading);
  const requestedCursor = useRef<string | null>(null);

  onLoadRef.current = onLoad;
  loadingRef.current = loading;

  useEffect(() => {
    if (typeof IntersectionObserver !== "function") return;
    const target = sentinelRef.current;
    if (!target) return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (
          entries.some((entry) => entry.isIntersecting) &&
          !loadingRef.current &&
          requestedCursor.current !== cursor
        ) {
          requestedCursor.current = cursor;
          onLoadRef.current("automatic");
        }
      },
      { rootMargin: "800px 0px" },
    );
    observer.observe(target);
    return () => observer.disconnect();
  }, [cursor]);

  return (
    <div className={className} ref={sentinelRef}>
      <button disabled={loading} onClick={() => onLoad("manual")} type="button">
        {loading ? "正在加载…" : label}
      </button>
    </div>
  );
}
