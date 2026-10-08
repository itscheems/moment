import { Menu } from "@base-ui/react/menu";
import { Tabs } from "@base-ui/react/tabs";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  isReadArticlePost,
  type PublicPost,
  type SiteIdentity,
} from "../../../api/public";
import { useNavigation } from "../../../app/navigation";
import {
  ARTICLE_COMPOSE_PATH,
  articleDraftPath,
  articlePublishedPath,
  articleFocusPath,
  articleWorkspaceFromPath,
} from "../../../app/routes";
import { Icon } from "../../../components/icons";
import { notify } from "../../../components/Feedback";
import { useMediaQuery } from "../../../components/useMediaQuery";
import { useSession } from "../../auth/SessionProvider";
import { ConfirmDialog } from "../../owner/ConfirmDialog";
import {
  announceOwnerPost,
  listenForPublishedPosts,
} from "../../owner/postEvents";
import { writeToClipboard } from "../../../components/content/notion/lib/clipboard";
import {
  ArticleDraftApi,
  articleDraftErrorMessage,
  type ArticleDraft,
  type ArticleDraftSummary,
} from "../articleDrafts";
import { ArticlePage } from "../ArticlePage";
import { formatMomentDate } from "../../posts/dates";
import { ArticleEditor } from "./ArticleEditor";
import { emptyArticleDocument } from "./document";
import styles from "./Editor.module.css";

function asSummary(draft: ArticleDraft): ArticleDraftSummary {
  return {
    id: draft.id,
    title: draft.title,
    publishedEntryId: draft.publishedEntryId,
    createdAt: draft.createdAt,
    updatedAt: draft.updatedAt,
    authorUserId: draft.authorUserId,
    sourceState: draft.sourceState,
    sourceVisibility: draft.sourceVisibility,
  };
}

