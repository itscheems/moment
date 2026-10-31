import {
  PublicApi,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { PostPresentation } from "../posts/PostPresentation";
import { commentAsPost } from "../share/shareCardInput";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  interactionsApi,
  type InboxItem,
  type InboxPage,
  type NotificationScope,
} from "../../api/interactions";
import { InternalLink, useNavigation } from "../../app/navigation";
import { commentDetailPath } from "../../app/routes";
import { SiteAvatar } from "../../components/SiteIdentity";
import { Icon } from "../../components/icons";
import { LoadMoreSentinel } from "../../components/LoadMoreSentinel";
import { useSession } from "../auth/SessionProvider";
import { useNotifications } from "./NotificationProvider";
import styles from "./Notifications.module.css";

function notificationHref(item: InboxItem) {
  return item.subject.kind === "comment"
    ? commentDetailPath(item.postId, item.subject.id)
    : "/m/" + item.postId;
}
function NotificationRow({
  item,
  embedded = false,
}: {
  item: InboxItem;
  embedded?: boolean;
}) {
  const { t } = useTranslation();
  const { navigate } = useNavigation();
  const like = item.reasons.includes("like");
  const face = {
    username: item.actor.username ?? "",
    displayName: item.actor.displayName,
    avatarMediaId: item.actor.avatarMediaId,
    coverMediaId: null,
  };
  const message = like
    ? item.subject.kind === "comment"
      ? t("赞了你的评论")
      : t("赞了你的帖子")
    : item.reasons.includes("mention")
      ? t("提及了你")
      : item.reasons.includes("reply")
        ? t("回复了你")
        : t("在你参与的对话中回复");
  const open = () => {
    if (item.available) navigate(notificationHref(item));
  };
  return (
    <article
      className={styles.row}
      data-unseen={!item.seen || undefined}
      data-notification-id={embedded ? undefined : item.id}
    >
      <div className={styles.eventIcon} data-like={like || undefined}>
        <Icon
          name={
            like
              ? "heart-filled"
              : item.reasons.includes("mention")
                ? "notification"
                : "reply"
          }
        />
      </div>
      <div className={styles.rowBody}>
        <div className={styles.avatarLine}>
          {item.actor.profileUrl ? (
            <InternalLink
              aria-label={item.actor.displayName}
              href={item.actor.profileUrl}
            >
              <SiteAvatar site={face} />
            </InternalLink>
          ) : (
            <span>
              <SiteAvatar site={face} />
            </span>
          )}
        </div>
        <p className={styles.eventText}>
          {item.actor.profileUrl ? (
            <InternalLink className={styles.actor} href={item.actor.profileUrl}>
              {item.actor.displayName}
            </InternalLink>
          ) : (
            <strong>{item.actor.displayName}</strong>
          )}{" "}
          {message}
        </p>
        {item.available ? (
          <InternalLink
            className={styles.excerpt}
            href={notificationHref(item)}
          >
            {item.excerpt || t("查看内容")}
          </InternalLink>
        ) : (
          <p className={styles.unavailable}>{t("此内容暂不可用")}</p>
        )}
      </div>
      {item.available ? (
        <button
          type="button"
          tabIndex={-1}
          className={styles.rowHit}
          onClick={open}
          aria-label={t("查看通知内容")}
        />
      ) : null}
    </article>
  );
}

