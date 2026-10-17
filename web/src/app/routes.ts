export type RouteId =
  | "home"
  | "detail"
  | "article-focus"
  | "search"
  | "archive"
  | "profile"
  | "login"
  | "owner-compose"
  | "article-workspace"
  | "owner-edit"
  | "owner-settings"
  | "not-found";

export interface AppRoute {
  id: RouteId;
  title: string;
  eyebrow: string;
  href: string;
  ownerOnly?: boolean;
}

// Composing is not an owner-only corner of the site, it is what the site is
// for, so it answers at the top level. "compose" is a reserved handle
// (internal/domain/user.go), so this cannot shadow a profile.
export const COMPOSE_PATH = "/compose";
export const COMPOSE_MEDIA_PATH = "/compose/post/media";
export const COMPOSE_DRAFTS_PATH = "/compose/drafts";
// Writing an article is composing, so it lives under the same top-level
// address rather than under /owner: what a page is for decides where it sits,
// and who may open it is the server's answer and not the URL's.
export const ARTICLE_COMPOSE_PATH = "/compose/articles";

/** Where one draft is edited. The id is the server's, so it is escaped. */
export function articleDraftPath(id: string): string {
  return `${ARTICLE_COMPOSE_PATH}/drafts/${encodeURIComponent(id)}`;
}

const ARTICLE_FOCUS_PATTERN = /^\/[a-z][a-z0-9_]{2,29}\/article\/([^/]+)\/?$/;

export function articleFocusPath(username: string, id: string): string {
  return `/${encodeURIComponent(username)}/article/${encodeURIComponent(id)}`;
}

