export type RouteId =
  | "home"
  | "detail"
  | "search"
  | "archive"
  | "profile"
  | "login"
  | "owner-compose"
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
export const COMPOSE_DRAFTS_PATH = "/compose/drafts";

function matchesPath(pathname: string, path: string): boolean {
  return pathname === path || pathname === `${path}/`;
}

export function isComposePath(pathname: string): boolean {
  return matchesPath(pathname, COMPOSE_PATH) || isComposeDraftsPath(pathname);
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
    match: (pathname) =>
      pathname === "/profile" ||
      pathname === "/profile/" ||
      /^\/[a-z][a-z0-9_]{2,29}\/?$/.test(pathname),
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
    href: route.href,
    ownerOnly: route.ownerOnly,
  };
}