function NotificationContent({
  item,
  site,
  parents,
  onReady,
}: {
  onReady?: () => void;
  item: InboxItem;
  site: SiteIdentity;
  parents: Map<string, Promise<PublicPost>>;
}) {
  const { t } = useTranslation();
  const auth = useSession();
  const target = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(false);
  const [detail, setDetail] = useState<InboxItem | null>(null);
  const [parent, setParent] = useState<PublicPost | null>(null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (typeof IntersectionObserver !== "function") {
      setVisible(true);
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { rootMargin: "300px 0px" },
    );
    if (target.current) observer.observe(target.current);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (!visible || !item.available) return;
    const controller = new AbortController();
    setFailed(false);
    void interactionsApi
      .get(item.id, controller.signal)
      .then(async (value) => {
        let root: PublicPost | null = null;
        if (value.available && value.comment) {
          let reading = parents.get(item.postId);
          if (!reading) {
            reading = new PublicApi().post(item.postId);
            parents.set(item.postId, reading);
          }
          root = await reading;
        }
        if (!controller.signal.aborted) {
          setDetail(value);
          setParent(root);
        }
      })
      .catch((error) => {
        if (!controller.signal.aborted) {
          auth.expireIfInvalidSession(error);
          setFailed(true);
        }
      });
    return () => controller.abort();
  }, [
    visible,
    item.id,
    item.available,
    item.excerpt,
    item.postId,
    parents,
    attempt,
    auth.expireIfInvalidSession,
  ]);
  const source = detail?.available
    ? (detail.post ??
      (detail.comment && parent ? commentAsPost(detail.comment, parent) : null))
    : null;
  useEffect(() => {
    if (detail && (!detail.available || source)) onReady?.();
  }, [detail, source, onReady]);
  return (
    <div
      ref={target}
      data-notification-id={item.id}
      data-event-type={item.eventType}
    >
      {!item.available || (detail && !detail.available) ? (
        <NotificationRow item={{ ...item, available: false, excerpt: "" }} />
      ) : source ? (
        <PostPresentation
          post={source}
          site={site}
          navigable
          timelineDensity
          href={notificationHref(item)}
          canPublish={auth.account?.status === "active"}
        />
      ) : (
        <div
          className={styles.contentLoading}
          role={failed ? "alert" : "status"}
        >
          {failed ? (
            <>
              <span>{t("通知内容暂时无法加载")}</span>
              <button
                type="button"
                onClick={() => {
                  parents.delete(item.postId);
                  setAttempt((value) => value + 1);
                }}
              >
                {t("重试")}
              </button>
            </>
          ) : (
            t("正在加载…")
          )}
        </div>
      )}
    </div>
  );
}