export function articleFocusIdFromPath(pathname: string): string | null {
  if (isArticleComposePath(pathname)) return null;
  const raw = ARTICLE_FOCUS_PATTERN.exec(pathname)?.[1];
  if (raw === undefined) return null;
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

export type ProfileRouteFilter = "all" | "moment" | "article";

const PROFILE_PATTERN =
  /^\/([a-z][a-z0-9_]{2,29})(?:\/(all|posts|articles))?\/?$/;

export function profileFromPath(pathname: string): {
  username: string | null;
  filter: ProfileRouteFilter;
} | null {
  const match = PROFILE_PATTERN.exec(pathname);
  if (!match || (match[1] === "profile" && match[2])) return null;
  return {
    // The existing handle-free entry redirects to the signed-in account.
    username: match[1] === "profile" ? null : match[1]!,
    filter:
      match[2] === "articles"
        ? "article"
        : match[2] === "posts"
          ? "moment"
          : "all",
  };
}

export function profileFeedPath(
  username: string,
  filter: ProfileRouteFilter,
): string {
  const segment =
    filter === "article" ? "articles" : filter === "moment" ? "posts" : "all";
  return `/${encodeURIComponent(username)}/${segment}`;
}

function matchesPath(pathname: string, path: string): boolean {
  return pathname === path || pathname === `${path}/`;
}

export function isComposePath(pathname: string): boolean {
  return (
    matchesPath(pathname, COMPOSE_PATH) ||
    matchesPath(pathname, COMPOSE_MEDIA_PATH) ||
    isComposeDraftsPath(pathname)
  );
}

const ARTICLE_DRAFT_PATTERN =
  /^\/compose\/(?:article\/|articles\/drafts\/)([^/]+)(?:\/focus)?\/?$/;

export function isArticleComposePath(pathname: string): boolean {
  return (
    matchesPath(pathname, ARTICLE_COMPOSE_PATH) ||
    matchesPath(pathname, "/compose/article") ||
    /^\/compose\/articles\/(?:drafts|published)(?:\/[^/]+(?:\/focus)?)?\/?$/.test(
      pathname,
    ) ||
    ARTICLE_DRAFT_PATTERN.test(pathname)
  );
}

/**
 * The draft an address names, and null for the address that starts a new one.
 * A segment that is not a legal escape is handed on as it arrived: the server
 * decides what is and is not one of its ids.
 */
export function articleDraftIdFromPath(pathname: string): string | null {
  const raw = ARTICLE_DRAFT_PATTERN.exec(pathname)?.[1];
  if (raw === undefined) return null;
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

export function isComposeDraftsPath(pathname: string): boolean {
  return (
    matchesPath(pathname, COMPOSE_DRAFTS_PATH) ||
    matchesPath(pathname, "/owner/compose/drafts")
  );
}

/**
 * Where a link or a history entry written before composing moved out from
 * under /owner should go. Entries already in somebody's history keep working
 * rather than turning into a not-found page.
 */
export function movedComposePath(pathname: string): string | null {
  if (matchesPath(pathname, "/owner/compose")) return COMPOSE_PATH;
  if (matchesPath(pathname, "/owner/compose/drafts"))
    return COMPOSE_DRAFTS_PATH;
  return null;
}

const routes: Array<AppRoute & { match: (pathname: string) => boolean }> = [
  {
    id: "home",
    title: "时间流",
    eyebrow: "",
    href: "/",
    match: (pathname) => pathname === "/",
  },
  {
    id: "detail",
    title: "Moment",
    eyebrow: "",
    href: "/",
    match: (pathname) => /^\/m\/[^/]+\/?$/.test(pathname),
  },
  {
    id: "article-focus",
    title: "文章",
    eyebrow: "",
    href: "/",
    match: (pathname) => articleFocusIdFromPath(pathname) !== null,
  },
  {
    id: "search",
    title: "搜索",
    eyebrow: "",
    href: "/search",
    match: (pathname) => pathname === "/search" || pathname === "/search/",
  },
  {
    id: "archive",
    title: "归档",
    eyebrow: "",
    href: "/archive",
    match: (pathname) => pathname === "/archive" || pathname === "/archive/",
  },
  // Signing in is not an owner action. Which account the credential belongs to
  // decides what that account may do, and that decision is the server's; a page
  // that called itself the owner's told everybody else they were in the wrong
  // place before it knew who they were. "login" is a reserved handle
  // (internal/domain/user.go), so this cannot shadow a profile.
  {
    id: "login",
    title: "登录",
    eyebrow: "",
    href: "/login",
    match: (pathname) => pathname === "/login" || pathname === "/login/",
  },
  // Ahead of the compose route so the reading of "/compose/article" is never
  // decided by how loosely that one happens to match today.
  {
    id: "article-workspace",
    title: "文章",
    eyebrow: "",
    href: ARTICLE_COMPOSE_PATH,
    match: isArticleComposePath,
  },
  {
    id: "owner-compose",
    title: "发布 Moment",
    eyebrow: "",
    href: COMPOSE_PATH,
    ownerOnly: true,
    // The drafts list is a face of the same surface, so it shares the route
    // and carries its own address: reloading it comes back to the list
    // rather than to a composer with the list shut. The old /owner paths
    // still match, so an entry in somebody's history renders while the
    // redirect corrects the address.
    match: (pathname) =>
      isComposePath(pathname) || movedComposePath(pathname) !== null,
  },
  {
    id: "owner-edit",
    title: "编辑 Moment",
    eyebrow: "",
    href: "/",
    ownerOnly: true,
    match: (pathname) => /^\/owner\/m\/[^/]+\/edit\/?$/.test(pathname),
  },
  // Not owner-only, despite the path. Login methods and active sessions belong
  // to whoever is signed in, and this is the page that holds them; the sections
  // that act on the site rather than on the account hide themselves instead.
  {
    id: "owner-settings",
    title: "设置",
    eyebrow: "",
    href: "/owner/settings",
    match: (pathname) =>
      pathname === "/owner/settings" || pathname === "/owner/settings/",
  },
  // Handles live at the site root now (`/username`, not `/@username`), so this
  // pattern would swallow every other single-segment page. `resolveRoute` takes
  // the first match, so the profile route has to stay last in this array.
  {
    id: "profile",
    title: "个人资料",
    eyebrow: "",
    href: "/profile",
    match: (pathname) => profileFromPath(pathname) !== null,
  },
];

const notFound: AppRoute = {
  id: "not-found",
  title: "没有这个页面",
  eyebrow: "",
  href: "/",
};

export function resolveRoute(pathname: string): AppRoute {
  const route = routes.find((candidate) => candidate.match(pathname));
  if (!route) return notFound;

  return {
    id: route.id,
    title: route.title,
    eyebrow: route.eyebrow,
    href:
      route.id === "article-focus"
        ? `/m/${encodeURIComponent(articleFocusIdFromPath(pathname) ?? "")}`
        : route.href,
    ownerOnly: route.ownerOnly,
  };
}

export function articlePublishedPath(id?: string): string {
  return `${ARTICLE_COMPOSE_PATH}/published${id ? `/${encodeURIComponent(id)}` : ""}`;
}
export function articleWorkspaceFromPath(pathname: string): {
  section: "drafts" | "published";
  id: string | null;
  focus: boolean;
} {
  const published = /^\/compose\/articles\/published(?:\/([^/]+))?\/?$/.exec(
    pathname,
  );
  if (published) {
    let id: string | null;
    try {
      id = published[1] ? decodeURIComponent(published[1]) : null;
    } catch {
      id = published[1] ?? null;
    }
    return { section: "published", id, focus: false };
  }
  return {
    section: "drafts",
    id: articleDraftIdFromPath(pathname),
    focus: pathname.replace(/\/$/, "").endsWith("/focus"),
  };
}
export function movedArticleComposePath(pathname: string): string | null {
  if (matchesPath(pathname, "/compose/article")) return ARTICLE_COMPOSE_PATH;
  if (/^\/compose\/article\/[^/]+\/?$/.test(pathname)) {
    const id = articleDraftIdFromPath(pathname);
    return id ? articleDraftPath(id) : ARTICLE_COMPOSE_PATH;
  }
  return null;
}
