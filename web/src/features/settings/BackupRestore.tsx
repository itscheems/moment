import { getLocale } from "../../i18n";
import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { ensureCredentialConfirmation } from "../../api/credentialConfirmation";
import { MapCard } from "../maps/MapCard";
import {
  backupsApi,
  type ImportStatus,
  type MergeAction,
  type MergeEntityKind,
  type MergeInspection,
  type MergeReviewEntity,
  type MergeReviewItem,
  type MergeReviewMedia,
  type MergeSelection,
} from "../../api/backups";
import { Dialog } from "../../components/Dialog";
import {
  type ChangeEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import { PublicApiError } from "../../api/public";
import { Icon } from "../../components/icons";
import { ActionTooltip } from "../../components/TimelineInteractions";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { accountCacheScope } from "../auth/accountScope";
import { useSession } from "../auth/SessionProvider";
import { providerLabel } from "../auth/providers";
import styles from "./Settings.module.css";

type TransferState = "idle" | "running" | "failed" | "complete";

const activePhases = new Set<ImportStatus["phase"]>([
  "claimed",
  "backing_up",
  "finalizing",
  "switching",
  "verifying",
  "rolling_back",
]);

function rememberedMergeToken(key: string | null): string | null {
  if (!key) return null;
  try {
    const value = JSON.parse(window.sessionStorage.getItem(key) ?? "null") as {
      stagingToken?: unknown;
      expiresAt?: unknown;
    } | null;
    if (
      !value ||
      typeof value.stagingToken !== "string" ||
      value.stagingToken.length === 0 ||
      value.stagingToken.length > 256 ||
      typeof value.expiresAt !== "string" ||
      !Number.isFinite(Date.parse(value.expiresAt)) ||
      Date.parse(value.expiresAt) <= Date.now()
    ) {
      window.sessionStorage.removeItem(key);
      return null;
    }
    return value.stagingToken;
  } catch {
    window.sessionStorage.removeItem(key);
    return null;
  }
}

function rememberMergeStaging(key: string | null, inspection: MergeInspection) {
  if (!key) return;
  window.sessionStorage.setItem(
    key,
    JSON.stringify({
      stagingToken: inspection.stagingToken,
      expiresAt: inspection.expiresAt,
    }),
  );
}

function forgetMergeStaging(key: string | null, expectedToken: string) {
  if (key && rememberedMergeToken(key) === expectedToken) {
    window.sessionStorage.removeItem(key);
  }
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes / 1024;
  let unit = units[0];
  for (let index = 1; index < units.length && value >= 1024; index += 1) {
    value /= 1024;
    unit = units[index];
  }
  return `${new Intl.NumberFormat(getLocale(), { maximumFractionDigits: 1 }).format(value)} ${unit}`;
}

function mergeKindLabel(kind: MergeEntityKind): string {
  switch (kind) {
    case "entry":
      return "Moment";
    case "comment":
      return t("评论");
    case "media":
      return t("媒体");
    case "user":
      return t("账户");
    case "identity":
      return t("登录方式");
  }
}

function mergeBlockedMessage(reason: string | null): string {
  switch (reason) {
    case "USERNAME_TAKEN":
      return t(
        "备份里有一个账户和当前站点的另一个账户用了同一个用户名，合并无法决定该给谁改名；仍可在核对实际内容后完全替换。",
      );
    default:
      return t("这份备份不能安全合并；仍可在核对实际内容后完全替换。");
  }
}

/**
 * Why an account row carries no controls.
 *
 * Stated in full wherever accounts appear, because "not selectable" on its own
 * reads as something missing. It is a rule, and the rule has a reason: an
 * account is a person, and the choices that make sense for content do not make
 * sense for one.
 */
const ACCOUNT_DECISION_NOTE = () =>
  t("账户和登录方式只能看，不能选。备份里的一个账户就是一个人：") +
  t("“两者都保留”会把同一个人拆成两个，“使用备份版本”则等于让一份旧备份") +
  t("在正在运行的站点上给人改名、或重新放行一个已经停用的账户。") +
  t(
    "它们按固定规则处理——同一个账户以当前站点为准，备份里独有的账户会被加进来——",
  ) +
  t("列在这里是为了让你在决定之前，看见一次恢复会顺带带来什么。");

function accountStatusLabel(status: string | undefined): string {
  switch (status) {
    case "pending":
      return t("待批准");
    case "active":
      return t("已启用");
    case "suspended":
      return t("已停用");
    default:
      return status ?? t("未知状态");
  }
}

function accountRoleLabel(role: string | undefined): string {
  return role === "admin" ? t("管理员") : t("成员");
}

/**
 * What the fixed rule will do with this row, said plainly.
 *
 * A reviewer cannot change any of it, so the only useful thing to show beside
 * an account is the outcome it is already headed for.
 */
function accountOutcomeLabel(
  item: MergeReviewItem,
  importMode: "merge" | "restore",
): string {
  if (item.classification === "incoming_only") return t("备份独有 · 会被加入");
  if (item.classification === "local_only") {
    return importMode === "restore"
      ? t("当前独有 · 将移除")
      : t("当前独有 · 会保留");
  }
  return t("两侧都有 · 以当前站点为准");
}

function mergeValue(value: unknown): string {
  if (value === null) return t("未设置");
  if (typeof value === "string") return value || t("空值");
  if (typeof value === "boolean") return value ? t("开启") : t("关闭");
  return JSON.stringify(value);
}

function mergeSiteFieldLabel(field: string): string {
  const labels: Record<string, string> = {
    username: t("用户名"),
    display_name: t("显示名称"),
    title: t("站点标题"),
    bio: t("简介"),
    timezone: t("时区"),
    avatar_media_id: t("头像"),
    cover_media_id: t("主页封面"),
    pinned_post_id: t("置顶 Moment"),
    comments_enabled: t("评论开关"),
  };
  return labels[field] ?? field;
}

function mergeChangedFieldLabel(field: string): string {
  const labels: Record<string, string> = {
    ui_locale: t("显示语言"),
    kind: t("类型"),
    content_md: t("正文"),
    created_at: t("创建时间"),
    updated_at: t("更新时间"),
    published_at: t("发布时间"),
    quoted_entry_id: t("引用的 Moment"),
    quoted_comment_id: t("引用的评论"),
    media: t("媒体与图片描述"),
    location: t("位置"),
    entry_id: t("所属 Moment"),
    parent_id: t("上级评论"),

    author_name: t("作者名称"),
    sha256: t("文件内容"),
    original_name: t("原文件名"),
    mime_type: t("文件类型"),
    byte_size: t("文件大小"),
    width: t("宽度"),
    height: t("高度"),
  };
  return labels[field] ?? field;
}

function ReviewMediaPreview({
  inspection,
  media,
}: {
  inspection: MergeInspection;
  media: MergeReviewMedia;
}) {
  const { t } = useTranslation();

  const { api, session } = useSession();
  const [url, setURL] = useState<string | null>(null);
  useEffect(() => {
    if (!session) return;
    const controller = new AbortController();
    let objectURL: string | null = null;
    void backupsApi
      .loadImportReviewMedia(
        session.csrfToken,
        inspection.stagingToken,
        inspection.plan.plan_id,
        media.source,
        media.id,
        controller.signal,
      )
      .then((blob) => {
        if (controller.signal.aborted) return;
        objectURL = URL.createObjectURL(blob);
        setURL(objectURL);
      })
      .catch(() => undefined);
    return () => {
      controller.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [
    api,
    inspection.plan.plan_id,
    inspection.stagingToken,
    media.id,
    media.source,
    session,
  ]);
  return url ? (
    <img
      alt={media.alt_text ?? media.original_name}
      className={styles.reviewMedia}
      height={media.height}
      src={url}
      width={media.width}
    />
  ) : (
    <div className={styles.reviewMediaPlaceholder}>{t("正在载入图片…")}</div>
  );
}

function ReviewEntityCard({
  entity,
  inspection,
  label,
  tone,
}: {
  entity: MergeReviewEntity;
  inspection: MergeInspection;
  label: string;
  tone?: "danger" | "incoming";
}) {
  const { t } = useTranslation();

  return (
    <article className={styles.reviewEntity} data-tone={tone}>
      <header>
        <span>{label}</span>
        <time dateTime={entity.published_at ?? entity.created_at}>
          {new Intl.DateTimeFormat(getLocale(), {
            dateStyle: "medium",
            timeStyle: "short",
          }).format(new Date(entity.published_at ?? entity.created_at))}
        </time>
      </header>
      {entity.username !== undefined ? (
        // An account row. `content_md` is on the wire but holds nothing for a
        // person, so rendering it would print "（无文字内容）" over one.
        <dl className={styles.reviewAccount}>
          <div>
            <dt>{t("用户名")}</dt>
            <dd>@{entity.username}</dd>
          </div>
          <div>
            <dt>{t("显示名称")}</dt>
            <dd>{entity.display_name || t("未命名")}</dd>
          </div>
          <div>
            <dt>{t("身份")}</dt>
            <dd>{accountRoleLabel(entity.role)}</dd>
          </div>
          <div>
            <dt>{t("状态")}</dt>
            <dd>{accountStatusLabel(entity.status)}</dd>
          </div>
          <div>
            <dt>{t("显示语言")}</dt>
            <dd>
              {entity.ui_locale === "en"
                ? "English"
                : entity.ui_locale === "zh-CN"
                  ? "简体中文"
                  : t("未设置")}
            </dd>
          </div>
        </dl>
      ) : entity.provider !== undefined ? (
        <dl className={styles.reviewAccount}>
          <div>
            <dt>{t("登录方式")}</dt>
            <dd>{providerLabel(entity.provider)}</dd>
          </div>
          <div>
            <dt>{t("邮箱")}</dt>
            <dd>{entity.email || t("该账号没有公开邮箱")}</dd>
          </div>
        </dl>
      ) : (
        <>
          {entity.author_name ? (
            <p className={styles.reviewAuthor}>{entity.author_name}</p>
          ) : null}
          <p className={styles.reviewContent}>
            {entity.content_md || t("（无文字内容）")}
          </p>
        </>
      )}
      {entity.map_card ? <MapCard card={entity.map_card} deferMap /> : null}
      {entity.location ? (
        <p
          className={styles.reviewLocation}
          title={entity.location.displayName}
        >
          {entity.location.name}
          {entity.location.district ? ` · ${entity.location.district}` : ""}
        </p>
      ) : null}
      {entity.media.length ? (
        <div className={styles.reviewMediaGrid}>
          {entity.media.map((media) => (
            <ReviewMediaPreview
              inspection={inspection}
              key={`${media.source}:${media.id}:${media.position}`}
              media={media}
            />
          ))}
        </div>
      ) : null}
    </article>
  );
}

function ReviewItem({
  inspection,
  item,
  restoreMode,
}: {
  inspection: MergeInspection;
  item: MergeReviewItem;
  restoreMode: boolean;
}) {
  const { t } = useTranslation();

  if (item.classification === "conflict") {
    return (
      <div className={styles.reviewComparison}>
        <ReviewEntityCard
          entity={item.local!}
          inspection={inspection}
          label={t("当前站点")}
        />
        <ReviewEntityCard
          entity={item.incoming!}
          inspection={inspection}
          label={t("备份文件")}
          tone="incoming"
        />
      </div>
    );
  }
  const entity = item.local ?? item.incoming;
  if (!entity) return null;
  const localOnly = item.classification === "local_only";
  return (
    <ReviewEntityCard
      entity={entity}
      inspection={inspection}
      label={
        localOnly
          ? restoreMode
            ? t("当前独有 · 将移除")
            : t("当前独有 · 会保留")
          : t("备份新增")
      }
      tone={
        localOnly && restoreMode
          ? "danger"
          : item.classification === "incoming_only"
            ? "incoming"
            : undefined
      }
    />
  );
}

function DecisionChoice({
  checked,
  children,
  label,
  onChoose,
}: {
  checked: boolean;
  children: ReactNode;
  label: string;
  onChoose: () => void;
}) {
  return (
    <div
      aria-checked={checked}
      aria-label={label}
      className={styles.mergeDecisionChoice}
      onClick={onChoose}
      onKeyDown={(event) => {
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
        onChoose();
      }}
      role="radio"
      tabIndex={0}
    >
      {children}
      <span aria-hidden="true" className={styles.mergeDecisionMark} />
    </div>
  );
}

function importError(error: unknown, operation: "plan" | "restore"): string {
  if (!(error instanceof PublicApiError)) {
    return operation === "plan"
      ? t("预检没有完成。当前站点没有改变，请重新选择或重试。")
      : t("服务器明确拒绝了恢复，当前站点没有被确认替换。请重新预检。");
  }
  switch (error.code) {
    case "REAUTHENTICATION_REQUIRED":
      return t("身份验证未完成，操作未执行。");
    case "EXPORT_INVALID":
    case "EXPORT_CHECKSUM_MISMATCH":
    case "EXPORT_VERSION_UNSUPPORTED":
    case "IMPORT_STAGING_INVALID":
      return t(
        "这个文件不是可恢复的 Moment 备份，或其校验已经失效。请换一个原始导出文件。",
      );
    case "IMPORT_DISK_SPACE_INSUFFICIENT":
      return t("可用磁盘空间不足。释放空间后重新预检，恢复尚未开始。");
    case "IMPORT_IN_PROGRESS":
    case "MAINTENANCE_MODE":
      return t("另一个恢复或维护操作正在进行。请先查询当前状态，稍后再预检。");
    case "REQUEST_BODY_TOO_LARGE":
      return t("备份文件超过此实例允许的上传大小。请检查实例限制或使用 CLI。");
    case "UNAUTHORIZED":
    case "CSRF_INVALID":
      return t("登录会话已经失效。备份文件仍只留在浏览器内，请重新登录。");
    case "CANCELLED":
      return t("预检上传已取消，当前站点没有改变。");
    case "API_RESPONSE_INVALID":
      return t("服务器响应无法确认。不要再次恢复，请检查恢复状态。");
    default:
      return operation === "plan"
        ? t("预检没有完成。当前站点没有改变，可以安全重试。")
        : t("服务器明确返回恢复失败。请重新预检后再决定。");
  }
}

function statusCopy(
  status: ImportStatus,
  merge = false,
): {
  eyebrow: string;
  title: string;
  detail: string;
  tone: "working" | "success" | "warning" | "danger";
} {
  switch (status.phase) {
    case "claimed":
      return {
        eyebrow: t("准备恢复"),
        title: t("服务器已接管恢复"),
        detail: t("正在重新校验预检绑定，当前站点尚未切换。"),
        tone: "working",
      };
    case "backing_up":
      return {
        eyebrow: t("自动备份"),
        title: t("正在备份当前站点"),
        detail: t("只有自动备份验证完成，恢复才会继续。"),
        tone: "working",
      };
    case "finalizing":
      return {
        eyebrow: t("准备数据"),
        title: t("正在准备恢复数据"),
        detail: t("预检内容已经重新验证，当前站点仍在使用。"),
        tone: "working",
      };
    case "switching":
      return {
        eyebrow: t("切换数据"),
        title: t("正在切换活动数据"),
        detail: t("短暂维护期间不要提交其他写操作，本页会查询服务器状态。"),
        tone: "working",
      };
    case "verifying":
      return {
        eyebrow: t("验证数据"),
        title: t("正在验证恢复后的站点"),
        detail: t("只有新数据通过就绪检查，本页才会报告成功。"),
        tone: "working",
      };
    case "rolling_back":
      return {
        eyebrow: t("自动回退"),
        title: t("正在尝试恢复原站点"),
        detail: t("新数据没有通过验证，服务器正在核对并恢复先前数据。"),
        tone: "warning",
      };
    case "succeeded":
      return {
        eyebrow: merge ? t("合并完成") : t("恢复完成"),
        title: merge
          ? t("备份已合并并通过就绪检查")
          : t("备份已恢复并通过就绪检查"),
        detail: merge
          ? t("合并前自动备份已验证。重新加载页面即可查看合并后的内容。")
          : t("恢复前自动备份已验证。重新加载页面即可查看恢复后的内容。"),
        tone: "success",
      };
    case "failed":
      return {
        eyebrow: t("恢复停止"),
        title: t("恢复在切换前停止"),
        detail: t("旧数据仍是活动数据。修复错误后，从预检重新开始。"),
        tone: "warning",
      };
    case "rolled_back":
      return {
        eyebrow: t("已恢复原数据"),
        title: t("已自动恢复原站点"),
        detail: t("新数据未通过就绪检查，原数据已经重新启用并验证。"),
        tone: "warning",
      };
    case "needs_attention":
      return {
        eyebrow: t("需要人工处理"),
        title: t("恢复状态需要人工处理"),
        detail: t(
          "服务器无法证明旧数据或新数据处于安全终态。停止写入并运行 moment doctor --deep --json。",
        ),
        tone: "danger",
      };
  }
}

export function BackupRestore({
  settingsBlocked,
  instanceId,
  panel,
}: {
  settingsBlocked: boolean;
  instanceId: string | null;
  panel?: "export" | "restore";
}) {
  const { t } = useTranslation();

  const { api, account, session, expireIfInvalidSession } = useSession();
  const scope = accountCacheScope(instanceId, account?.userId ?? null);
  const stagingKey = scope ? `moment.backups.merge-staging.v2:${scope}` : null;
  const mountedRef = useRef(true);
  const fileRevisionRef = useRef(0);
  const mergeControllerRef = useRef<AbortController | null>(null);
  const restoreControllerRef = useRef<AbortController | null>(null);
  const pollControllerRef = useRef<AbortController | null>(null);
  const pollTimerRef = useRef<number | null>(null);
  const restoreAttemptedRef = useRef(false);
  const restoreSentRef = useRef(false);
  const unassociatedPollsRef = useRef(0);
  const foreignOperationRef = useRef<string | null>(null);
  const lastObservedOperationRef = useRef<string | null>(null);
  const restoreBaselineRef = useRef<string | null>(null);
  const mergeTokenRef = useRef<string | null>(rememberedMergeToken(stagingKey));
  const apiRef = useRef(api);
  const csrfTokenRef = useRef<string | null>(session?.csrfToken ?? null);
  const exportGuardRef = useRef(false);
  const exportGuardTimerRef = useRef<number | null>(null);
  const pickerRef = useRef<HTMLButtonElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const focusStatusRef = useRef(false);
  const [file, setFile] = useState<File | null>(null);
  const [mergeInspection, setMergeInspection] =
    useState<MergeInspection | null>(null);
  const [mergeSelection, setMergeSelection] = useState<MergeSelection | null>(
    null,
  );
  const [mergeState, setMergeState] = useState<TransferState>("idle");
  const [mergeProgress, setMergeProgress] = useState<number | null>(null);
  const [mergeError, setMergeError] = useState<string | null>(null);
  const [exportState, setExportState] = useState<TransferState>("idle");
  const [exportNotice, setExportNotice] = useState<string | null>(null);
  const [restoreState, setRestoreState] = useState<TransferState>("idle");
  const [restoreError, setRestoreError] = useState<string | null>(null);
  const [status, setStatus] = useState<ImportStatus | null>(null);
  const [statusChecking, setStatusChecking] = useState(true);
  const [statusInitialized, setStatusInitialized] = useState(false);
  const [statusDisconnected, setStatusDisconnected] = useState(false);
  const [manualStatusRequired, setManualStatusRequired] = useState(false);
  const [siteRefreshRequired, setSiteRefreshRequired] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [mergeConfirmOpen, setMergeConfirmOpen] = useState(false);
  const [importMode, setImportMode] = useState<"restore" | "merge">("restore");

  apiRef.current = api;
  csrfTokenRef.current = session?.csrfToken ?? null;

  useDocumentScrollLock(confirmOpen || mergeConfirmOpen);

  const restoreBusy =
    !statusInitialized ||
    restoreState === "running" ||
    (status !== null && activePhases.has(status.phase)) ||
    status?.phase === "needs_attention" ||
    manualStatusRequired ||
    statusDisconnected ||
    siteRefreshRequired;

  const exportBlocked =
    restoreState === "running" ||
    (status !== null && activePhases.has(status.phase)) ||
    status?.phase === "needs_attention" ||
    manualStatusRequired ||
    siteRefreshRequired;

  const clearPoll = useCallback(() => {
    pollControllerRef.current?.abort();
    pollControllerRef.current = null;
    if (pollTimerRef.current !== null)
      window.clearTimeout(pollTimerRef.current);
    pollTimerRef.current = null;
  }, []);

  const queryStatus = useCallback(
    async (
      revision: number,
      expectedOperation: string | null = null,
      ignoredBaseline: string | null = null,
    ) => {
      clearPoll();
      const controller = new AbortController();
      pollControllerRef.current = controller;
      setStatusChecking(true);
      try {
        const next = await backupsApi.importStatus(controller.signal);
        if (
          !mountedRef.current ||
          controller.signal.aborted ||
          revision !== fileRevisionRef.current
        ) {
          return;
        }
        if (
          expectedOperation !== null &&
          next.operationId !== expectedOperation
        ) {
          lastObservedOperationRef.current = next.operationId;
          setStatusDisconnected(false);
          if (
            activePhases.has(next.phase) ||
            next.phase === "needs_attention"
          ) {
            foreignOperationRef.current = next.operationId;
            setStatus(next);
            setRestoreState(
              next.phase === "needs_attention" ? "failed" : "idle",
            );
            if (next.phase === "needs_attention") {
              setManualStatusRequired(true);
              setRestoreError(
                t(
                  "服务器记录已经切换到另一项需要人工处理的恢复。本页不会把它归为本次提交；请核对两个操作记录。",
                ),
              );
            } else {
              setRestoreError(
                t(
                  "服务器记录已经切换到另一项恢复。本页不会把它归为本次提交；正在跟随当前恢复。",
                ),
              );
            }
            if (activePhases.has(next.phase)) {
              pollTimerRef.current = window.setTimeout(
                () => void queryStatus(revision, next.operationId),
                1_500,
              );
            }
          } else {
            setStatus(null);
            setRestoreState("failed");
            setManualStatusRequired(true);
            setRestoreError(
              t(
                "恢复操作记录已变化，无法证明本次提交的终态。不会重传文件；请人工核对两个操作记录。",
              ),
            );
          }
          return;
        }
        if (
          ignoredBaseline !== null &&
          next.operationId === ignoredBaseline &&
          expectedOperation === null
        ) {
          setStatusDisconnected(false);
          unassociatedPollsRef.current += 1;
          if (unassociatedPollsRef.current >= 5) {
            setRestoreState("failed");
            setManualStatusRequired(true);
            setRestoreError(
              t(
                "尚未观察到与本次提交关联的新恢复操作。不会重传文件；请人工核对服务器状态后再决定下一步。",
              ),
            );
            return;
          }
          pollTimerRef.current = window.setTimeout(
            () => void queryStatus(revision, null, ignoredBaseline),
            1_000,
          );
          return;
        }
        if (foreignOperationRef.current === next.operationId) {
          lastObservedOperationRef.current = next.operationId;
          setStatusDisconnected(false);
          if (activePhases.has(next.phase)) {
            setStatus(next);
            setRestoreState("idle");
            setRestoreError(
              t("正在跟随服务器上的另一项恢复；它不会被归为本次提交。"),
            );
            pollTimerRef.current = window.setTimeout(
              () => void queryStatus(revision, next.operationId),
              1_500,
            );
          } else {
            setStatus(next.phase === "needs_attention" ? next : null);
            setRestoreState("failed");
            setManualStatusRequired(true);
            setRestoreError(
              t(
                "另一项恢复已经到达终态，但无法证明本次提交的结果。不会重传文件；请人工核对操作记录。",
              ),
            );
          }
          return;
        }
        unassociatedPollsRef.current = 0;
        lastObservedOperationRef.current = next.operationId;
        setStatus(next);
        setStatusDisconnected(false);
        setManualStatusRequired(false);
        setRestoreError(null);
        if (activePhases.has(next.phase)) {
          pollTimerRef.current = window.setTimeout(
            () => void queryStatus(revision, next.operationId, ignoredBaseline),
            1_500,
          );
        } else {
          setRestoreState(next.phase === "succeeded" ? "complete" : "failed");
          if (
            next.phase === "succeeded" &&
            (restoreAttemptedRef.current || expectedOperation !== null)
          ) {
            setSiteRefreshRequired(true);
          }
        }
      } catch (error) {
        if (
          !mountedRef.current ||
          controller.signal.aborted ||
          revision !== fileRevisionRef.current
        ) {
          return;
        }
        if (
          error instanceof PublicApiError &&
          error.code === "IMPORT_STAGING_INVALID" &&
          expectedOperation === null
        ) {
          if (!restoreAttemptedRef.current) {
            setStatus(null);
            setStatusDisconnected(false);
          } else {
            unassociatedPollsRef.current += 1;
            if (unassociatedPollsRef.current >= 5) {
              setRestoreState("failed");
              setManualStatusRequired(true);
              setRestoreError(
                t(
                  "服务器尚未记录与本次提交关联的恢复操作。不会重传文件；请人工核对状态。",
                ),
              );
            } else {
              pollTimerRef.current = window.setTimeout(
                () => void queryStatus(revision, null, ignoredBaseline),
                1_000,
              );
            }
          }
        } else if (!expireIfInvalidSession(error)) {
          setStatusDisconnected(true);
          pollTimerRef.current = window.setTimeout(
            () =>
              void queryStatus(revision, expectedOperation, ignoredBaseline),
            2_500,
          );
        }
      } finally {
        if (
          mountedRef.current &&
          !controller.signal.aborted &&
          revision === fileRevisionRef.current
        ) {
          setStatusChecking(false);
          setStatusInitialized(true);
        }
      }
    },
    [api, clearPoll, expireIfInvalidSession],
  );

  useEffect(() => {
    mountedRef.current = true;
    void queryStatus(fileRevisionRef.current);
    return () => {
      mountedRef.current = false;
      fileRevisionRef.current += 1;
      mergeControllerRef.current?.abort();
      restoreControllerRef.current?.abort();
      clearPoll();
      if (exportGuardTimerRef.current !== null) {
        window.clearTimeout(exportGuardTimerRef.current);
      }
      exportGuardRef.current = false;
      const mergeToken = mergeTokenRef.current;
      const csrfToken = csrfTokenRef.current;
      if (mergeToken && csrfToken && !restoreSentRef.current) {
        void backupsApi
          .discardMergeImport(csrfToken, mergeToken, undefined, true)
          .then(() => forgetMergeStaging(stagingKey, mergeToken))
          .catch(() => undefined);
      }
    };
  }, [clearPoll, queryStatus]);

  useEffect(() => {
    if (!focusStatusRef.current || restoreState !== "running") return;
    const frame = window.requestAnimationFrame(() => {
      statusRef.current?.focus();
      focusStatusRef.current = false;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [restoreState]);

  const download = async () => {
    if (!session || exportGuardRef.current) return;
    exportGuardRef.current = true;
    exportGuardTimerRef.current = window.setTimeout(() => {
      exportGuardRef.current = false;
      exportGuardTimerRef.current = null;
    }, 1_000);
    setExportState("running");
    setExportNotice(t("正在把同源备份请求交给浏览器…"));
    try {
      if (!(await ensureCredentialConfirmation())) {
        setExportState("failed");
        setExportNotice(t("身份验证未完成，操作未执行。"));
        return;
      }
      const anchor = document.createElement("a");
      anchor.href = "/api/v1/backups/export";
      anchor.download = "moment-export.zip";
      anchor.hidden = true;
      document.body.append(anchor);
      anchor.click();
      anchor.remove();
      setExportState("complete");
      setExportNotice(
        t("已请求下载。请在浏览器下载列表确认进度、完成状态与保存位置。"),
      );
    } catch {
      setExportState("failed");
      setExportNotice(t("浏览器没有接受下载请求。页面状态未改变，请重试。"));
    }
  };

  const discardCurrentMerge = async (): Promise<boolean> => {
    const token = mergeTokenRef.current;
    if (!token) return true;
    if (!session) {
      setMergeError(t("登录会话已失效，暂时无法放弃上一次合并检查。"));
      return false;
    }
    try {
      await backupsApi.discardMergeImport(session.csrfToken, token);
    } catch (error) {
      if (!(
        error instanceof PublicApiError &&
        (error.code === "IMPORT_STAGING_INVALID" ||
          error.code === "IMPORT_STAGING_EXPIRED")
      )) {
        expireIfInvalidSession(error);
        setMergeError(
          t(
            "暂时无法放弃上一次合并检查。请重试，或等待计划过期后再选择新文件。",
          ),
        );
        return false;
      }
    }
    mergeTokenRef.current = null;
    forgetMergeStaging(stagingKey, token);
    return true;
  };

  const chooseFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const next = event.currentTarget.files?.[0] ?? null;
    event.currentTarget.value = "";
    if (!next || restoreBusy) return;
    mergeControllerRef.current?.abort();
    clearPoll();
    if (mergeTokenRef.current && !(await discardCurrentMerge())) return;
    fileRevisionRef.current += 1;
    restoreAttemptedRef.current = false;
    unassociatedPollsRef.current = 0;
    foreignOperationRef.current = null;
    setFile(next);
    setMergeInspection(null);
    setMergeSelection(null);
    setMergeState("idle");
    setMergeProgress(null);
    setMergeError(null);
    setRestoreState("idle");
    setRestoreError(null);
    setStatus(null);
    setStatusDisconnected(false);
    setManualStatusRequired(false);
    if (next.size === 0) {
      setMergeState("failed");
      setMergeError(t("空文件不能作为备份。请选择 Moment 导出的 ZIP 文件。"));
      window.requestAnimationFrame(() => pickerRef.current?.focus());
      return;
    }
    setMergeState("idle");
    setMergeError(null);
  };

  const inspectMerge = async () => {
    if (!session || !file || mergeState === "running" || restoreBusy) return;
    mergeControllerRef.current?.abort();
    setMergeError(null);
    if (mergeTokenRef.current && !(await discardCurrentMerge())) {
      setMergeState("failed");
      return;
    }
    const controller = new AbortController();
    mergeControllerRef.current = controller;
    const revision = fileRevisionRef.current;
    setMergeState("running");
    setMergeProgress(null);
    setMergeError(null);
    setMergeInspection(null);
    setMergeSelection(null);
    try {
      const next = await backupsApi.reviewImport(
        session.csrfToken,
        file,
        (progress) => {
          if (
            mountedRef.current &&
            mergeControllerRef.current === controller &&
            revision === fileRevisionRef.current
          ) {
            setMergeProgress(progress);
          }
        },
        controller.signal,
      );
      if (
        !mountedRef.current ||
        controller.signal.aborted ||
        mergeControllerRef.current !== controller ||
        revision !== fileRevisionRef.current
      ) {
        return;
      }
      setMergeInspection(next);
      mergeTokenRef.current = next.stagingToken;
      rememberMergeStaging(stagingKey, next);
      setMergeSelection(structuredClone(next.plan.default_selection));
      setImportMode(next.mergeAllowed ? "merge" : "restore");
      setMergeState("complete");
    } catch (error) {
      if (
        !mountedRef.current ||
        controller.signal.aborted ||
        revision !== fileRevisionRef.current
      ) {
        return;
      }
      expireIfInvalidSession(error);
      setMergeState("failed");
      if (error instanceof PublicApiError) {
        if (error.code === "IMPORT_MERGE_PLAN_STALE") {
          setMergeError(t("当前站点已发生变化，请重新检查合并冲突。"));
        } else {
          setMergeError(importError(error, "plan"));
        }
      } else {
        setMergeError(importError(error, "plan"));
      }
    }
  };

  const chooseMergeItem = (
    kind: MergeEntityKind,
    id: string,
    action: MergeAction,
  ) => {
    setMergeSelection((current) =>
      current
        ? {
            ...current,
            items: current.items.map((item) =>
              item.kind === kind && item.id === id ? { ...item, action } : item,
            ),
          }
        : current,
    );
  };

  const chooseMergeSiteField = (field: string, action: MergeAction) => {
    setMergeSelection((current) =>
      current
        ? {
            ...current,
            site_fields: current.site_fields.map((item) =>
              item.field === field ? { ...item, action } : item,
            ),
          }
        : current,
    );
  };

  const cancelPlan = async () => {
    mergeControllerRef.current?.abort();
    mergeControllerRef.current = null;
    if (mergeTokenRef.current && !(await discardCurrentMerge())) return;
    fileRevisionRef.current += 1;
    restoreAttemptedRef.current = false;
    unassociatedPollsRef.current = 0;
    foreignOperationRef.current = null;
    setFile(null);
    setMergeInspection(null);
    setMergeSelection(null);
    setMergeState("idle");
    setMergeProgress(null);
    setMergeError(null);
    setMergeConfirmOpen(false);
    window.requestAnimationFrame(() => pickerRef.current?.focus());
  };

  const commitMerge = async () => {
    if (
      !session ||
      !mergeInspection ||
      !mergeSelection ||
      settingsBlocked ||
      !statusInitialized ||
      statusDisconnected ||
      restoreAttemptedRef.current ||
      restoreBusy
    ) {
      return;
    }
    restoreAttemptedRef.current = true;
    restoreSentRef.current = false;
    unassociatedPollsRef.current = 0;
    foreignOperationRef.current = null;
    const revision = fileRevisionRef.current;
    const controller = new AbortController();
    restoreControllerRef.current = controller;
    clearPoll();
    setImportMode("merge");
    setRestoreState("running");
    setRestoreError(null);
    setManualStatusRequired(false);
    setStatus(null);
    try {
      let baseline: ImportStatus | null = null;
      try {
        baseline = await backupsApi.importStatus(controller.signal);
      } catch (error) {
        if (!(
          error instanceof PublicApiError &&
          error.code === "IMPORT_STAGING_INVALID"
        )) {
          throw error;
        }
      }
      if (
        !mountedRef.current ||
        controller.signal.aborted ||
        restoreControllerRef.current !== controller ||
        revision !== fileRevisionRef.current
      ) {
        return;
      }
      restoreBaselineRef.current = baseline?.operationId ?? null;
      lastObservedOperationRef.current = baseline?.operationId ?? null;
      if (
        baseline &&
        (activePhases.has(baseline.phase) ||
          baseline.phase === "needs_attention")
      ) {
        restoreAttemptedRef.current = false;
        setStatus(baseline);
        setRestoreState(
          baseline.phase === "needs_attention" ? "failed" : "idle",
        );
        setRestoreError(t("服务器已有另一项导入正在进行，本次合并尚未提交。"));
        if (activePhases.has(baseline.phase)) {
          void queryStatus(revision, baseline.operationId);
        }
        return;
      }
      restoreSentRef.current = true;
      const next = await backupsApi.commitMergeImport(
        session.csrfToken,
        mergeInspection.stagingToken,
        mergeSelection,
        controller.signal,
      );
      if (
        !mountedRef.current ||
        controller.signal.aborted ||
        revision !== fileRevisionRef.current
      ) {
        return;
      }
      setStatus(next);
      const committedToken = mergeTokenRef.current;
      if (committedToken) forgetMergeStaging(stagingKey, committedToken);
      mergeTokenRef.current = null;
      setRestoreState("complete");
      setSiteRefreshRequired(true);
    } catch (error) {
      if (!mountedRef.current || revision !== fileRevisionRef.current) return;
      if (
        error instanceof PublicApiError &&
        error.status >= 400 &&
        error.status < 500
      ) {
        restoreAttemptedRef.current = false;
        restoreSentRef.current = false;
        expireIfInvalidSession(error);
        setRestoreState("failed");
        if (
          error.code === "IMPORT_MERGE_PLAN_STALE" ||
          error.code === "IMPORT_STAGING_INVALID"
        ) {
          const invalidToken = mergeTokenRef.current;
          if (invalidToken) forgetMergeStaging(stagingKey, invalidToken);
          mergeTokenRef.current = null;
          setMergeInspection(null);
          setMergeSelection(null);
          setMergeState("failed");
          setMergeError(
            t("合并计划已过期或当前站点已变化，请重新检查合并冲突。"),
          );
        } else if (error.code === "REAUTHENTICATION_REQUIRED") {
          setMergeError(
            t("身份验证未完成，操作未执行。当前审阅和选择已保留。"),
          );
        } else if (error.code === "IMPORT_IN_PROGRESS") {
          setMergeError(
            t("另一项导入刚刚开始，本次合并没有提交。请稍后重新检查。"),
          );
        } else {
          setMergeError(
            t("服务器拒绝了当前冲突选择。合并没有开始，请重新检查后再提交。"),
          );
        }
        return;
      }
      if (!restoreSentRef.current) {
        restoreAttemptedRef.current = false;
        expireIfInvalidSession(error);
        setRestoreState("failed");
        setRestoreError(t("提交前无法确认服务器状态，合并尚未开始。"));
        setStatusDisconnected(true);
        return;
      }
      expireIfInvalidSession(error);
      setRestoreState("running");
      setRestoreError(
        t("合并结果尚未确认。不会重复提交；正在查询服务器保存的导入状态。"),
      );
      void queryStatus(revision, null, restoreBaselineRef.current);
    }
  };

  const restore = async () => {
    if (
      !session ||
      !mergeInspection ||
      !mergeInspection.disk.spaceOK ||
      settingsBlocked ||
      !statusInitialized ||
      statusDisconnected ||
      restoreAttemptedRef.current ||
      restoreBusy
    ) {
      return;
    }
    restoreAttemptedRef.current = true;
    restoreSentRef.current = false;
    unassociatedPollsRef.current = 0;
    foreignOperationRef.current = null;
    const revision = fileRevisionRef.current;
    const controller = new AbortController();
    restoreControllerRef.current = controller;
    clearPoll();
    setImportMode("restore");
    setRestoreState("running");
    setRestoreError(null);
    setManualStatusRequired(false);
    setStatus(null);
    try {
      let baseline: ImportStatus | null = null;
      try {
        baseline = await backupsApi.importStatus(controller.signal);
      } catch (error) {
        if (!(
          error instanceof PublicApiError &&
          error.code === "IMPORT_STAGING_INVALID"
        )) {
          throw error;
        }
      }
      if (
        !mountedRef.current ||
        controller.signal.aborted ||
        restoreControllerRef.current !== controller ||
        revision !== fileRevisionRef.current
      ) {
        return;
      }
      restoreBaselineRef.current = baseline?.operationId ?? null;
      lastObservedOperationRef.current = baseline?.operationId ?? null;
      if (
        baseline &&
        (activePhases.has(baseline.phase) ||
          baseline.phase === "needs_attention")
      ) {
        restoreAttemptedRef.current = false;
        setStatus(baseline);
        setRestoreState(
          baseline.phase === "needs_attention" ? "failed" : "idle",
        );
        setRestoreError(
          baseline.phase === "needs_attention"
            ? t(
                "服务器仍有需要人工处理的恢复状态，本文件尚未上传。先完成主机侧恢复处置。",
              )
            : t(
                "检测到另一项恢复正在进行，本文件尚未上传。正在跟随当前恢复状态。",
              ),
        );
        if (activePhases.has(baseline.phase)) {
          void queryStatus(revision, baseline.operationId);
        }
        return;
      }
      restoreSentRef.current = true;
      const next = await backupsApi.commitReviewedReplacement(
        session.csrfToken,
        mergeInspection.stagingToken,
        mergeInspection.plan.plan_id,
        controller.signal,
      );
      if (
        !mountedRef.current ||
        controller.signal.aborted ||
        revision !== fileRevisionRef.current
      ) {
        return;
      }
      setStatus(next);
      const committedToken = mergeTokenRef.current;
      if (committedToken) forgetMergeStaging(stagingKey, committedToken);
      mergeTokenRef.current = null;
      setRestoreState("complete");
      setSiteRefreshRequired(true);
    } catch (error) {
      if (!mountedRef.current || revision !== fileRevisionRef.current) return;
      if (
        error instanceof PublicApiError &&
        error.status >= 400 &&
        error.status < 500
      ) {
        restoreAttemptedRef.current = false;
        restoreSentRef.current = false;
        expireIfInvalidSession(error);
        setRestoreState("failed");
        if (
          error.code === "IMPORT_MERGE_PLAN_STALE" ||
          error.code === "IMPORT_STAGING_INVALID"
        ) {
          const invalidToken = mergeTokenRef.current;
          if (invalidToken) forgetMergeStaging(stagingKey, invalidToken);
          mergeTokenRef.current = null;
          setMergeInspection(null);
          setMergeSelection(null);
          setMergeState("failed");
          setMergeError(
            t(
              "审阅后当前站点已经发生变化。为保护新内容，恢复已拒绝；请重新检查备份。",
            ),
          );
        } else if (error.code === "REAUTHENTICATION_REQUIRED") {
          setRestoreError(
            t("身份验证未完成，操作未执行。当前审阅和选择已保留。"),
          );
        } else {
          setRestoreError(
            t("服务器拒绝了这次恢复，当前站点没有被替换。请重新检查后再试。"),
          );
        }
        return;
      }
      if (!restoreSentRef.current) {
        restoreAttemptedRef.current = false;
        expireIfInvalidSession(error);
        setRestoreState("failed");
        setRestoreError(
          t(
            "提交前无法确认服务器当前恢复状态，备份尚未上传。连接恢复后可再次确认。",
          ),
        );
        setStatusDisconnected(true);
        return;
      }
      expireIfInvalidSession(error);
      setRestoreState("running");
      setRestoreError(
        t("恢复结果尚未确认。不会重新提交文件；正在查询服务器保存的恢复状态。"),
      );
      void queryStatus(revision, null, restoreBaselineRef.current);
    }
  };

  const stopWaiting = () => {
    restoreControllerRef.current?.abort();
    if (!restoreSentRef.current) {
      setRestoreError(t("已停止提交前状态检查，备份尚未上传。"));
      return;
    }
    setRestoreError(
      t(
        "已停止等待原请求。恢复可能仍在服务器执行；不会重传文件，正在查询状态。",
      ),
    );
    const observedNewOperation =
      status?.operationId && status.operationId !== restoreBaselineRef.current
        ? status.operationId
        : null;
    void queryStatus(
      fileRevisionRef.current,
      observedNewOperation,
      restoreBaselineRef.current,
    );
  };

  const currentStatus = status
    ? statusCopy(status, importMode === "merge")
    : null;
  const conflictItems = mergeInspection?.review.conflicts.items ?? [];
  // Accounts are reported, never chosen, so they cannot share a list with the
  // rows that carry decision controls -- a radio group beside a person is an
  // offer the server would refuse. They are gathered out of all three buckets
  // into one read-only section instead, where the rule can be stated once
  // rather than implied by an absence of buttons next to a post.
  //
  // Which is why each section heading counts its own rows rather than the
  // bucket total the server sent: the total includes the account rows that
  // were just moved out of it, so an archive carrying accounts made every
  // heading claim more items than the list beneath it holds. Truncation is
  // said in words underneath instead, where it can say what it means.
  const isAccountRow = (item: MergeReviewItem) =>
    item.kind === "user" || item.kind === "identity";
  const mergeItemConflicts = conflictItems.filter(
    (item) => !isAccountRow(item),
  );
  const mergeLocalOnly =
    mergeInspection?.review.local_only.items.filter(
      (item) => !isAccountRow(item),
    ) ?? [];
  const mergeIncomingOnly =
    mergeInspection?.review.incoming_only.items.filter(
      (item) => !isAccountRow(item),
    ) ?? [];
  const mergeAccountRows = [
    ...conflictItems,
    ...(mergeInspection?.review.incoming_only.items ?? []),
    ...(mergeInspection?.review.local_only.items ?? []),
  ].filter(isAccountRow);
  const mergeSiteConflicts =
    mergeInspection?.review.site_fields.filter(
      (field) => field.classification !== "unchanged",
    ) ?? [];
  const showRestoreStatus =
    restoreState === "running" ||
    status !== null ||
    statusDisconnected ||
    manualStatusRequired;

  const restoreStatusPanel = showRestoreStatus ? (
    <div
      aria-live="polite"
      className={styles.restoreStatus}
      data-tone={currentStatus?.tone ?? "working"}
      ref={statusRef}
      tabIndex={-1}
    >
      <p>{currentStatus?.eyebrow ?? t("恢复状态")}</p>
      <h3>{currentStatus?.title ?? t("正在确认服务器状态")}</h3>
      <span>
        {currentStatus?.detail ??
          t("恢复请求可能仍在运行。本页不会重新提交备份，只会查询服务器记录。")}
      </span>
      {restoreError ? <p className={styles.dataError}>{restoreError}</p> : null}
      {statusDisconnected ? (
        <p className={styles.dataError}>
          {t("暂时无法连接服务器。页面会继续重试状态查询，不会重传备份。")}
        </p>
      ) : null}
      {manualStatusRequired ? (
        <p className={styles.dataError}>
          {t(
            "自动关联已经停止。先在实例主机核对恢复状态；本页不会自行再次提交备份。",
          )}
        </p>
      ) : null}
      {status?.phase === "needs_attention" ? (
        <details
          className={styles.technicalDetails}
          onToggle={(event) => {
            if (!event.currentTarget.open) event.currentTarget.open = true;
          }}
          open
        >
          <summary>{t("需要核对的技术信息")}</summary>
          <dl className={styles.operatorFacts}>
            <div>
              <dt>{t("操作")}</dt>
              <dd>{status.operationId}</dd>
            </div>
            <div>
              <dt>{t("错误码")}</dt>
              <dd>{status.errorCode}</dd>
            </div>
            <div>
              <dt>{t("当前数据版本")}</dt>
              <dd>{status.activeGeneration}</dd>
            </div>
          </dl>
        </details>
      ) : null}
      <div className={styles.inlineActions}>
        {restoreState === "running" ||
        activePhases.has(status?.phase ?? "succeeded") ? (
          <button
            className={styles.secondaryButton}
            onClick={stopWaiting}
            type="button"
          >
            {t("停止等待并查询状态")}
          </button>
        ) : null}
        {statusDisconnected || manualStatusRequired ? (
          <button
            className={styles.secondaryButton}
            disabled={statusChecking}
            onClick={() => {
              unassociatedPollsRef.current = 0;
              setManualStatusRequired(false);
              const observedNewOperation =
                status?.operationId &&
                status.operationId !== restoreBaselineRef.current
                  ? status.operationId
                  : null;
              void queryStatus(
                fileRevisionRef.current,
                observedNewOperation,
                restoreBaselineRef.current,
              );
            }}
            type="button"
          >
            {t("立即重试状态")}
          </button>
        ) : null}
        {status?.phase === "succeeded" ? (
          <button
            className={styles.primaryButton}
            onClick={() => window.location.reload()}
            type="button"
          >
            {t("重新载入已恢复站点")}
          </button>
        ) : null}
      </div>
    </div>
  ) : (
    <div aria-live="polite" className={styles.backupOverview}>
      <p>{t("最近状态")}</p>
      <h3>
        {statusChecking ? t("正在确认恢复状态") : t("当前没有进行中的恢复")}
      </h3>
      <span>
        {statusChecking
          ? t("确认完成前，站点设置与恢复入口会保持只读。")
          : t("可以下载当前站点的完整备份，或选择备份文件进行安全预检。")}
      </span>
    </div>
  );

  return (
    <section
      aria-label={t("备份与恢复")}
      className={styles.portableFlow}
      data-settings-panel={panel}
    >
      <header className={styles.sectionHeader}>
        <h2>{t("备份与恢复")}</h2>
      </header>
      {restoreStatusPanel}
      <div
        className={styles.portableStep}
        data-backup-export
        hidden={panel === "restore"}
      >
        <div>
          <h3>{t("导出完整站点")}</h3>
          <p>
            {t("包含资料、Moment、回复和图片，不包含登录密钥与浏览器草稿。")}
          </p>
          <button
            aria-busy={exportState === "running"}
            className={styles.secondaryButton}
            disabled={!session || exportState === "running" || exportBlocked}
            onClick={download}
            type="button"
          >
            {exportState === "running" ? t("正在请求下载…") : t("下载整站备份")}
          </button>
          {exportNotice ? (
            <p
              className={
                exportState === "failed" ? styles.dataError : styles.dataNotice
              }
              role={exportState === "failed" ? "alert" : "status"}
            >
              {exportNotice}
            </p>
          ) : null}
        </div>
      </div>

      <div className={styles.portableStep}>
        <div>
          <h3>{t("检查备份")}</h3>
          <p>{t("选择文件会先检查内容，不会立即替换当前网站。")}</p>
          <div className={styles.inlineActions}>
            <button
              className={styles.secondaryButton}
              disabled={
                !session ||
                !statusInitialized ||
                mergeState === "running" ||
                restoreBusy
              }
              onClick={() => fileInputRef.current?.click()}
              ref={pickerRef}
              type="button"
            >
              {file ? t("更换备份文件") : t("选择备份文件")}
            </button>
            {file ? (
              <button
                className={styles.primaryButton}
                disabled={
                  !statusInitialized || mergeState === "running" || restoreBusy
                }
                onClick={() => void inspectMerge()}
                type="button"
              >
                {mergeInspection ? t("重新检查") : t("检查备份")}
              </button>
            ) : null}
            {file ? (
              <button
                className={styles.quietButton}
                disabled={restoreBusy}
                onClick={() => void cancelPlan()}
                type="button"
              >
                {mergeState === "running" ? t("取消检查") : t("清除文件")}
              </button>
            ) : null}
          </div>
          <input
            accept=".zip,application/zip"
            aria-label={t("选择 Moment 备份 ZIP")}
            disabled={!session || !statusInitialized || restoreBusy}
            onChange={chooseFile}
            ref={fileInputRef}
            tabIndex={-1}
            type="file"
          />
          {file ? (
            <p className={styles.fileIdentity}>
              {file.name} · {formatBytes(file.size)}
            </p>
          ) : null}
          {mergeState === "running" ? (
            <>
              <progress
                aria-label={t("合并检查上传进度")}
                max="100"
                value={mergeProgress ?? undefined}
              />
              <p aria-live="polite" className={styles.dataNotice}>
                {mergeProgress === null
                  ? t("正在上传并比较当前站点…")
                  : mergeProgress < 100
                    ? t("正在上传并比较当前站点… {{value1}}%", {
                        value1: mergeProgress,
                      })
                    : t("上传完成，正在分类新增内容与冲突…")}
              </p>
            </>
          ) : null}
          {mergeError ? (
            <p className={styles.dataError} role="alert">
              {mergeError}
            </p>
          ) : null}
        </div>
      </div>

      {mergeInspection && mergeSelection ? (
        <div className={styles.mergePanel}>
          <header>
            <div>
              <p>{t("备份检查完成")}</p>
              <div className={styles.mergeHeadingLine}>
                <h3>
                  {importMode === "merge" ? t("审阅并合并") : t("审阅覆盖恢复")}
                </h3>
                <ActionTooltip
                  label={
                    importMode === "merge"
                      ? t(
                          "备份新增内容会加入站点，当前独有内容会保留。冲突内容在下方并排展示，由你逐项决定。",
                        )
                      : t(
                          "覆盖恢复会让站点与备份完全一致。下方标为“将移除”的内容只存在于当前站点，请在确认前逐条核对。",
                        )
                  }
                  wide
                >
                  <button
                    aria-label={
                      importMode === "merge"
                        ? t("了解审阅并合并")
                        : t("了解审阅覆盖恢复")
                    }
                    className={styles.mergeInformationButton}
                    type="button"
                  >
                    <Icon name="info" />
                  </button>
                </ActionTooltip>
              </div>
            </div>
            <span>
              {new Intl.DateTimeFormat(getLocale(), {
                hour: "2-digit",
                minute: "2-digit",
              }).format(new Date(mergeInspection.expiresAt))}
              {t("前提交")}
            </span>
          </header>
          <div aria-label={t("导入方式")} className={styles.importModeSwitch}>
            <button
              aria-pressed={importMode === "merge"}
              disabled={!mergeInspection.mergeAllowed}
              onClick={() => setImportMode("merge")}
              type="button"
            >
              {t("安全合并")}
            </button>
            <button
              aria-pressed={importMode === "restore"}
              onClick={() => setImportMode("restore")}
              type="button"
            >
              {t("完全替换")}
            </button>
          </div>
          {!mergeInspection.mergeAllowed ? (
            <p className={styles.dataNotice}>
              {mergeBlockedMessage(mergeInspection.mergeBlockedReason)}
            </p>
          ) : null}
          <div aria-label={t("合并分类摘要")} className={styles.mergeSummary}>
            <span>
              <strong>{mergeInspection.plan.summary.incoming_only}</strong>
              {t("新增")}
            </span>
            <span>
              <strong>{mergeInspection.plan.summary.local_only}</strong>
              {importMode === "merge" ? t("仅当前，保留") : t("仅当前，将移除")}
            </span>
            <span>
              <strong>{mergeInspection.plan.summary.unchanged}</strong>
              {t("相同")}
            </span>
            <span
              data-tone={
                mergeInspection.plan.summary.conflict ? "warning" : "success"
              }
            >
              <strong>{mergeInspection.plan.summary.conflict}</strong>
              {t("冲突")}
            </span>
          </div>

          {mergeLocalOnly.length ? (
            <section className={styles.reviewSection}>
              <div className={styles.reviewSectionHeading}>
                <h4>
                  {importMode === "merge"
                    ? t("当前独有 · 会保留")
                    : t("当前独有 · 将移除")}
                </h4>
                <span>
                  {mergeLocalOnly.length} {t("项")}
                </span>
              </div>
              <div className={styles.reviewList}>
                {mergeLocalOnly.map((item) => (
                  <ReviewItem
                    inspection={mergeInspection}
                    item={item}
                    key={`${item.kind}:${item.id}`}
                    restoreMode={importMode === "restore"}
                  />
                ))}
              </div>
              {mergeInspection.review.local_only.truncated ? (
                <p className={styles.dataNotice}>
                  {t("这里只显示最近 200 项；完整选择可用 CLI 审阅。")}
                </p>
              ) : null}
            </section>
          ) : null}

          {mergeIncomingOnly.length ? (
            <section className={styles.reviewSection}>
              <div className={styles.reviewSectionHeading}>
                <h4>{t("备份新增")}</h4>
                <span>
                  {mergeIncomingOnly.length} {t("项")}
                </span>
              </div>
              <div className={styles.reviewList}>
                {mergeIncomingOnly.map((item) => (
                  <ReviewItem
                    inspection={mergeInspection}
                    item={item}
                    key={`${item.kind}:${item.id}`}
                    restoreMode={importMode === "restore"}
                  />
                ))}
              </div>
              {mergeInspection.review.incoming_only.truncated ? (
                <p className={styles.dataNotice}>
                  {t("这里只显示最近 200 项；完整选择可用 CLI 审阅。")}
                </p>
              ) : null}
            </section>
          ) : null}

          {mergeAccountRows.length ? (
            <section
              aria-labelledby="account-rows"
              className={styles.reviewSection}
            >
              <div className={styles.reviewSectionHeading}>
                <h4 id="account-rows">{t("账户与登录方式")}</h4>
                <span>
                  {mergeAccountRows.length} {t("项")}
                </span>
              </div>
              <p className={styles.dataNotice}>{ACCOUNT_DECISION_NOTE()}</p>
              <div className={styles.reviewList}>
                {mergeAccountRows.map((item) => (
                  <div
                    className={styles.reviewAccountRow}
                    key={`${item.kind}:${item.id}`}
                  >
                    <div className={styles.reviewConflictHeading}>
                      <strong>{mergeKindLabel(item.kind)}</strong>
                      <small>{accountOutcomeLabel(item, importMode)}</small>
                    </div>
                    <ReviewItem
                      inspection={mergeInspection}
                      item={item}
                      restoreMode={importMode === "restore"}
                    />
                  </div>
                ))}
              </div>
            </section>
          ) : null}

          {mergeItemConflicts.length ? (
            <section
              aria-labelledby="content-conflicts"
              className={styles.mergeConflictSection}
            >
              <h4 id="content-conflicts">{t("内容冲突")}</h4>
              <div className={styles.mergeConflictList}>
                {mergeItemConflicts.slice(0, 200).map((item) => {
                  const decision = mergeSelection.items.find(
                    (candidate) =>
                      candidate.kind === item.kind && candidate.id === item.id,
                  );
                  return (
                    <div
                      className={styles.mergeConflict}
                      key={`${item.kind}:${item.id}`}
                    >
                      <div className={styles.reviewConflictHeading}>
                        <strong>{mergeKindLabel(item.kind)}</strong>
                        <small>
                          {t("变更：")}
                          {item.changed_fields
                            .map(mergeChangedFieldLabel)
                            .join("、") || t("内容")}
                        </small>
                      </div>
                      {importMode === "merge" ? (
                        <div
                          aria-label={t("{{value1}} {{value2}} 的冲突处理", {
                            value1: mergeKindLabel(item.kind),
                            value2: item.id,
                          })}
                          className={styles.mergeDecisionGrid}
                          role="radiogroup"
                        >
                          <DecisionChoice
                            checked={
                              (decision?.action ?? "keep_local") ===
                              "keep_local"
                            }
                            label={t("保留当前版本")}
                            onChoose={() =>
                              chooseMergeItem(item.kind, item.id, "keep_local")
                            }
                          >
                            <ReviewEntityCard
                              entity={item.local!}
                              inspection={mergeInspection}
                              label={t("当前站点")}
                            />
                          </DecisionChoice>
                          <DecisionChoice
                            checked={decision?.action === "use_incoming"}
                            label={t("使用备份版本")}
                            onChoose={() =>
                              chooseMergeItem(
                                item.kind,
                                item.id,
                                "use_incoming",
                              )
                            }
                          >
                            <ReviewEntityCard
                              entity={item.incoming!}
                              inspection={mergeInspection}
                              label={t("备份文件")}
                              tone="incoming"
                            />
                          </DecisionChoice>
                          <DecisionChoice
                            checked={decision?.action === "keep_both"}
                            label={t("两者都保留")}
                            onChoose={() =>
                              chooseMergeItem(item.kind, item.id, "keep_both")
                            }
                          >
                            <span className={styles.mergeKeepBoth}>
                              <strong>{t("两者都保留")}</strong>
                              <small>
                                {t("备份版本会作为另一条内容加入站点")}
                              </small>
                            </span>
                          </DecisionChoice>
                        </div>
                      ) : (
                        <ReviewItem
                          inspection={mergeInspection}
                          item={item}
                          restoreMode
                        />
                      )}
                    </div>
                  );
                })}
              </div>
              {mergeItemConflicts.length > 200 ? (
                <p className={styles.dataNotice}>
                  {t(
                    "当前显示前 200 项；其余冲突保持“保留当前版本”。如需逐项调整全部冲突，请使用 CLI 导出的选择文件。",
                  )}
                </p>
              ) : null}
            </section>
          ) : null}

          {mergeSiteConflicts.length ? (
            <section
              aria-labelledby="profile-conflicts"
              className={styles.mergeConflictSection}
            >
              <h4 id="profile-conflicts">{t("资料、头像与封面差异")}</h4>
              <div className={styles.mergeConflictList}>
                {mergeSiteConflicts.map((field) => {
                  const decision = mergeSelection.site_fields.find(
                    (candidate) => candidate.field === field.field,
                  );
                  return (
                    <div className={styles.mergeConflict} key={field.field}>
                      <div className={styles.reviewConflictHeading}>
                        <strong>{mergeSiteFieldLabel(field.field)}</strong>
                      </div>
                      <div
                        aria-label={t("{{value1}}的冲突处理", {
                          value1: mergeSiteFieldLabel(field.field),
                        })}
                        className={styles.mergeDecisionGrid}
                        role="radiogroup"
                      >
                        <DecisionChoice
                          checked={
                            (decision?.action ?? "keep_local") === "keep_local"
                          }
                          label={t("保留当前值")}
                          onChoose={() =>
                            chooseMergeSiteField(field.field, "keep_local")
                          }
                        >
                          <div className={styles.reviewProfileValue}>
                            <span>{t("当前站点")}</span>
                            {field.local_media ? (
                              <ReviewMediaPreview
                                inspection={mergeInspection}
                                media={field.local_media}
                              />
                            ) : field.local_entry ? (
                              <ReviewEntityCard
                                entity={field.local_entry}
                                inspection={mergeInspection}
                                label={t("当前置顶")}
                              />
                            ) : (
                              <p>{mergeValue(field.local_value)}</p>
                            )}
                          </div>
                        </DecisionChoice>
                        <DecisionChoice
                          checked={decision?.action === "use_incoming"}
                          label={t("使用备份值")}
                          onChoose={() =>
                            chooseMergeSiteField(field.field, "use_incoming")
                          }
                        >
                          <div className={styles.reviewProfileValue}>
                            <span>{t("备份文件")}</span>
                            {field.incoming_media ? (
                              <ReviewMediaPreview
                                inspection={mergeInspection}
                                media={field.incoming_media}
                              />
                            ) : field.incoming_entry ? (
                              <ReviewEntityCard
                                entity={field.incoming_entry}
                                inspection={mergeInspection}
                                label={t("备份置顶")}
                                tone="incoming"
                              />
                            ) : (
                              <p>{mergeValue(field.incoming_value)}</p>
                            )}
                          </div>
                        </DecisionChoice>
                      </div>
                    </div>
                  );
                })}
              </div>
            </section>
          ) : null}

          {!mergeItemConflicts.length && !mergeSiteConflicts.length ? (
            <p className={styles.mergeClear}>
              {t("没有需要手动处理的冲突，可以直接合并。")}
            </p>
          ) : null}
          {mergeInspection.warnings.length ? (
            <div className={styles.importWarnings}>
              <strong>{t("合并提醒")}</strong>
              <ul>
                {mergeInspection.warnings.map((warning) => (
                  <li key={warning}>{warning}</li>
                ))}
              </ul>
            </div>
          ) : null}
          {settingsBlocked ? (
            <p className={styles.dataError} role="alert">
              {t("先保存或放弃当前设置与头像更改，才能提交合并。")}
            </p>
          ) : null}
          {importMode === "merge" ? (
            <Dialog.Root
              onOpenChange={setMergeConfirmOpen}
              open={mergeConfirmOpen}
            >
              <Dialog.Trigger
                className={styles.dialogTrigger}
                disabled={
                  !session ||
                  settingsBlocked ||
                  !statusInitialized ||
                  statusDisconnected ||
                  restoreAttemptedRef.current ||
                  restoreBusy
                }
                render={
                  <button className={styles.primaryButton} type="button" />
                }
              >
                {t("提交合并")}
              </Dialog.Trigger>
              <Dialog.Portal>
                <Dialog.Backdrop className={styles.dialogBackdrop} />
                <Dialog.Viewport className={styles.dialogViewport}>
                  <Dialog.Popup className={styles.restoreDialog}>
                    <Dialog.Title>{t("按当前选择合并这份备份？")}</Dialog.Title>
                    <Dialog.Description>
                      {t("将加入")} {mergeInspection.incomingCounts.entries}{" "}
                      {t("条备份 Moment 中可合并的内容，并按你选择的方式处理")}{" "}
                      {mergeInspection.plan.summary.conflict} {t("项冲突。")}
                    </Dialog.Description>
                    <div className={styles.dialogImpact}>
                      <div>
                        <span>{t("新增")}</span>
                        <strong>
                          {mergeInspection.plan.summary.incoming_only} {t("项")}
                        </strong>
                      </div>
                      <div>
                        <span>{t("仅本地")}</span>
                        <strong>
                          {mergeInspection.plan.summary.local_only}{" "}
                          {t("项，全部保留")}
                        </strong>
                      </div>
                      <div>
                        <span>{t("冲突")}</span>
                        <strong>
                          {mergeInspection.plan.summary.conflict}{" "}
                          {t("项，按选择处理")}
                        </strong>
                      </div>
                    </div>
                    <p className={styles.dialogRisk}>
                      <strong>{t("合并前仍会自动备份当前站点。")}</strong>
                      {t(
                        "计划已绑定当前数据版本；若站点已变化，服务器会拒绝旧计划并要求重新检查。",
                      )}
                    </p>
                    <div className={styles.dialogActions}>
                      <Dialog.Close className={styles.secondaryButton}>
                        {t("返回检查")}
                      </Dialog.Close>
                      <button
                        aria-busy={restoreState === "running"}
                        className={styles.primaryButton}
                        disabled={restoreState === "running"}
                        onClick={() => {
                          focusStatusRef.current = true;
                          setMergeConfirmOpen(false);
                          void commitMerge();
                        }}
                        type="button"
                      >
                        {t("确认并合并")}
                      </button>
                    </div>
                  </Dialog.Popup>
                </Dialog.Viewport>
              </Dialog.Portal>
            </Dialog.Root>
          ) : (
            <Dialog.Root onOpenChange={setConfirmOpen} open={confirmOpen}>
              <Dialog.Trigger
                className={styles.dialogTrigger}
                disabled={
                  !session ||
                  !mergeInspection.disk.spaceOK ||
                  settingsBlocked ||
                  !statusInitialized ||
                  statusDisconnected ||
                  restoreAttemptedRef.current ||
                  restoreBusy
                }
                render={
                  <button className={styles.dangerButton} type="button" />
                }
              >
                {t("确认完全替换")}
              </Dialog.Trigger>
              <Dialog.Portal>
                <Dialog.Backdrop className={styles.dialogBackdrop} />
                <Dialog.Viewport className={styles.dialogViewport}>
                  <Dialog.Popup className={styles.restoreDialog}>
                    <Dialog.Close
                      aria-label={t("关闭覆盖恢复确认")}
                      className={styles.dialogClose}
                    >
                      ×
                    </Dialog.Close>
                    <Dialog.Title>{t("完全替换当前站点？")}</Dialog.Title>
                    <Dialog.Description>
                      {t("备份中的")} {mergeInspection.incomingCounts.entries}{" "}
                      {t("条 Moment、")}
                      {mergeInspection.incomingCounts.comments} {t("条评论和")}
                      {mergeInspection.incomingCounts.media}{" "}
                      {t(
                        "个媒体记录会成为完整站点。站内账户也会整体换成备份里的",
                      )}{" "}
                      {mergeInspection.incomingCounts.users}{" "}
                      {t("个，现在的账户与")}
                      {mergeInspection.replacement.counts.identities}{" "}
                      {t("个已关联登录方式随之消失。")}
                    </Dialog.Description>
                    <div className={styles.dialogImpact}>
                      <div>
                        <span>{t("备份内容")}</span>
                        <strong>
                          {mergeInspection.incomingCounts.entries}{" "}
                          {t("Moment ·")}{" "}
                          {mergeInspection.incomingCounts.comments}{" "}
                          {t("评论 ·")} {mergeInspection.incomingCounts.users}{" "}
                          {t("账户")}
                        </strong>
                      </div>
                      <div>
                        <span>{t("现有账户")}</span>
                        <strong>
                          {mergeInspection.replacement.counts.users}{" "}
                          {t("个将被替换")}
                        </strong>
                      </div>
                      <div>
                        <span>{t("当前独有")}</span>
                        <strong>
                          {mergeInspection.review.local_only.total}{" "}
                          {t("项将移除")}
                        </strong>
                      </div>
                      <div>
                        <span>{t("容量")}</span>
                        <strong>
                          {mergeInspection.disk.spaceOK
                            ? t("检查通过")
                            : t("空间不足")}
                        </strong>
                      </div>
                    </div>
                    <p className={styles.dialogRisk}>
                      <strong>{t("服务器会先自动备份当前站点。")}</strong>
                      {t(
                        "如果你在本次审阅后又发布或修改了内容，服务器会拒绝恢复并要求重新检查。",
                      )}
                    </p>
                    <div className={styles.dialogActions}>
                      <Dialog.Close className={styles.secondaryButton}>
                        {t("返回检查")}
                      </Dialog.Close>
                      <button
                        aria-busy={restoreState === "running"}
                        className={styles.dangerButton}
                        disabled={restoreState === "running"}
                        onClick={() => {
                          focusStatusRef.current = true;
                          setConfirmOpen(false);
                          void restore();
                        }}
                        type="button"
                      >
                        {t("确认完全替换")}
                      </button>
                    </div>
                  </Dialog.Popup>
                </Dialog.Viewport>
              </Dialog.Portal>
            </Dialog.Root>
          )}
        </div>
      ) : null}
    </section>
  );
}
