import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useTranslation } from "react-i18next";
import {
  browserAccountsApi,
  type BrowserAccounts,
} from "../../api/browserAccounts";
import { PublicApiError, type PublicAccount } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { notify } from "../../components/Feedback";
import { useSession } from "./SessionProvider";
import { accountCacheScope } from "./accountScope";
import { clearComposeDraft } from "../compose/draft";
import { clearKeptDrafts } from "../compose/drafts";

function useBrowserAccountsState() {
  const { t } = useTranslation();
  const auth = useSession();
  const authRef = useRef(auth);
  authRef.current = auth;
  const navigation = useNavigation();
  const [snapshot, setSnapshot] = useState<BrowserAccounts | null>(null);
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [menuOpen, setMenuOpen] = useState(false);
  const [mode, setMode] = useState<"add" | "manage" | null>(() =>
    navigation.pathname === "/account/add" ? "add" : null,
  );
  const [username, setUsername] = useState("");
  const addParent = useRef<"manage" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [removal, setRemoval] = useState<{
    account: PublicAccount | null;
    focus: HTMLElement | null;
  } | null>(null);
  const pending = useRef(false);
  const generation = useRef(0);

  const load = useCallback(async (signal?: AbortSignal) => {
    const started = generation.current;
    let result: BrowserAccounts;
    try {
      result = await browserAccountsApi.list(signal);
    } catch (error) {
      if (
        !(error instanceof PublicApiError && error.status === 401) ||
        !authRef.current.session ||
        signal?.aborted
      )
        throw error;
      await browserAccountsApi.bootstrap(authRef.current.session.csrfToken);
      result = await browserAccountsApi.list(signal);
    }
    if (!signal?.aborted && started === generation.current) {
      setSnapshot(result);
      setError(null);
    }
    return result;
  }, []);

  const refresh = useCallback(
    async (signal?: AbortSignal) => {
      const started = generation.current;
      try {
        await load(signal);
        const next = await browserAccountsApi.counts(signal);
        if (!signal?.aborted && started === generation.current) setCounts(next);
      } catch {
        if (!signal?.aborted && started === generation.current)
          setError(t("账户列表暂时无法刷新，请重试。"));
      }
    },
    [load, t],
  );

  useEffect(() => {
    if (!menuOpen && mode === null) return;
    const controller = new AbortController();
    const update = () => {
      if (document.visibilityState !== "hidden")
        void refresh(controller.signal);
    };
    update();
    const timer = window.setInterval(update, 15000);
    window.addEventListener("focus", update);
    document.addEventListener("visibilitychange", update);
    return () => {
      controller.abort();
      clearInterval(timer);
      window.removeEventListener("focus", update);
      document.removeEventListener("visibilitychange", update);
    };
  }, [menuOpen, mode, refresh, auth.account?.userId]);

  const mutate = async (
    operation: (csrf: string) => Promise<void>,
    changed: boolean,
    removed: string[] | "all",
    instanceId: string | null,
    expectedAccount?: string,
  ) => {
    if (pending.current) throw new Error(t("账户操作正在进行，请稍候。"));
    pending.current = true;
    setBusy(true);
    setError(null);
    generation.current++;
    try {
      const current = snapshot ?? (await load());
      let identityResolved = false;
      try {
        await operation(current.csrfToken);
      } catch (error) {
        if (
          error instanceof PublicApiError &&
          error.status >= 400 &&
          error.status < 500
        )
          throw error;
        // A lost response is reconciled against the observable result, so a
        // committed exit is not left behind an endless retry confirmation.
        let completed = false;
        if (removed === "all" || removed.length > 0) {
          try {
            const actual = await browserAccountsApi.list();
            completed =
              removed === "all"
                ? actual.accounts.length === 0
                : removed.every(
                    (id) =>
                      !actual.accounts.some(
                        (entry) => entry.account.userId === id,
                      ),
                  );
          } catch (readError) {
            completed =
              removed === "all" &&
              readError instanceof PublicApiError &&
              readError.status === 401;
          }
        } else if (changed) {
          const actual = await auth.refreshSession();
          identityResolved = true;
          completed =
            !!actual &&
            (expectedAccount
              ? actual.userId === expectedAccount
              : actual.userId !== auth.account?.userId);
        }
        if (!completed) throw error;
      }
      if (changed) {
        if (!identityResolved) await auth.refreshSession(removed.length === 0);
        setMode(null);
        navigation.replace("/");
      }
      for (const id of removed === "all"
        ? current.accounts.map((entry) => entry.account.userId)
        : removed) {
        const scope = accountCacheScope(instanceId, id);
        clearComposeDraft(scope, { reason: "logout" });
        clearKeptDrafts(scope);
      }
      setSnapshot(null);
      setCounts({});
      generation.current++;
      try {
        await load();
      } catch (error) {
        if (!(error instanceof PublicApiError && error.status === 401))
          setError(t("账户列表暂时无法刷新，请重试。"));
      }
    } finally {
      pending.current = false;
      setBusy(false);
    }
  };

  const activate = (id: string) =>
    navigation.runAfterLeave(() => {
      void mutate(
        (csrf) => browserAccountsApi.activate(csrf, id),
        true,
        [],
        null,
        id,
      ).catch(() => notify(t("账户切换未完成，请重试。"), { type: "error" }));
    });
  const openAdd = (name = "") =>
    navigation.runAfterLeave(() => {
      addParent.current = mode === "manage" ? "manage" : null;
      setUsername(name);
      setMode("add");
    });
  const close = () => {
    if (busy) return;
    setMode(mode === "add" ? addParent.current : null);
    if (navigation.pathname === "/account/add") navigation.replace("/");
  };

  return {
    snapshot,
    counts,
    error,
    busy,
    menuOpen,
    setMenuOpen,
    mode,
    username,
    refresh,
    removal,
    requestRemove: (
      account: PublicAccount | null,
      focus: HTMLElement | null = null,
    ) => navigation.runAfterLeave(() => setRemoval({ account, focus })),
    cancelRemove: () => {
      if (!busy) setRemoval(null);
    },
    activate,
    openAdd,
    openManage: () => setMode("manage"),
    close,
    add: (credential: string, method: "password" | "token", name?: string) =>
      mutate(
        (csrf) => browserAccountsApi.add(csrf, credential, method, name),
        true,
        [],
        null,
      ),
    startOAuth: async (provider: string) => {
      const current = snapshot ?? (await load());
      return browserAccountsApi.startOAuth(current.csrfToken, provider);
    },
    remove: async (id: string | undefined, instanceId: string | null) => {
      if (id && id === auth.account?.userId) {
        if (pending.current) throw new Error(t("账户操作正在进行，请稍候。"));
        pending.current = true;
        setBusy(true);
        try {
          await auth.logout(instanceId);
          generation.current++;
          setSnapshot(null);
          setCounts({});
          setMode(null);
          setRemoval(null);
          navigation.replace("/");
        } finally {
          pending.current = false;
          setBusy(false);
        }
        return;
      }
      await mutate(
        (csrf) => browserAccountsApi.remove(csrf, id),
        !id || id === auth.account?.userId,
        id ? [id] : "all",
        instanceId,
      );
      setRemoval(null);
    },
  };
}

const Context = createContext<ReturnType<
  typeof useBrowserAccountsState
> | null>(null);
export function BrowserAccountsProvider({ children }: { children: ReactNode }) {
  const value = useBrowserAccountsState();
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useBrowserAccounts() {
  return useContext(Context);
}
