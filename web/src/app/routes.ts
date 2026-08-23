export type RouteId =
  | "home"
  | "detail"
  | "search"
  | "archive"
  | "profile"
  | "owner-login"
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
  // place before it knew who they were. `/owner/login` still answers so that a
  // link somebody kept does not dead-end. "login" is a reserved handle
  // (internal/domain/user.go), so this cannot shadow a profile.
  {
    id: "owner-login",
    title: "登录",
    eyebrow: "",
    href: "/login",
    match: (pathname) =>
      pathname === "/login" ||
      pathname === "/login/" ||
      pathname === "/owner/login" ||
      pathname === "/owner/login/",
  },
  {
    id: "owner-compose",
    title: "发布 Moment",
    eyebrow: "",
    href: "/owner/compose",
    ownerOnly: true,
    match: (pathname) =>
      pathname === "/owner/compose" || pathname === "/owner/compose/",
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