export function NotificationsPage({
  scope,
  site,
}: {
  scope: NotificationScope;
  site: SiteIdentity;
}) {
  const { t } = useTranslation();
  const auth = useSession();
  const live = useNotifications();
  const [page, setPage] = useState<InboxPage | null>(null);
  const [error, setError] = useState<"initial" | "more" | null>(null);
  const [ackError, setAckError] = useState(false);
  const [readyBoundary, setReadyBoundary] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const parents = useMemo(
    () => new Map<string, Promise<PublicPost>>(),
    [scope, attempt],
  );
  const top = useRef<HTMLDivElement>(null);
  const atTop = useRef(true);
  const [viewTick, setViewTick] = useState(0);
  const acknowledged = useRef("");
  const morePending = useRef(false);
  const epoch = useRef(0);
  const latestCursor = useRef("");
  const enabled =
    auth.state === "signed-in" && auth.account?.status === "active";
  const load = useCallback(
    (signal: AbortSignal) => {
      const request = ++epoch.current;
      setLoading(true);
      setError(null);
      void interactionsApi
        .list(scope, "", signal)
        .then((result) => {
          if (!signal.aborted && request === epoch.current) {
            setPage(result);
            latestCursor.current = live.progress?.throughCursor ?? "";
          }
        })
        .catch((failure) => {
          if (!signal.aborted) {
            auth.expireIfInvalidSession(failure);
            setError("initial");
          }
        })
        .finally(() => {
          if (!signal.aborted && request === epoch.current) setLoading(false);
        });
    },
    [scope, auth.expireIfInvalidSession, live.progress?.throughCursor],
  );
  const contentReady = useCallback(
    () => setReadyBoundary(page?.throughCursor ?? ""),
    [page?.throughCursor],
  );
  const pageReady =
    page?.items.length === 0 ||
    page?.items[0]?.reasons.includes("like") ||
    readyBoundary === page?.throughCursor;
  const loadRef = useRef(load);
  loadRef.current = load;
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    loadRef.current(controller.signal);
    return () => {
      controller.abort();
      epoch.current++;
    };
  }, [enabled, scope, attempt]);
  useEffect(() => {
    const element = top.current;
    if (!element) return;
    const update = () => {
      atTop.current = window.scrollY <= 4;
      setViewTick((value) => value + 1);
    };
    update();
    window.addEventListener("scroll", update, { passive: true });
    document.addEventListener("visibilitychange", update);
    window.addEventListener("focus", update);
    return () => {
      window.removeEventListener("scroll", update);
      document.removeEventListener("visibilitychange", update);
      window.removeEventListener("focus", update);
    };
  }, []);
  useEffect(() => {
    if (
      !enabled ||
      !atTop.current ||
      document.visibilityState === "hidden" ||
      !live.progress?.throughCursor ||
      live.progress.throughCursor === latestCursor.current
    )
      return;
    latestCursor.current = live.progress.throughCursor;
    setAttempt((value) => value + 1);
  }, [live.progress?.throughCursor, enabled, viewTick]);
  useEffect(() => {
    if (
      !enabled ||
      !page ||
      !pageReady ||
      loading ||
      error ||
      !auth.session ||
      !atTop.current ||
      document.visibilityState === "hidden" ||
      acknowledged.current === page.throughCursor
    )
      return;
    const controller = new AbortController();
    let retry: number | undefined;
    const frame = requestAnimationFrame(() => {
      acknowledged.current = page.throughCursor;
      void interactionsApi
        .seen(
          auth.session!.csrfToken,
          scope,
          page.throughCursor,
          controller.signal,
        )
        .then(() => {
          setAckError(false);
          live.refresh();
        })
        .catch((failure) => {
          acknowledged.current = "";
          if (!controller.signal.aborted) {
            setAckError(true);
            if (!auth.expireIfInvalidSession(failure))
              retry = window.setTimeout(
                () => setViewTick((value) => value + 1),
                2000,
              );
          }
        });
    });
    return () => {
      cancelAnimationFrame(frame);
      controller.abort();
      if (retry) clearTimeout(retry);
    };
  }, [
    page,
    pageReady,
    scope,
    enabled,
    loading,
    error,
    auth.session,
    auth.expireIfInvalidSession,
    viewTick,
    live.refresh,
  ]);
  const loadMore = async () => {
    if (!page?.nextCursor || morePending.current || loading) return;
    morePending.current = true;
    setLoadingMore(true);
    const request = epoch.current;
    try {
      const result = await interactionsApi.list(scope, page.nextCursor);
      if (request === epoch.current)
        setPage((current) =>
          current
            ? {
                ...current,
                items: [
                  ...current.items,
                  ...result.items.filter(
                    (item) => !current.items.some((old) => old.id === item.id),
                  ),
                ],
                nextCursor: result.nextCursor,
              }
            : current,
        );
    } catch (failure) {
      if (request !== epoch.current) return;
      auth.expireIfInvalidSession(failure);
      setError("more");
    } finally {
      morePending.current = false;
      setLoadingMore(false);
    }
  };
  return (
    <section className={styles.page} aria-label={t("通知")}>
      <nav className={styles.tabs} aria-label={t("通知筛选")}>
        {(
          [
            ["all", t("全部")],
            ["mentions", t("提及")],
          ] as const
        ).map(([value, label]) => (
          <InternalLink
            key={value}
            href={
              value === "all"
                ? "/account/notifications"
                : "/account/notifications/mentions"
            }
            aria-current={scope === value ? "page" : undefined}
          >
            <span>{label}</span>
          </InternalLink>
        ))}
      </nav>
      <div ref={top} className={styles.top} />
      {error && page ? (
        <div role="alert" className={styles.status}>
          <span>{t("通知暂时无法加载")}</span>
          <button
            type="button"
            onClick={() =>
              error === "more"
                ? void loadMore()
                : setAttempt((value) => value + 1)
            }
          >
            {t("重试")}
          </button>
        </div>
      ) : null}
      {!enabled ? (
        <p className={styles.status}>{t("账号通过审核后即可查看通知")}</p>
      ) : loading && !page ? (
        <p className={styles.status} role="status">
          {t("正在加载…")}
        </p>
      ) : error && !page ? (
        <div className={styles.status} role="alert">
          {t("通知暂时无法加载")}
          <button
            type="button"
            onClick={() => setAttempt((value) => value + 1)}
          >
            {t("重试")}
          </button>
        </div>
      ) : page?.items.length === 0 ? (
        <div className={styles.empty}>
          <h2>
            {scope === "mentions"
              ? t("暂时还没有新消息")
              : t("这里会显示你的通知")}
          </h2>
          <p>
            {scope === "mentions"
              ? t("当有人提及你或回复你时，会显示在这里。")
              : t("点赞、回复和提及会显示在这里。")}
          </p>
        </div>
      ) : (
        <>
          {page?.items.map((item, index) =>
            item.reasons.includes("like") ? (
              <NotificationRow key={item.id} item={item} />
            ) : (
              <NotificationContent
                key={item.id}
                item={item}
                site={site}
                parents={parents}
                onReady={index === 0 ? contentReady : undefined}
              />
            ),
          )}
          {page?.nextCursor ? (
            <LoadMoreSentinel
              className={styles.status}
              cursor={page.nextCursor}
              label={t("加载更多")}
              loading={loadingMore}
              onLoad={() => void loadMore()}
            />
          ) : null}
        </>
      )}
      {ackError && enabled ? (
        <p className={styles.connection} role="status">
          {t("正在同步查看进度…")}
        </p>
      ) : null}
      {live.reconnecting && enabled ? (
        <p className={styles.connection} role="status">
          {t("正在重新连接通知…")}
        </p>
      ) : null}
    </section>
  );
}