export default function ArticleWorkspace({ site }: { site: SiteIdentity }) {
  const session = useSession();
  const account = session.account;
  const navigation = useNavigation();
  const route = articleWorkspaceFromPath(navigation.pathname);
  const api = useMemo(() => new ArticleDraftApi(), []);
  const narrow = useMediaQuery("(max-width: 1023px)");
  const [drafts, setDrafts] = useState<ArticleDraftSummary[]>([]);
  const [published, setPublished] = useState<PublicPost[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [reading, setReading] = useState<PublicPost | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [readError, setReadError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const lastSelected = useRef<{
    drafts: string | null;
    published: string | null;
  }>({ drafts: null, published: null });
  const listRef = useRef<HTMLDivElement>(null);
  const detailRef = useRef<HTMLDivElement>(null);
  const detailScroll = useRef(new Map<string, number>());
  const detailKey = `${route.section}:${route.id ?? ""}`;
  const listScroll = useRef({ drafts: 0, published: 0 });
  const generation = useRef(0);
  const draftVersions = useRef(new Map<string, number>());
  const draftRevision = useRef(0);
  const accountId = useRef(account?.userId);
  accountId.current = account?.userId;
  const createKey = useRef<string | null>(null);
  const creating = useRef(false);
  const csrf = session.session?.csrfToken ?? "";
  const authorSite: SiteIdentity = account
    ? {
        ...site,
        ownerId: account.userId,
        username: account.username,
        displayName: account.displayName,
        avatarMediaId: account.avatarMediaId,
        coverMediaId: account.coverMediaId,
        bio: account.bio,
      }
    : site;

  const refresh = useCallback(
    async (signal?: AbortSignal) => {
      const owner = accountId.current;
      const sequence = ++generation.current;
      const revision = draftRevision.current;
      try {
        const [summaries, page] = await Promise.all([
          api.list(signal),
          api.published(undefined, signal),
        ]);
        if (
          signal?.aborted ||
          sequence !== generation.current ||
          owner !== accountId.current
        )
          return;
        setDrafts((previous) => {
          const newer = previous.filter(
            (draft) => (draftVersions.current.get(draft.id) ?? 0) > revision,
          );
          return [
            ...newer,
            ...summaries.filter(
              (draft) => !newer.some((local) => local.id === draft.id),
            ),
          ];
        });
        setPublished((previous) =>
          page.nextCursor === null
            ? page.items
            : [
                ...page.items,
                ...previous.filter(
                  (item) =>
                    !page.items.some((newItem) => newItem.id === item.id) &&
                    item.publishedAt <
                      page.items[page.items.length - 1]!.publishedAt,
                ),
              ],
        );
        setCursor((previous) =>
          page.nextCursor === null ? null : (previous ?? page.nextCursor),
        );
        setError(null);
      } catch (error) {
        if (!signal?.aborted && sequence === generation.current) {
          session.expireIfInvalidSession(error);
          setError(articleDraftErrorMessage(error));
        }
      } finally {
        if (!signal?.aborted && sequence === generation.current)
          setLoading(false);
      }
    },
    [api, session.expireIfInvalidSession],
  );

  useEffect(() => {
    const controller = new AbortController();
    setDrafts([]);
    setPublished([]);
    setReading(null);
    setCursor(null);
    setLoading(true);
    void refresh(controller.signal);
    const changed = (
      notice?: import("../../owner/postEvents").PublishedPostNotice,
    ) => {
      if (notice?.authorId && notice.authorId !== accountId.current) return;
      if (notice?.action === "unpublished" || notice?.action === "deleted")
        setPublished((previous) =>
          previous.filter((post) => post.id !== notice.id),
        );
      if (document.visibilityState === "visible")
        void refresh(controller.signal);
    };
    const focused = () => changed();
    const off = listenForPublishedPosts(changed);
    window.addEventListener("focus", focused);
    document.addEventListener("visibilitychange", focused);
    return () => {
      controller.abort();
      generation.current++;
      off();
      window.removeEventListener("focus", focused);
      document.removeEventListener("visibilitychange", focused);
    };
  }, [account?.userId, refresh]);

  useLayoutEffect(() => {
    if (detailRef.current)
      detailRef.current.scrollTop = detailScroll.current.get(detailKey) ?? 0;
  }, [detailKey]);

  useEffect(() => {
    if (route.id) lastSelected.current[route.section] = route.id;
    if (listRef.current)
      listRef.current.scrollTop = listScroll.current[route.section];
  }, [route.section, route.id]);
  useEffect(() => {
    if (route.section !== "published" || !route.id) {
      setReading(null);
      setReadError(null);
      return;
    }
    const controller = new AbortController();
    const owner = accountId.current;
    setReading(null);
    setReadError(null);
    void api
      .readManaged(route.id, controller.signal)
      .then((post) => {
        if (!controller.signal.aborted && owner === accountId.current)
          setReading(post);
      })
      .catch((error) => {
        if (!controller.signal.aborted) {
          session.expireIfInvalidSession(error);
          setReadError(articleDraftErrorMessage(error));
        }
      });
    return () => controller.abort();
  }, [api, route.section, route.id, session.expireIfInvalidSession]);

  const announceChange = useCallback(
    (
      id: string,
      action: "draft" | "published" | "unpublished" | "deleted" = "draft",
    ) => {
      if (typeof window.BroadcastChannel !== "function") return;
      try {
        const channel = new window.BroadcastChannel("moment.posts.v1");
        channel.postMessage({
          scope: window.location.origin,
          id,
          kind: "article",
          authorId: accountId.current,
          action,
        });
        channel.close();
      } catch {
        /* Cross-tab invalidation is optional; focus refresh remains available. */
      }
    },
    [],
  );
  const saved = useCallback(
    (draft: ArticleDraft) => {
      draftVersions.current.set(draft.id, ++draftRevision.current);
      setDrafts((previous) =>
        [
          asSummary(draft),
          ...previous.filter((item) => item.id !== draft.id),
        ].sort(
          (a, b) =>
            b.updatedAt.localeCompare(a.updatedAt) || b.id.localeCompare(a.id),
        ),
      );
      announceChange(draft.id);
    },
    [announceChange],
  );
  const newArticle = useCallback(async () => {
    if (creating.current || !csrf) return;
    creating.current = true;
    setBusy(true);
    const owner = accountId.current;
    createKey.current ??= crypto.randomUUID();
    try {
      const draft = await api.create(
        csrf,
        { title: "", body: emptyArticleDocument(), coverMediaId: null },
        createKey.current,
      );
      if (owner !== accountId.current) return;
      createKey.current = null;
      saved(draft);
      navigation.navigate(articleDraftPath(draft.id), { preserveScroll: true });
    } catch (error) {
      session.expireIfInvalidSession(error);
      notify(articleDraftErrorMessage(error), { type: "error" });
    } finally {
      creating.current = false;
      setBusy(false);
    }
  }, [api, csrf, navigation, saved, session.expireIfInvalidSession]);
  const edit = async (post: PublicPost) => {
    if (busy) return;
    setBusy(true);
    try {
      const draft = await api.openPublished(csrf, post.id);
      saved(draft);
      navigation.navigate(articleDraftPath(draft.id), { preserveScroll: true });
    } catch (error) {
      notify(articleDraftErrorMessage(error), { type: "error" });
    } finally {
      setBusy(false);
    }
  };
  const withdraw = async (post: PublicPost) => {
    if (busy) return;
    setBusy(true);
    try {
      const draft = await api.unpublish(csrf, post.id);
      saved(draft);
      setPublished((previous) =>
        previous.filter((item) => item.id !== post.id),
      );
      announceOwnerPost({ kind: "deleted", post });
      announceChange(post.id, "unpublished");
      navigation.replace(articleDraftPath(draft.id));
      notify("文章已移至草稿。");
    } catch (error) {
      session.expireIfInvalidSession(error);
      notify(articleDraftErrorMessage(error), { type: "error" });
    } finally {
      setBusy(false);
    }
  };
  const remove = async (
    item: ArticleDraftSummary | PublicPost,
    isPublished: boolean,
  ) => {
    if (busy) return;
    setBusy(true);
    try {
      if (isPublished) {
        await api.removeArticle(csrf, item.id);
        setPublished((previous) =>
          previous.filter((row) => row.id !== item.id),
        );
        announceOwnerPost({ kind: "deleted", post: item as PublicPost });
      } else {
        await api.discard(csrf, item.id);
        setDrafts((previous) => previous.filter((row) => row.id !== item.id));
      }
      announceChange(item.id, "deleted");
      if (route.id === item.id)
        navigation.replace(
          isPublished ? articlePublishedPath() : ARTICLE_COMPOSE_PATH,
        );
      void refresh();
      notify("文章已删除。");
    } catch (error) {
      notify(articleDraftErrorMessage(error), { type: "error" });
    } finally {
      setBusy(false);
    }
  };
  const link = (post: PublicPost) =>
    articleFocusPath(post.author?.username ?? authorSite.username, post.id);
  const copyLink = async (post: PublicPost) => {
    try {
      await writeToClipboard(new URL(link(post), window.location.origin).href);
      notify("链接已复制");
    } catch {
      notify("暂时无法复制链接", { type: "error" });
    }
  };
  const loadMore = async () => {
    if (!cursor || loadingMore) return;
    setLoadingMore(true);
    const owner = accountId.current;
    try {
      const page = await api.published(cursor);
      if (owner !== accountId.current) return;
      setPublished((previous) => [
        ...previous,
        ...page.items.filter(
          (item) => !previous.some((old) => old.id === item.id),
        ),
      ]);
      setCursor(page.nextCursor);
      setError(null);
    } catch (error) {
      setError(articleDraftErrorMessage(error));
    } finally {
      setLoadingMore(false);
    }
  };
  const changeTab = (value: unknown) => {
    const section = value === "published" ? "published" : "drafts";
    if (listRef.current)
      listScroll.current[route.section] = listRef.current.scrollTop;
    const selected = lastSelected.current[section];
    navigation.navigate(
      section === "published"
        ? articlePublishedPath(selected ?? undefined)
        : selected
          ? articleDraftPath(selected)
          : ARTICLE_COMPOSE_PATH,
      { preserveScroll: true },
    );
  };
  const menu = (
    item: ArticleDraftSummary | PublicPost,
    isPublished: boolean,
  ) => (
    <Menu.Root>
      <Menu.Trigger
        aria-label={`更多：${isPublished ? (item as PublicPost).article?.title : (item as ArticleDraftSummary).title || "无标题"}`}
        className={styles.toolButton}
      >
        <Icon name="more" />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner
          className={styles.menuPositioner}
          side="bottom"
          align="end"
          sideOffset={4}
          collisionPadding={8}
        >
          <Menu.Popup className={styles.menuPopup} aria-label="文章操作">
            {isPublished ? (
              <>
                <Menu.Item
                  className={styles.menuItem}
                  onClick={() => void copyLink(item as PublicPost)}
                >
                  <Icon name="article-link" />
                  复制文章链接
                </Menu.Item>
                <Menu.Item
                  className={styles.menuItem}
                  onClick={() => navigation.navigate(link(item as PublicPost))}
                >
                  <Icon name="article-link" />
                  查看文章
                </Menu.Item>
                <Menu.Item
                  disabled={busy}
                  className={styles.menuItem}
                  onClick={() => void edit(item as PublicPost)}
                >
                  <Icon name="edit" />
                  编辑文章
                </Menu.Item>
                <Menu.Item
                  disabled={busy}
                  className={styles.menuItem}
                  onClick={() => void withdraw(item as PublicPost)}
                >
                  <Icon name="article-unpublish" />
                  取消发布，移至草稿
                </Menu.Item>
              </>
            ) : null}
            <ConfirmDialog
              menuItem
              title="删除这篇文章？"
              description={
                isPublished ||
                (item as ArticleDraftSummary).sourceState === "withdrawn"
                  ? "文章、关联草稿和评论将被永久删除，无法找回。"
                  : "删除这份草稿后无法找回。已发布的文章不受影响。"
              }
              confirmLabel="删除文章"
              danger
              pending={busy}
              onConfirm={() => remove(item, isPublished)}
              trigger={
                <Menu.Item
                  closeOnClick={false}
                  disabled={busy}
                  className={`${styles.menuItem} ${styles.menuItemDanger}`}
                >
                  <Icon name="delete" />
                  删除文章
                </Menu.Item>
              }
            />
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
  const writeAction = (
    <button
      className={styles.primaryAction}
      disabled={busy}
      onClick={() => void newArticle()}
      type="button"
    >
      {busy ? "正在创建…" : "写文章"}
    </button>
  );
  return (
    <section
      className={`${styles.workspace} ${route.focus ? styles.workspaceFocused : ""}`}
      data-article-theme
      data-article-workspace
    >
      <aside
        className={styles.workspaceList}
        hidden={route.focus || (narrow && route.id !== null)}
      >
        <header className={styles.workspaceListHeader}>
          <h1>文章</h1>
        </header>
        <Tabs.Root value={route.section} onValueChange={changeTab}>
          <Tabs.List className={styles.workspaceTabs} aria-label="文章列表">
            <Tabs.Tab className={styles.workspaceTab} value="drafts">
              草稿
            </Tabs.Tab>
            <Tabs.Tab className={styles.workspaceTab} value="published">
              已发布
            </Tabs.Tab>
            <Tabs.Indicator className={styles.workspaceIndicator} />
          </Tabs.List>
        </Tabs.Root>
        <div
          className={styles.workspaceListScroll}
          ref={listRef}
          onScroll={(event) => {
            listScroll.current[route.section] = event.currentTarget.scrollTop;
          }}
        >
          {loading ? (
            <p className={styles.workspaceNotice} role="status">
              正在读取文章…
            </p>
          ) : null}
          {error ? (
            <div className={styles.workspaceNotice} role="alert">
              <p>{error}</p>
              <button
                className={styles.secondaryAction}
                onClick={() => void refresh()}
                type="button"
              >
                重试
              </button>
            </div>
          ) : null}
          <ol className={styles.workspaceRows}>
            {route.section === "drafts"
              ? drafts.map((draft) => (
                  <li
                    className={styles.workspaceRow}
                    data-selected={route.id === draft.id || undefined}
                    key={draft.id}
                  >
                    <button
                      type="button"
                      className={styles.workspaceRowOpen}
                      onClick={() =>
                        navigation.navigate(articleDraftPath(draft.id), {
                          preserveScroll: true,
                        })
                      }
                    >
                      <span className={styles.workspaceRowMeta}>
                        <span className={styles.draftBadge}>
                          {draft.sourceState === "working_copy"
                            ? "工作副本"
                            : "草稿"}
                        </span>
                        <span aria-hidden="true">·</span>
                        <time dateTime={draft.updatedAt}>
                          {
                            formatMomentDate(draft.updatedAt, site.timezone)
                              .relative
                          }
                          保存
                        </time>
                      </span>
                      <span className={styles.workspaceRowContent}>
                        <strong className={styles.workspaceRowTitle}>
                          {draft.title.trim() || "无标题"}
                        </strong>
                      </span>
                    </button>
                    <div className={styles.workspaceRowMenu}>
                      {menu(draft, false)}
                    </div>
                  </li>
                ))
              : published.map((post) => (
                  <li
                    className={styles.workspaceRow}
                    data-selected={route.id === post.id || undefined}
                    key={post.id}
                  >
                    <button
                      type="button"
                      className={styles.workspaceRowOpen}
                      onClick={() =>
                        navigation.navigate(articlePublishedPath(post.id), {
                          preserveScroll: true,
                        })
                      }
                    >
                      <span className={styles.workspaceRowMeta}>
                        <span className={styles.publishedBadge}>已发布</span>
                        <span aria-hidden="true">·</span>
                        <time dateTime={post.publishedAt}>
                          {
                            formatMomentDate(post.publishedAt, site.timezone)
                              .relative
                          }
                        </time>
                      </span>
                      <span className={styles.workspaceRowContent}>
                        <strong className={styles.workspaceRowTitle}>
                          {post.article?.title}
                        </strong>
                        {post.article?.excerpt ? (
                          <span className={styles.workspaceExcerpt}>
                            {post.article.excerpt}
                          </span>
                        ) : null}
                      </span>
                    </button>
                    <div className={styles.workspaceRowMenu}>
                      {menu(post, true)}
                    </div>
                  </li>
                ))}
          </ol>
          {!loading &&
          !error &&
          (route.section === "drafts" ? drafts : published).length === 0 ? (
            <div className={styles.workspaceEmptyList}>
              <h2>
                {route.section === "drafts"
                  ? "草稿会保存在这里"
                  : "还没有已发布的文章"}
              </h2>
              <p>
                {route.section === "drafts"
                  ? "开始写作，随时回来继续。"
                  : "发布后的文章将在这里显示。"}
              </p>
            </div>
          ) : null}
          {route.section === "published" && cursor ? (
            <button
              className={styles.secondaryAction}
              disabled={loadingMore}
              onClick={() => void loadMore()}
              type="button"
            >
              {loadingMore ? "正在加载…" : "加载更多"}
            </button>
          ) : null}
        </div>
        {narrow ? (
          <div className={styles.workspaceMobileWrite}>{writeAction}</div>
        ) : null}
      </aside>
      <div
        className={styles.workspaceDetail}
        ref={detailRef}
        onScroll={(event) =>
          detailScroll.current.set(detailKey, event.currentTarget.scrollTop)
        }
        hidden={narrow && route.id === null}
      >
        {route.section === "drafts" && route.id ? (
          <ArticleEditor
            site={authorSite}
            draftId={route.id}
            embedded
            focused={route.focus}
            onSaved={saved}
            onBack={() =>
              navigation.navigate(ARTICLE_COMPOSE_PATH, {
                preserveScroll: true,
              })
            }
            onToggleFocus={() =>
              navigation.replace(
                articleDraftPath(route.id!) + (route.focus ? "" : "/focus"),
              )
            }
            onPublished={(post) => {
              setDrafts((previous) =>
                previous.filter((item) => item.id !== route.id),
              );
              setPublished((previous) => [
                post,
                ...previous.filter((item) => item.id !== post.id),
              ]);
              announceChange(post.id, "published");
              navigation.replace(articlePublishedPath(post.id));
            }}
          />
        ) : route.section === "published" && route.id ? (
          <>
            <header className={styles.workspacePublishedHeader}>
              <button
                className={styles.toolButton}
                aria-label="返回文章列表"
                type="button"
                onClick={() =>
                  navigation.navigate(articlePublishedPath(), {
                    preserveScroll: true,
                  })
                }
              >
                <Icon name="back" />
              </button>
              <span className={styles.publishedBadge}>已发布</span>
              <span className={styles.workspaceSpacer} />
              {reading ? (
                <>
                  <button
                    className={styles.secondaryAction}
                    type="button"
                    disabled={busy}
                    onClick={() => void edit(reading)}
                  >
                    编辑文章
                  </button>
                  {menu(reading, true)}
                </>
              ) : null}
            </header>
            {readError ? (
              <p className={styles.workspaceNotice} role="alert">
                {readError}
              </p>
            ) : reading && isReadArticlePost(reading) ? (
              <ArticlePage post={reading} site={site} showActions={false} />
            ) : (
              <p className={styles.workspaceNotice} role="status">
                正在读取文章…
              </p>
            )}
          </>
        ) : (
          <div className={styles.workspaceWelcome}>
            <h2>在这里写文章</h2>
            <p>继续一份草稿，或者开始写一篇新文章。</p>
            {writeAction}
          </div>
        )}
      </div>
    </section>
  );
}
