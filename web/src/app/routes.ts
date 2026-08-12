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
  {
    id: "profile",
    title: "个人资料",
    eyebrow: "",
    href: "/profile",
    match: (pathname) => pathname === "/profile" || pathname === "/profile/",
  },
  {
    id: "owner-login",
    title: "所有者登录",
    eyebrow: "",
    href: "/owner/login",
    match: (pathname) =>
      pathname === "/owner/login" || pathname === "/owner/login/",
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
  {
    id: "owner-settings",
    title: "站点设置",
    eyebrow: "",
    href: "/owner/settings",
    ownerOnly: true,
    match: (pathname) =>
      pathname === "/owner/settings" || pathname === "/owner/settings/",
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
