import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  interactionsApi,
  parseNotificationState,
  type NotificationState,
} from "../../api/interactions";
import { useOptionalSession } from "../auth/SessionProvider";

interface InboxState {
  progress: NotificationState | null;
  reconnecting: boolean;
  refresh: () => void;
}
const Context = createContext<InboxState>({
  progress: null,
  reconnecting: false,
  refresh: () => {},
});
export function NotificationProvider({ children }: { children: ReactNode }) {
  const auth = useOptionalSession();
  const [progress, setProgress] = useState<{
    owner: string;
    value: NotificationState;
  } | null>(null);
  const [reconnecting, setReconnecting] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const enabled =
    auth?.state === "signed-in" && auth.account?.status === "active";
  const expire = auth?.expireIfInvalidSession;
  useEffect(() => {
    if (!enabled) return;
    let source: EventSource | null = null;
    let disposed = false;
    let revision = 0;
    let retry: ReturnType<typeof setTimeout> | undefined;
    const controller = new AbortController();
    const check = () => {
      const started = revision;
      void interactionsApi
        .state(controller.signal)
        .then((state) => {
          if (!disposed && started === revision)
            setProgress({ owner: auth!.account!.userId, value: state });
        })
        .catch((error) => {
          if (!disposed) {
            expire?.(error);
            setReconnecting(true);
          }
        });
    };
    const connect = () => {
      if (retry) clearTimeout(retry);
      source?.close();
      source = null;
      if (document.visibilityState === "hidden") return;
      check();
      if (typeof EventSource === "undefined") return;
      source = new EventSource("/api/v1/account/notifications/stream");
      const connection = source;
      source.addEventListener("sync", (event) => {
        if (disposed || source !== connection) return;
        try {
          revision++;
          setProgress({
            owner: auth!.account!.userId,
            value: parseNotificationState(
              JSON.parse((event as MessageEvent).data),
            ),
          });
          setReconnecting(false);
        } catch {
          setReconnecting(true);
        }
      });
      source.addEventListener("closed", () => {
        if (disposed || source !== connection) return;
        source?.close();
        source = null;
        setReconnecting(true);
        check();
        retry = setTimeout(connect, 3000);
      });
      source.onerror = () => {
        if (!disposed && source === connection) {
          setReconnecting(true);
          check();
        }
      };
    };
    connect();
    document.addEventListener("visibilitychange", connect);
    return () => {
      disposed = true;
      if (retry) clearTimeout(retry);
      controller.abort();
      source?.close();
      document.removeEventListener("visibilitychange", connect);
    };
  }, [enabled, auth?.account?.userId, auth?.session?.version, attempt, expire]);
  const refresh = useCallback(() => setAttempt((value) => value + 1), []);
  const value = useMemo(
    () => ({
      progress:
        enabled && progress && progress.owner === auth?.account?.userId
          ? progress.value
          : null,
      reconnecting,
      refresh,
    }),
    [enabled, auth?.account?.userId, progress, reconnecting, refresh],
  );
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useNotifications() {
  return useContext(Context);
}
