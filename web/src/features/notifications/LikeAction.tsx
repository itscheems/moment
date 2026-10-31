import {
  createContext,
  useContext,
  useMemo,
  useEffect,
  useSyncExternalStore,
  useState,
  useRef,
  type ReactNode,
} from "react";
import { useTranslation } from "react-i18next";
import {
  interactionsApi,
  type LikeState,
  type SubjectKind,
} from "../../api/interactions";
import { useOptionalSession } from "../auth/SessionProvider";
import { useLoginNavigation } from "../auth/loginNavigation";
import {
  ActionTooltip,
  type ActionTooltipPlacement,
} from "../../components/TimelineInteractions";
import { notify } from "../../components/Feedback";
import { Icon } from "../../components/icons";
import postStyles from "../posts/PostPresentation.module.css";
import styles from "./Notifications.module.css";

interface LikeRecord {
  state: LikeState;
  loaded: boolean;
  error: boolean;
}
const EMPTY: LikeRecord = {
  state: { count: 0, liked: false },
  loaded: false,
  error: false,
};
class LikeCache {
  private records = new Map<string, LikeRecord>();
  private listeners = new Map<string, Set<() => void>>();
  private pending = new Set<string>();
  private queued = false;
  get = (key: string) => this.records.get(key) ?? EMPTY;
  set(key: string, value: LikeRecord) {
    this.records.set(key, value);
    this.listeners.get(key)?.forEach((fn) => fn());
  }
  subscribe = (key: string, listener: () => void) => {
    const set = this.listeners.get(key) ?? new Set();
    set.add(listener);
    this.listeners.set(key, set);
    if (!this.get(key).loaded) this.enqueue(key);
    return () => {
      set.delete(listener);
    };
  };
  private enqueue(key: string) {
    this.pending.add(key);
    if (this.queued) return;
    this.queued = true;
    queueMicrotask(() => {
      this.queued = false;
      const keys = [...this.pending];
      this.pending.clear();
      for (const kind of ["post", "comment"] as const) {
        const selected = keys.filter((key) => key.startsWith(kind + ":"));
        for (let i = 0; i < selected.length; i += 100) {
          const batch = selected.slice(i, i + 100);
          void interactionsApi
            .likes(
              kind,
              batch.map((key) => key.slice(kind.length + 1)),
            )
            .then((states) => {
              for (const key of batch) {
                if (this.get(key).loaded) continue;
                const state = states[key.slice(kind.length + 1)];
                this.set(
                  key,
                  state
                    ? { state, loaded: true, error: false }
                    : { ...EMPTY, loaded: true, error: true },
                );
              }
            })
            .catch(() => {
              for (const key of batch)
                if (!this.get(key).loaded)
                  this.set(key, {
                    ...this.get(key),
                    loaded: true,
                    error: true,
                  });
            });
        }
      }
    });
  }
}
const CacheContext = createContext<LikeCache | null>(null);
export function LikeProvider({
  children,
  scope,
}: {
  children: ReactNode;
  scope: string;
}) {
  const cache = useMemo(() => new LikeCache(), [scope]);
  return (
    <CacheContext.Provider value={cache}>{children}</CacheContext.Provider>
  );
}
const noopSubscribe = () => () => {};
export function LikeAction({
  kind,
  id,
  tooltipPlacement,
}: {
  kind: SubjectKind;
  id: string;
  tooltipPlacement?: ActionTooltipPlacement;
}) {
  const { t } = useTranslation();
  const auth = useOptionalSession();
  const { requestLogin } = useLoginNavigation();
  const cache = useContext(CacheContext);
  const key = kind + ":" + id;
  const record = useSyncExternalStore(
    useMemo(
      () =>
        cache ? (fn: () => void) => cache.subscribe(key, fn) : noopSubscribe,
      [cache, key],
    ),
    () => cache?.get(key) ?? EMPTY,
    () => EMPTY,
  );
  const [pending, setPending] = useState(false);
  const pendingRef = useRef(false);
  const identity = auth?.session?.version ?? "visitor";
  const current = useRef({ cache, key, identity });
  current.current = { cache, key, identity };
  useEffect(() => {
    pendingRef.current = false;
    setPending(false);
  }, [cache, key, identity]);
  const active = !record.error && record.state.liked;
  const label = record.error
    ? t("重试读取点赞状态")
    : active
      ? t("取消点赞")
      : t("点赞");
  const change = async () => {
    if (!auth?.session || auth.state !== "signed-in") {
      requestLogin();
      return;
    }
    if (auth.account?.status !== "active") {
      notify(t("账号通过审核后即可点赞"), { type: "error" });
      return;
    }
    if (pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    let before = cache?.get(key) ?? EMPTY;
    const isCurrent = () =>
      current.current.cache === cache &&
      current.current.key === key &&
      current.current.identity === identity;
    const retryOnly = before.error;
    try {
      if (before.error || !before.loaded) {
        const states = await interactionsApi.likes(kind, [id]);
        if (!states[id]) throw new Error("unavailable");
        if (!isCurrent()) return;
        before = { state: states[id], loaded: true, error: false };
        if (retryOnly) {
          cache?.set(key, before);
          return;
        }
      }
      const next = !before.state.liked;
      cache?.set(key, {
        state: {
          liked: next,
          count: Math.max(0, before.state.count + (next ? 1 : -1)),
        },
        loaded: true,
        error: false,
      });
      const state = await interactionsApi.setLike(
        auth.session.csrfToken,
        kind,
        id,
        next,
      );
      if (isCurrent()) cache?.set(key, { state, loaded: true, error: false });
    } catch (error) {
      if (!isCurrent()) return;
      let refreshed = false;
      try {
        const states = await interactionsApi.likes(kind, [id]);
        if (!isCurrent()) return;
        if (states[id]) {
          cache?.set(key, { state: states[id], loaded: true, error: false });
          refreshed = true;
        }
      } catch {
        // Keep an explicit unknown state when the reconciliation read is unavailable.
      }
      if (!isCurrent()) return;
      if (!refreshed) cache?.set(key, { ...before, error: true, loaded: true });
      const expired = auth.expireIfInvalidSession(error);
      notify(
        expired
          ? t("登录已过期，重新登录后再操作。")
          : refreshed
            ? t("请求未确认，已重新读取点赞状态")
            : t("点赞状态暂时无法读取，请重试"),
        { type: "error" },
      );
    } finally {
      if (isCurrent()) {
        pendingRef.current = false;
        setPending(false);
      }
    }
  };
  return (
    <ActionTooltip label={label} placement={tooltipPlacement}>
      <button
        aria-label={label}
        aria-pressed={record.error ? undefined : active}
        data-post-action="like"
        className={`${postStyles.postAction} ${styles.like} ${active ? styles.liked : ""}`}
        disabled={pending}
        onClick={(event) => {
          event.stopPropagation();
          void change();
        }}
        type="button"
      >
        <Icon name={active ? "heart-filled" : "heart"} />
        {!record.error && record.state.count > 0 ? (
          <span className={postStyles.actionCount}>{record.state.count}</span>
        ) : null}
      </button>
    </ActionTooltip>
  );
}
