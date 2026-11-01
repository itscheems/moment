import type { IconName } from "../../components/icons";

export type SettingsCategory =
  "account" | "security" | "display" | "site" | "members" | "backups";

export interface SettingsItem {
  id: string;
  category: SettingsCategory;
  title: string;
  description: string;
  icon: IconName;
}

export const settingsCategories: Array<{
  id: SettingsCategory;
  title: string;
  description: string;
  admin?: boolean;
}> = [
  {
    id: "account",
    title: "你的账号",
    description: "管理个人资料和密码，导出或导入你的个人数据。",
  },
  {
    id: "security",
    title: "安全和账号访问",
    description: "管理登录方式、API 令牌和已登录的设备。",
  },
  {
    id: "display",
    title: "显示与语言",
    description: "选择 Moment 的显示外观和界面语言。",
  },
  {
    id: "site",
    title: "站点设置",
    description: "管理站点的评论设置。",
    admin: true,
  },
  {
    id: "members",
    title: "成员管理",
    description: "审核新成员，管理账号状态、权限和加入方式。",
    admin: true,
  },
  {
    id: "backups",
    title: "备份与恢复",
    description: "导出完整站点，或检查并恢复已有备份。",
    admin: true,
  },
];

export const settingsItems: SettingsItem[] = [
  {
    id: "profile",
    category: "account",
    title: "个人资料",
    description: "编辑名字、用户名、简介、头像和封面。",
    icon: "account-settings",
  },
  {
    id: "password",
    category: "account",
    title: "修改密码",
    description: "设置或更改你的登录密码。",
    icon: "key",
  },
  {
    id: "export",
    category: "account",
    title: "导出个人数据",
    description: "下载你的内容、草稿、评论和媒体。",
    icon: "download",
  },
  {
    id: "import",
    category: "account",
    title: "导入个人数据",
    description: "将个人数据添加到当前账号，保留已有内容。",
    icon: "archive",
  },
  {
    id: "identities",
    category: "security",
    title: "登录方式",
    description: "查看、关联或解绑第三方登录账号。",
    icon: "link",
  },
  {
    id: "tokens",
    category: "security",
    title: "API 令牌",
    description: "管理用于客户端和命令行的访问令牌。",
    icon: "key",
  },
  {
    id: "sessions",
    category: "security",
    title: "活跃会话",
    description: "查看已登录的设备，或退出不再使用的会话。",
    icon: "devices",
  },
  {
    id: "appearance",
    category: "display",
    title: "显示",
    description: "选择背景和强调色，立即在当前浏览器中应用。",
    icon: "theme",
  },
  {
    id: "language",
    category: "display",
    title: "语言",
    description: "选择你希望使用的界面语言。",
    icon: "globe",
  },
  {
    id: "comments",
    category: "site",
    title: "评论",
    description: "决定是否允许新的评论。",
    icon: "reply",
  },
  {
    id: "roster",
    category: "members",
    title: "成员列表",
    description: "查看成员和待审核申请，管理账号状态与权限。",
    icon: "account-settings",
  },
  {
    id: "registration",
    category: "members",
    title: "加入方式",
    description: "设置新账号如何加入站点。",
    icon: "plus",
  },
  {
    id: "export",
    category: "backups",
    title: "导出整站备份",
    description: "保存站点资料、内容、评论和媒体。",
    icon: "download",
  },
  {
    id: "restore",
    category: "backups",
    title: "从备份恢复",
    description: "检查备份，核对合并或替换的影响后再恢复。",
    icon: "archive",
  },
];

export function settingsItemPath(item: Pick<SettingsItem, "category" | "id">) {
  return `/settings/${item.category}/${item.id}`;
}

export function matchSettingsPath(pathname: string): boolean {
  const path = pathname.replace(/\/$/, "");
  return (
    path === "/settings" ||
    settingsCategories.some(
      (category) => path === `/settings/${category.id}`,
    ) ||
    settingsItems.some((item) => settingsItemPath(item) === path)
  );
}

export function settingsLocation(pathname: string) {
  const path = pathname.replace(/\/$/, "");
  const item = settingsItems.find((item) => settingsItemPath(item) === path);
  const category =
    settingsCategories.find(
      (category) => category.id === (item?.category ?? path.split("/")[2]),
    ) ?? settingsCategories[0]!;
  return { root: path === "/settings", category, item };
}
