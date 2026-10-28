export type RouteId =
  | "home"
  | "detail"
  | "comment-detail"
  | "article-focus"
  | "search"
  | "archive"
  | "profile-edit"
  | "profile"
  | "login"
  | "register"
  | "compose"
  | "article-workspace"
  | "edit"
  | "settings"
  | "not-found";

export interface AppRoute {
  id: RouteId;
  title: string;
  eyebrow: string;
  href: string;
  activeAccountOnly?: boolean;
  requiresSession?: boolean;
}

export const PROFILE_EDIT_PATH = "/settings/profile";

export function commentDetailPath(
  postId: string,
  commentId: string,
  wholeThread = false,
): string {
  return (
    "/m/" +
    encodeURIComponent(postId) +
    "/comments/" +
    encodeURIComponent(commentId) +
    (wholeThread ? "?view=thread" : "")
  );
}

export function commentDetailFromPath(
  pathname: string,
): { postId: string; commentId: string } | null {
  const match = /^\/m\/([^/]+)\/comments\/([^/]+)\/?$/.exec(pathname);
  if (!match) return null;
  try {
    return {
      postId: decodeURIComponent(match[1]),
      commentId: decodeURIComponent(match[2]),
    };
  } catch {
    return null;
  }
}
export function isProfileEditPath(pathname: string): boolean {
  return pathname === PROFILE_EDIT_PATH || pathname === `${PROFILE_EDIT_PATH}/`;
}

export const COMPOSE_PATH = "/compose";
export const COMPOSE_MEDIA_PATH = "/compose/post/media";
export const COMPOSE_DRAFTS_PATH = "/compose/drafts";
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
  return matchesPath(pathname, COMPOSE_DRAFTS_PATH);
}

// Every registered route must explicitly classify its session requirement.
const routes: Array<
  AppRoute & { requiresSession: boolean; match: (pathname: string) => boolean }
> = [
  {
    id: "comment-detail",
    requiresSession: false,
    title: "Moment",
    eyebrow: "",
    href: "/",
    match: (pathname) => commentDetailFromPath(pathname) !== null,
  },
  {
    id: "home",
    requiresSession: false,
    title: "时间流",
    eyebrow: "",
    href: "/",
    match: (pathname) => pathname === "/",
  },
  {
    id: "detail",
    requiresSession: false,
    title: "Moment",
    eyebrow: "",
    href: "/",
    match: (pathname) => /^\/m\/[^/]+\/?$/.test(pathname),
  },
  {
    id: "article-focus",
    requiresSession: false,
    title: "文章",
    eyebrow: "",
    href: "/",
    match: (pathname) => articleFocusIdFromPath(pathname) !== null,
  },
  {
    id: "search",
    requiresSession: false,
    title: "搜索",
    eyebrow: "",
    href: "/search",
    match: (pathname) => pathname === "/search" || pathname === "/search/",
  },
  {
    id: "archive",
    requiresSession: false,
    title: "归档",
    eyebrow: "",
    href: "/archive",
    match: (pathname) => pathname === "/archive" || pathname === "/archive/",
  },
  {
    id: "login",
    requiresSession: false,
    title: "登录",
    eyebrow: "",
    href: "/login",
    match: (pathname) => pathname === "/login" || pathname === "/login/",
  },
  {
    id: "register",
    requiresSession: false,
    title: "注册",
    eyebrow: "",
    href: "/register",
    match: (pathname) => pathname === "/register" || pathname === "/register/",
  },
  // Ahead of the compose route so the reading of "/compose/article" is never
  // decided by how loosely that one happens to match today.
  {
    id: "article-workspace",
    requiresSession: true,
    title: "文章",
    eyebrow: "",
    href: ARTICLE_COMPOSE_PATH,
    match: isArticleComposePath,
  },
  {
    id: "compose",
    requiresSession: true,
    title: "发布 Moment",
    eyebrow: "",
    href: COMPOSE_PATH,
    activeAccountOnly: true,
    match: (pathname) => isComposePath(pathname),
  },
  {
    id: "edit",
    requiresSession: true,
    title: "编辑 Moment",
    eyebrow: "",
    href: "/",
    activeAccountOnly: true,
    match: (pathname) => /^\/m\/[^/]+\/edit\/?$/.test(pathname),
  },
  {
    id: "profile-edit",
    requiresSession: true,
    title: "编辑资料",
    eyebrow: "",
    href: PROFILE_EDIT_PATH,
    match: isProfileEditPath,
  },
  {
    id: "settings",
    requiresSession: true,
    title: "设置",
    eyebrow: "",
    href: "/settings/account",
    match: (pathname) =>
      /^\/settings\/(account|site|members|backups)\/?$/.test(pathname),
  },
  // Handles live at the site root now (`/username`, not `/@username`), so this
  // pattern would swallow every other single-segment page. `resolveRoute` takes
  // the first match, so the profile route has to stay last in this array.
  {
    id: "profile",
    requiresSession: false,
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
        : route.id === "comment-detail"
          ? "/m/" +
            encodeURIComponent(commentDetailFromPath(pathname)?.postId ?? "")
          : route.href,
    activeAccountOnly: route.activeAccountOnly,
    requiresSession:
      route.requiresSession ||
      (route.id === "profile" && profileFromPath(pathname)?.username === null),
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
