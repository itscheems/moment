import { Dialog } from "@base-ui/react/dialog";
import {
  type ChangeEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import type {
  ImportPlan,
  ImportStatus,
  MergeAction,
  MergeEntityKind,
  MergeInspection,
  MergeSelection,
} from "../../api/owner";
import { PublicApiError } from "../../api/public";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { useOwner } from "./OwnerProvider";
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

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes / 1024;
  let unit = units[0];
  for (let index = 1; index < units.length && value >= 1024; index += 1) {
    value /= 1024;
    unit = units[index];
  }
  return `${new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 1 }).format(value)} ${unit}`;
}

function formatDate(value: string | null): string {
  if (!value) return "无已发布内容";
  return (
    new Intl.DateTimeFormat("zh-CN", {
      dateStyle: "medium",
      timeStyle: "short",
      timeZone: "UTC",
    }).format(new Date(value)) + " UTC"
  );
}

function mergeKindLabel(kind: MergeEntityKind): string {
  switch (kind) {
    case "entry":
      return "Moment";
    case "comment":
      return "评论";
    case "media":
      return "媒体";
  }
}

function mergeValue(value: unknown): string {
  if (value === null) return "未设置";
  if (typeof value === "string") return value || "空值";
  if (typeof value === "boolean") return value ? "开启" : "关闭";
  return JSON.stringify(value);
}

function mergeSiteFieldLabel(field: string): string {
  const labels: Record<string, string> = {
    username: "用户名",
    display_name: "显示名称",
    title: "站点标题",
    bio: "简介",
    timezone: "时区",
    avatar_media_id: "头像",
    cover_media_id: "主页封面",
    pinned_post_id: "置顶 Moment",
    comments_enabled: "评论开关",
  };
  return labels[field] ?? field;
}

function mergeChangedFieldLabel(field: string): string {
  const labels: Record<string, string> = {
    kind: "类型",
    content_md: "正文",
    created_at: "创建时间",
    updated_at: "更新时间",
    published_at: "发布时间",
    media: "媒体与图片描述",
    location: "位置",
    entry_id: "所属 Moment",
    parent_id: "上级评论",
    author_kind: "作者类型",
    author_name: "作者名称",
    sha256: "文件内容",
    original_name: "原文件名",
    mime_type: "文件类型",
    byte_size: "文件大小",
    width: "宽度",
    height: "高度",
  };
  return labels[field] ?? field;
}

function importError(error: unknown, operation: "plan" | "restore"): string {
  if (!(error instanceof PublicApiError)) {
    return operation === "plan"
      ? "预检没有完成。当前站点没有改变，请重新选择或重试。"
      : "服务器明确拒绝了恢复，当前站点没有被确认替换。请重新预检。";
  }
  switch (error.code) {
    case "EXPORT_INVALID":
    case "EXPORT_CHECKSUM_MISMATCH":
    case "EXPORT_VERSION_UNSUPPORTED":
    case "IMPORT_STAGING_INVALID":
      return "这个文件不是可恢复的 Moment 备份，或其校验已经失效。请换一个原始导出文件。";
    case "IMPORT_DISK_SPACE_INSUFFICIENT":
      return "可用磁盘空间不足。释放空间后重新预检，恢复尚未开始。";
    case "IMPORT_IN_PROGRESS":
    case "MAINTENANCE_MODE":
      return "另一个恢复或维护操作正在进行。请先查询当前状态，稍后再预检。";
    case "REQUEST_BODY_TOO_LARGE":
      return "备份文件超过此实例允许的上传大小。请检查实例限制或使用 CLI。";
    case "UNAUTHORIZED":
    case "CSRF_INVALID":
      return "所有者会话已经失效。备份文件仍只留在浏览器内，请重新登录。";
    case "CANCELLED":
      return "预检上传已取消，当前站点没有改变。";
    case "API_RESPONSE_INVALID":
      return "服务器响应无法确认。不要再次恢复，请检查恢复状态。";
    default:
      return operation === "plan"
        ? "预检没有完成。当前站点没有改变，可以安全重试。"
        : "服务器明确返回恢复失败。请重新预检后再决定。";
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
        eyebrow: "准备恢复",
        title: "服务器已接管恢复",
        detail: "正在重新校验预检绑定，当前站点尚未切换。",
        tone: "working",
      };
    case "backing_up":
      return {
        eyebrow: "自动备份",
        title: "正在备份当前站点",
        detail: "只有自动备份验证完成，恢复才会继续。",
        tone: "working",
      };
    case "finalizing":
      return {
        eyebrow: "准备数据",
        title: "正在准备恢复数据",
        detail: "预检内容已经重新验证，当前站点仍在使用。",
        tone: "working",
      };
    case "switching":
      return {
        eyebrow: "切换数据",
        title: "正在切换活动数据",
        detail: "短暂维护期间不要提交其他写操作，本页会查询服务器状态。",
        tone: "working",
      };
    case "verifying":
      return {
        eyebrow: "验证数据",
        title: "正在验证恢复后的站点",
        detail: "只有新数据通过就绪检查，本页才会报告成功。",
        tone: "working",
      };
    case "rolling_back":
      return {
        eyebrow: "自动回退",
        title: "正在尝试恢复原站点",
        detail: "新数据没有通过验证，服务器正在核对并恢复先前数据。",
        tone: "warning",
      };
    case "succeeded":
      return {
        eyebrow: merge ? "合并完成" : "恢复完成",
        title: merge ? "备份已合并并通过就绪检查" : "备份已恢复并通过就绪检查",
        detail: merge
          ? "合并前自动备份已验证。重新加载页面即可查看合并后的内容。"
          : "恢复前自动备份已验证。重新加载页面即可查看恢复后的内容。",
        tone: "success",
      };
    case "failed":
      return {
        eyebrow: "恢复停止",
        title: "恢复在切换前停止",
        detail: "旧数据仍是活动数据。修复错误后，从预检重新开始。",
        tone: "warning",
      };
    case "rolled_back":
      return {
        eyebrow: "已恢复原数据",
        title: "已自动恢复原站点",
        detail: "新数据未通过就绪检查，原数据已经重新启用并验证。",
        tone: "warning",
      };
    case "needs_attention":
      return {
        eyebrow: "需要人工处理",
        title: "恢复状态需要人工处理",
        detail:
          "服务器无法证明旧数据或新数据处于安全终态。停止写入并运行 moment doctor --deep --json。",
        tone: "danger",
      };
  }
}

export function BackupRestore({
  settingsBlocked,
  onRestoreBusyChange,
}: {
  settingsBlocked: boolean;
  onRestoreBusyChange: (busy: boolean) => void;
}) {
  const { api, session, expireIfInvalidSession } = useOwner();
  const mountedRef = useRef(true);
  const fileRevisionRef = useRef(0);
  const planControllerRef = useRef<AbortController | null>(null);
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
  const exportGuardRef = useRef(false);
  const exportGuardTimerRef = useRef<number | null>(null);
  const pickerRef = useRef<HTMLButtonElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const statusRef = useRef<HTMLDivElement>(null);
  const focusStatusRef = useRef(false);
  const [file, setFile] = useState<File | null>(null);
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [planState, setPlanState] = useState<TransferState>("idle");
  const [planProgress, setPlanProgress] = useState<number | null>(null);
  const [planError, setPlanError] = useState<string | null>(null);
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
  const [restoreProgress, setRestoreProgress] = useState<number | null>(null);
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
  const [planDetailsOpen, setPlanDetailsOpen] = useState(false);

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

  useEffect(
    () => onRestoreBusyChange(restoreBusy),
    [onRestoreBusyChange, restoreBusy],
  );

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
        const next = await api.importStatus(controller.signal);
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
                "服务器记录已经切换到另一项需要人工处理的恢复。本页不会把它归为本次提交；请核对两个操作记录。",
              );
            } else {
              setRestoreError(
                "服务器记录已经切换到另一项恢复。本页不会把它归为本次提交；正在跟随当前恢复。",
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
              "恢复操作记录已变化，无法证明本次提交的终态。不会重传文件；请人工核对两个操作记录。",
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
              "尚未观察到与本次提交关联的新恢复操作。不会重传文件；请人工核对服务器状态后再决定下一步。",
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
              "正在跟随服务器上的另一项恢复；它不会被归为本次提交。",
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
              "另一项恢复已经到达终态，但无法证明本次提交的结果。不会重传文件；请人工核对操作记录。",
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
                "服务器尚未记录与本次提交关联的恢复操作。不会重传文件；请人工核对状态。",
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
      planControllerRef.current?.abort();
      mergeControllerRef.current?.abort();
      restoreControllerRef.current?.abort();
      clearPoll();
      if (exportGuardTimerRef.current !== null) {
        window.clearTimeout(exportGuardTimerRef.current);
      }
      exportGuardRef.current = false;
      onRestoreBusyChange(false);
    };
  }, [clearPoll, onRestoreBusyChange, queryStatus]);

  useEffect(() => {
    if (!focusStatusRef.current || restoreState !== "running") return;
    const frame = window.requestAnimationFrame(() => {
      statusRef.current?.focus();
      focusStatusRef.current = false;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [restoreState]);

  const download = () => {
    if (!session || exportGuardRef.current) return;
    exportGuardRef.current = true;
    exportGuardTimerRef.current = window.setTimeout(() => {
      exportGuardRef.current = false;
      exportGuardTimerRef.current = null;
    }, 1_000);
    setExportState("running");
    setExportNotice("正在把同源备份请求交给浏览器…");
    try {
      const anchor = document.createElement("a");
      anchor.href = "/api/v1/owner/export";
      anchor.download = "moment-export.zip";
      anchor.hidden = true;
      document.body.append(anchor);
      anchor.click();
      anchor.remove();
      setExportState("complete");
      setExportNotice(
        "已请求下载。请在浏览器下载列表确认进度、完成状态与保存位置。",
      );
    } catch {
      setExportState("failed");
      setExportNotice("浏览器没有接受下载请求。页面状态未改变，请重试。");
    }
  };

  const chooseFile = (event: ChangeEvent<HTMLInputElement>) => {
    const next = event.currentTarget.files?.[0] ?? null;
    event.currentTarget.value = "";
    if (!next || restoreBusy) return;
    planControllerRef.current?.abort();
    mergeControllerRef.current?.abort();
    clearPoll();
    fileRevisionRef.current += 1;
    restoreAttemptedRef.current = false;
    unassociatedPollsRef.current = 0;
    foreignOperationRef.current = null;
    setFile(next);
    setPlan(null);
    setMergeInspection(null);
    setMergeSelection(null);
    setMergeState("idle");
    setMergeProgress(null);
    setMergeError(null);
    setPlanDetailsOpen(false);
    setPlanProgress(null);
    setRestoreProgress(null);
    setRestoreState("idle");
    setRestoreError(null);
    setStatus(null);
    setStatusDisconnected(false);
    setManualStatusRequired(false);
    if (next.size === 0) {
      setPlanState("failed");
      setPlanError("空文件不能作为备份。请选择 Moment 导出的 ZIP 文件。");
      window.requestAnimationFrame(() => pickerRef.current?.focus());
      return;
    }
    setPlanState("idle");
    setPlanError(null);
  };

  const inspect = async () => {
    if (!session || !file || planState === "running" || restoreBusy) return;
    planControllerRef.current?.abort();
    const controller = new AbortController();
    planControllerRef.current = controller;
    const revision = fileRevisionRef.current;
    setPlanState("running");
    setPlanProgress(null);
    setPlanError(null);
    setPlan(null);
    setPlanDetailsOpen(false);
    try {
      const next = await api.planImport(
        session.csrfToken,
        file,
        (progress) => {
          if (
            mountedRef.current &&
            planControllerRef.current === controller &&
            revision === fileRevisionRef.current
          ) {
            setPlanProgress(progress);
          }
        },
        controller.signal,
      );
      if (
        !mountedRef.current ||
        controller.signal.aborted ||
        planControllerRef.current !== controller ||
        revision !== fileRevisionRef.current
      ) {
        return;
      }
      setPlan(next);
      setPlanState("complete");
    } catch (error) {
      if (
        !mountedRef.current ||
        controller.signal.aborted ||
        revision !== fileRevisionRef.current
      ) {
        return;
      }
      expireIfInvalidSession(error);
      setPlanState("failed");
      setPlanError(importError(error, "plan"));
    }
  };

  const inspectMerge = async () => {
    if (!session || !file || mergeState === "running" || restoreBusy) return;
    mergeControllerRef.current?.abort();
    const controller = new AbortController();
    mergeControllerRef.current = controller;
    const revision = fileRevisionRef.current;
    setMergeState("running");
    setMergeProgress(null);
    setMergeError(null);
    setMergeInspection(null);
    setMergeSelection(null);
    try {
      const next = await api.planMergeImport(
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
      setMergeSelection(structuredClone(next.plan.default_selection));
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
        if (error.code === "IMPORT_OWNER_MISMATCH") {
          setMergeError("这份备份属于另一位所有者，不能与当前站点合并。");
        } else if (error.code === "IMPORT_MERGE_PLAN_STALE") {
          setMergeError("当前站点已发生变化，请重新检查合并冲突。");
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

  const cancelPlan = () => {
    planControllerRef.current?.abort();
    planControllerRef.current = null;
    mergeControllerRef.current?.abort();
    mergeControllerRef.current = null;
    fileRevisionRef.current += 1;
    restoreAttemptedRef.current = false;
    unassociatedPollsRef.current = 0;
    foreignOperationRef.current = null;
    setFile(null);
    setPlan(null);
    setMergeInspection(null);
    setMergeSelection(null);
    setMergeState("idle");
    setMergeProgress(null);
    setMergeError(null);
    setPlanDetailsOpen(false);
    setPlanState("idle");
    setPlanProgress(null);
    setPlanError(null);
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
        baseline = await api.importStatus(controller.signal);
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
        setRestoreError("服务器已有另一项导入正在进行，本次合并尚未提交。");
        if (activePhases.has(baseline.phase)) {
          void queryStatus(revision, baseline.operationId);
        }
        return;
      }
      restoreSentRef.current = true;
      const next = await api.commitMergeImport(
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
          setMergeInspection(null);
          setMergeSelection(null);
          setMergeState("failed");
          setMergeError("合并计划已过期或当前站点已变化，请重新检查合并冲突。");
        } else if (error.code === "IMPORT_OWNER_MISMATCH") {
          setMergeError("这份备份属于另一位所有者，不能与当前站点合并。");
        } else if (error.code === "IMPORT_IN_PROGRESS") {
          setMergeError(
            "另一项导入刚刚开始，本次合并没有提交。请稍后重新检查。",
          );
        } else {
          setMergeError(
            "服务器拒绝了当前冲突选择。合并没有开始，请重新检查后再提交。",
          );
        }
        return;
      }
      if (!restoreSentRef.current) {
        restoreAttemptedRef.current = false;
        expireIfInvalidSession(error);
        setRestoreState("failed");
        setRestoreError("提交前无法确认服务器状态，合并尚未开始。");
        setStatusDisconnected(true);
        return;
      }
      expireIfInvalidSession(error);
      setRestoreState("running");
      setRestoreError(
        "合并结果尚未确认。不会重复提交；正在查询服务器保存的导入状态。",
      );
      void queryStatus(revision, null, restoreBaselineRef.current);
    }
  };

  const restore = async () => {
    if (
      !session ||
      !file ||
      !plan ||
      !plan.disk.spaceOK ||
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
    setRestoreProgress(null);
    setRestoreError(null);
    setManualStatusRequired(false);
    setStatus(null);
    try {
      let baseline: ImportStatus | null = null;
      try {
        baseline = await api.importStatus(controller.signal);
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
            ? "服务器仍有需要人工处理的恢复状态，本文件尚未上传。先完成主机侧恢复处置。"
            : "检测到另一项恢复正在进行，本文件尚未上传。正在跟随当前恢复状态。",
        );
        if (activePhases.has(baseline.phase)) {
          void queryStatus(revision, baseline.operationId);
        }
        return;
      }
      restoreSentRef.current = true;
      const next = await api.restoreImport(
        session.csrfToken,
        file,
        plan.archiveSHA256,
        (progress) => {
          if (
            mountedRef.current &&
            restoreControllerRef.current === controller &&
            revision === fileRevisionRef.current
          ) {
            setRestoreProgress(progress);
          }
        },
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
      setRestoreState("complete");
      setSiteRefreshRequired(true);
    } catch (error) {
      if (!mountedRef.current || revision !== fileRevisionRef.current) return;
      if (!restoreSentRef.current) {
        restoreAttemptedRef.current = false;
        expireIfInvalidSession(error);
        setRestoreState("failed");
        setRestoreError(
          "提交前无法确认服务器当前恢复状态，备份尚未上传。连接恢复后可再次确认。",
        );
        setStatusDisconnected(true);
        return;
      }
      expireIfInvalidSession(error);
      setRestoreState("running");
      setRestoreError(
        "恢复结果尚未确认。不会重新提交文件；正在查询服务器保存的恢复状态。",
      );
      void queryStatus(revision, null, restoreBaselineRef.current);
    }
  };

  const stopWaiting = () => {
    restoreControllerRef.current?.abort();
    if (!restoreSentRef.current) {
      setRestoreError("已停止提交前状态检查，备份尚未上传。");
      return;
    }
    setRestoreError(
      "已停止等待原请求。恢复可能仍在服务器执行；不会重传文件，正在查询状态。",
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
  const mergeItemConflicts =
    mergeInspection?.plan.items.filter(
      (item) => item.classification === "conflict" && item.selectable,
    ) ?? [];
  const mergeSiteConflicts =
    mergeInspection?.plan.site_fields.filter(
      (field) => field.classification === "conflict",
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
      <p>{currentStatus?.eyebrow ?? "恢复状态"}</p>
      <h3>{currentStatus?.title ?? "正在确认服务器状态"}</h3>
      <span>
        {currentStatus?.detail ??
          "恢复请求可能仍在运行。本页不会重新提交备份，只会查询服务器记录。"}
      </span>
      {restoreState === "running" && !status ? (
        <progress
          aria-label="备份恢复上传进度"
          max="100"
          value={restoreProgress ?? undefined}
        />
      ) : null}
      {restoreError ? <p className={styles.dataError}>{restoreError}</p> : null}
      {statusDisconnected ? (
        <p className={styles.dataError}>
          暂时无法连接服务器。页面会继续重试状态查询，不会重传备份。
        </p>
      ) : null}
      {manualStatusRequired ? (
        <p className={styles.dataError}>
          自动关联已经停止。先在实例主机核对恢复状态；本页不会自行再次提交备份。
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
          <summary>需要核对的技术信息</summary>
          <dl className={styles.operatorFacts}>
            <div>
              <dt>操作</dt>
              <dd>{status.operationId}</dd>
            </div>
            <div>
              <dt>错误码</dt>
              <dd>{status.errorCode}</dd>
            </div>
            <div>
              <dt>当前数据版本</dt>
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
            停止等待并查询状态
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
            立即重试状态
          </button>
        ) : null}
        {status?.phase === "succeeded" ? (
          <button
            className={styles.primaryButton}
            onClick={() => window.location.reload()}
            type="button"
          >
            重新载入已恢复站点
          </button>
        ) : null}
      </div>
    </div>
  ) : (
    <div aria-live="polite" className={styles.backupOverview}>
      <p>最近状态</p>
      <h3>{statusChecking ? "正在确认恢复状态" : "当前没有进行中的恢复"}</h3>
      <span>
        {statusChecking
          ? "确认完成前，站点设置与恢复入口会保持只读。"
          : "可以下载当前站点的完整备份，或选择备份文件进行安全预检。"}
      </span>
    </div>
  );

  return (
    <div className={styles.portableFlow}>
      {restoreStatusPanel}
      <div className={styles.portableStep}>
        <div>
          <h3>导出完整站点</h3>
          <p>包含资料、Moment、回复和图片，不包含登录密钥与浏览器草稿。</p>
          <button
            aria-busy={exportState === "running"}
            className={styles.secondaryButton}
            disabled={!session || exportState === "running" || exportBlocked}
            onClick={download}
            type="button"
          >
            {exportState === "running" ? "正在请求下载…" : "下载整站备份"}
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
          <h3>检查备份</h3>
          <p>选择文件会先检查内容，不会立即替换当前网站。</p>
          <div className={styles.inlineActions}>
            <button
              className={styles.secondaryButton}
              disabled={
                !session ||
                !statusInitialized ||
                planState === "running" ||
                restoreBusy
              }
              onClick={() => fileInputRef.current?.click()}
              ref={pickerRef}
              type="button"
            >
              {file ? "更换备份文件" : "选择备份文件"}
            </button>
            {file && planState !== "running" ? (
              <button
                className={styles.primaryButton}
                disabled={
                  !statusInitialized || mergeState === "running" || restoreBusy
                }
                onClick={() => void inspectMerge()}
                type="button"
              >
                {mergeInspection ? "重新检查合并" : "检查并合并"}
              </button>
            ) : null}
            {file && planState !== "running" ? (
              <button
                className={styles.secondaryButton}
                disabled={
                  !statusInitialized || mergeState === "running" || restoreBusy
                }
                onClick={() => void inspect()}
                type="button"
              >
                {plan ? "重新预检" : "开始预检"}
              </button>
            ) : null}
            {file ? (
              <button
                className={styles.quietButton}
                disabled={restoreBusy}
                onClick={cancelPlan}
                type="button"
              >
                {planState === "running" ? "取消预检" : "清除文件"}
              </button>
            ) : null}
          </div>
          <input
            accept=".zip,application/zip"
            aria-label="选择 Moment 备份 ZIP"
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
          {planState === "running" ? (
            <>
              <progress
                aria-label="备份预检上传进度"
                max="100"
                value={planProgress ?? undefined}
              />
              <p aria-live="polite" className={styles.dataNotice}>
                {planProgress === null
                  ? "正在上传并检查…"
                  : planProgress < 100
                    ? `正在上传并检查… ${planProgress}%`
                    : "上传完成，正在检查内容与所需空间…"}
              </p>
            </>
          ) : null}
          {mergeState === "running" ? (
            <>
              <progress
                aria-label="合并检查上传进度"
                max="100"
                value={mergeProgress ?? undefined}
              />
              <p aria-live="polite" className={styles.dataNotice}>
                {mergeProgress === null
                  ? "正在上传并比较当前站点…"
                  : mergeProgress < 100
                    ? `正在上传并比较当前站点… ${mergeProgress}%`
                    : "上传完成，正在分类新增内容与冲突…"}
              </p>
            </>
          ) : null}
          {planError ? (
            <p className={styles.dataError} role="alert">
              {planError}
            </p>
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
              <p>合并检查完成</p>
              <h3>选择冲突处理方式</h3>
            </div>
            <span>
              {new Intl.DateTimeFormat("zh-CN", {
                hour: "2-digit",
                minute: "2-digit",
              }).format(new Date(mergeInspection.expiresAt))}
              前提交
            </span>
          </header>
          <p className={styles.mergeIntroduction}>
            备份中新增的内容会加入站点；仅存在于当前站点的内容会保留。没有删除标记时，缺失内容不会被当成删除。
          </p>
          <div aria-label="合并分类摘要" className={styles.mergeSummary}>
            <span>
              <strong>{mergeInspection.plan.summary.incoming_only}</strong>
              新增
            </span>
            <span>
              <strong>{mergeInspection.plan.summary.local_only}</strong>
              仅本地，保留
            </span>
            <span>
              <strong>{mergeInspection.plan.summary.unchanged}</strong>
              相同
            </span>
            <span
              data-tone={
                mergeInspection.plan.summary.conflict ? "warning" : "success"
              }
            >
              <strong>{mergeInspection.plan.summary.conflict}</strong>
              冲突
            </span>
          </div>

          {mergeItemConflicts.length ? (
            <section
              aria-labelledby="content-conflicts"
              className={styles.mergeConflictSection}
            >
              <h4 id="content-conflicts">内容冲突</h4>
              <div className={styles.mergeConflictList}>
                {mergeItemConflicts.slice(0, 200).map((item) => {
                  const decision = mergeSelection.items.find(
                    (candidate) =>
                      candidate.kind === item.kind && candidate.id === item.id,
                  );
                  return (
                    <label
                      className={styles.mergeConflict}
                      key={`${item.kind}:${item.id}`}
                    >
                      <span>
                        <strong>{mergeKindLabel(item.kind)}</strong>
                        <code>{item.id}</code>
                        <small>
                          变更：
                          {item.changed_fields
                            .map(mergeChangedFieldLabel)
                            .join("、") || "内容"}
                        </small>
                      </span>
                      <select
                        aria-label={`${mergeKindLabel(item.kind)} ${item.id} 的冲突处理`}
                        onChange={(event) =>
                          chooseMergeItem(
                            item.kind,
                            item.id,
                            event.currentTarget.value as MergeAction,
                          )
                        }
                        value={decision?.action ?? "keep_local"}
                      >
                        <option value="keep_local">保留当前版本</option>
                        <option value="use_incoming">使用备份版本</option>
                        <option value="keep_both">两者都保留</option>
                      </select>
                    </label>
                  );
                })}
              </div>
              {mergeItemConflicts.length > 200 ? (
                <p className={styles.dataNotice}>
                  当前显示前 200
                  项；其余冲突保持“保留当前版本”。如需逐项调整全部冲突，请使用
                  CLI 导出的选择文件。
                </p>
              ) : null}
            </section>
          ) : null}

          {mergeSiteConflicts.length ? (
            <section
              aria-labelledby="profile-conflicts"
              className={styles.mergeConflictSection}
            >
              <h4 id="profile-conflicts">资料与站点设置冲突</h4>
              <div className={styles.mergeConflictList}>
                {mergeSiteConflicts.map((field) => {
                  const decision = mergeSelection.site_fields.find(
                    (candidate) => candidate.field === field.field,
                  );
                  return (
                    <label className={styles.mergeConflict} key={field.field}>
                      <span>
                        <strong>{mergeSiteFieldLabel(field.field)}</strong>
                        <small>当前：{mergeValue(field.local_value)}</small>
                        <small>备份：{mergeValue(field.incoming_value)}</small>
                      </span>
                      <select
                        aria-label={`${mergeSiteFieldLabel(field.field)}的冲突处理`}
                        onChange={(event) =>
                          chooseMergeSiteField(
                            field.field,
                            event.currentTarget.value as MergeAction,
                          )
                        }
                        value={decision?.action ?? "keep_local"}
                      >
                        <option value="keep_local">保留当前值</option>
                        <option value="use_incoming">使用备份值</option>
                      </select>
                    </label>
                  );
                })}
              </div>
            </section>
          ) : null}

          {!mergeItemConflicts.length && !mergeSiteConflicts.length ? (
            <p className={styles.mergeClear}>
              没有需要手动处理的冲突，可以直接合并。
            </p>
          ) : null}
          {mergeInspection.warnings.length ? (
            <div className={styles.importWarnings}>
              <strong>合并提醒</strong>
              <ul>
                {mergeInspection.warnings.map((warning) => (
                  <li key={warning}>{warning}</li>
                ))}
              </ul>
            </div>
          ) : null}
          {settingsBlocked ? (
            <p className={styles.dataError} role="alert">
              先保存或放弃当前设置与头像更改，才能提交合并。
            </p>
          ) : null}
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
              render={<button className={styles.primaryButton} type="button" />}
            >
              提交合并
            </Dialog.Trigger>
            <Dialog.Portal>
              <Dialog.Backdrop className={styles.dialogBackdrop} />
              <Dialog.Viewport className={styles.dialogViewport}>
                <Dialog.Popup className={styles.restoreDialog}>
                  <Dialog.Title>按当前选择合并这份备份？</Dialog.Title>
                  <Dialog.Description>
                    将加入 {mergeInspection.incomingCounts.entries} 条备份
                    Moment 中可合并的内容，并按你选择的方式处理{" "}
                    {mergeInspection.plan.summary.conflict} 项冲突。
                  </Dialog.Description>
                  <div className={styles.dialogImpact}>
                    <div>
                      <span>新增</span>
                      <strong>
                        {mergeInspection.plan.summary.incoming_only} 项
                      </strong>
                    </div>
                    <div>
                      <span>仅本地</span>
                      <strong>
                        {mergeInspection.plan.summary.local_only} 项，全部保留
                      </strong>
                    </div>
                    <div>
                      <span>冲突</span>
                      <strong>
                        {mergeInspection.plan.summary.conflict} 项，按选择处理
                      </strong>
                    </div>
                  </div>
                  <p className={styles.dialogRisk}>
                    <strong>合并前仍会自动备份当前站点。</strong>
                    计划已绑定当前数据版本；若站点已变化，服务器会拒绝旧计划并要求重新检查。
                  </p>
                  <div className={styles.dialogActions}>
                    <Dialog.Close className={styles.secondaryButton}>
                      返回检查
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
                      确认并合并
                    </button>
                  </div>
                </Dialog.Popup>
              </Dialog.Viewport>
            </Dialog.Portal>
          </Dialog.Root>
        </div>
      ) : null}

      {plan ? (
        <div className={styles.restorePreview}>
          <header>
            <div>
              <p>备份检查完成</p>
              <h3>恢复前影响清单</h3>
            </div>
            <span>{plan.disk.spaceOK ? "空间检查通过" : "空间不足"}</span>
          </header>
          <div aria-label="恢复决定摘要" className={styles.decisionSummary}>
            <section>
              <span>将导入</span>
              <strong>{plan.counts.entries} 条 Moment</strong>
              <small>
                {plan.counts.comments} 条评论 · {plan.counts.media} 个媒体记录
              </small>
            </section>
            <section data-tone="danger">
              <span>将被替换</span>
              <strong>{plan.replacement.counts.entries} 条 Moment</strong>
              <small>
                {plan.replacement.counts.comments} 条评论 ·{" "}
                {plan.replacement.counts.media} 个媒体记录
              </small>
            </section>
            <section data-tone={plan.disk.spaceOK ? "success" : "danger"}>
              <span>容量检查</span>
              <strong>{plan.disk.spaceOK ? "通过" : "空间不足"}</strong>
              <small>
                还需 {formatBytes(plan.disk.requiredAdditionalBytes)} · 可用{" "}
                {formatBytes(plan.disk.availableBytes)}
              </small>
            </section>
          </div>
          {!plan.disk.spaceOK ? (
            <p className={styles.dataError} role="alert">
              当前空间不足，不能提交恢复。完整容量账本保持展开，释放空间后请重新预检。
            </p>
          ) : null}
          <details
            className={styles.planDetails}
            data-locked={!plan.disk.spaceOK ? "true" : undefined}
            onToggle={(event) => {
              if (!plan.disk.spaceOK && !event.currentTarget.open) {
                event.currentTarget.open = true;
                return;
              }
              setPlanDetailsOpen(event.currentTarget.open);
            }}
            open={planDetailsOpen || !plan.disk.spaceOK}
          >
            <summary>查看完整替换、容量与校验账本</summary>
            <div className={styles.planDetailsBody}>
              <p className={styles.archiveHash}>
                格式 v{plan.formatVersion} · SHA-256{" "}
                <code>{plan.archiveSHA256}</code>
              </p>
              <div className={styles.impactColumns}>
                <section aria-labelledby="incoming-impact">
                  <h4 id="incoming-impact">将导入</h4>
                  <dl>
                    <div>
                      <dt>Moment</dt>
                      <dd>{plan.counts.entries}</dd>
                    </div>
                    <div>
                      <dt>评论</dt>
                      <dd>{plan.counts.comments}</dd>
                    </div>
                    <div>
                      <dt>媒体</dt>
                      <dd>
                        {plan.counts.media} · {formatBytes(plan.mediaBytes)}
                      </dd>
                    </div>
                    <div>
                      <dt>时间范围</dt>
                      <dd>
                        {formatDate(plan.timeRange.from)} —{" "}
                        {formatDate(plan.timeRange.to)}
                      </dd>
                    </div>
                    <div>
                      <dt>缺少图片描述</dt>
                      <dd>{plan.missingAlt}</dd>
                    </div>
                  </dl>
                </section>
                <section aria-labelledby="replacement-impact">
                  <h4 id="replacement-impact">将被替换</h4>
                  <dl>
                    <div>
                      <dt>Moment</dt>
                      <dd>{plan.replacement.counts.entries}</dd>
                    </div>
                    <div>
                      <dt>评论</dt>
                      <dd>{plan.replacement.counts.comments}</dd>
                    </div>
                    <div>
                      <dt>媒体</dt>
                      <dd>
                        {plan.replacement.counts.media} ·{" "}
                        {formatBytes(plan.replacement.mediaBytes)}
                      </dd>
                    </div>
                    <div>
                      <dt>当前数据</dt>
                      <dd>{formatBytes(plan.replacement.dataBytes)}</dd>
                    </div>
                  </dl>
                </section>
              </div>
              <section
                aria-labelledby="disk-impact"
                className={styles.diskLedger}
              >
                <h4 id="disk-impact">恢复所需空间</h4>
                <dl>
                  <div>
                    <dt>上传包</dt>
                    <dd>{formatBytes(plan.disk.archiveBytes)}</dd>
                  </div>
                  <div>
                    <dt>展开内容</dt>
                    <dd>{formatBytes(plan.disk.expandedBytes)}</dd>
                  </div>
                  <div>
                    <dt>暂存重建</dt>
                    <dd>{formatBytes(plan.disk.stagingBytes)}</dd>
                  </div>
                  <div>
                    <dt>自动恢复前备份</dt>
                    <dd>{formatBytes(plan.disk.preImportBackupBytes)}</dd>
                  </div>
                  <div>
                    <dt>回滚保留</dt>
                    <dd>{formatBytes(plan.disk.rollbackReserveBytes)}</dd>
                  </div>
                  <div>
                    <dt>安全余量</dt>
                    <dd>{formatBytes(plan.disk.safetyMarginBytes)}</dd>
                  </div>
                  <div>
                    <dt>还需空间</dt>
                    <dd>{formatBytes(plan.disk.requiredAdditionalBytes)}</dd>
                  </div>
                  <div>
                    <dt>预计峰值</dt>
                    <dd>
                      {formatBytes(plan.disk.estimatedPeakFootprintBytes)}
                    </dd>
                  </div>
                  <div>
                    <dt>当前可用</dt>
                    <dd>{formatBytes(plan.disk.availableBytes)}</dd>
                  </div>
                  <div>
                    <dt>空间检查</dt>
                    <dd>{plan.disk.spaceOK ? "通过" : "不足"}</dd>
                  </div>
                </dl>
              </section>
            </div>
          </details>
          {plan.warnings.length ? (
            <div className={styles.importWarnings}>
              <strong>预检提醒</strong>
              <ul>
                {plan.warnings.map((warning) => (
                  <li key={warning}>{warning}</li>
                ))}
              </ul>
            </div>
          ) : null}
          <div className={styles.backupPromise}>
            <strong>提交前会自动备份当前数据</strong>
            <p>
              自动备份验证失败时不会替换当前站点；恢复后的数据也必须通过检查。
            </p>
          </div>
          {settingsBlocked ? (
            <p className={styles.dataError} role="alert">
              先保存或放弃当前站点设置与头像更改，才能开始恢复。预检结果仍保留在本页。
            </p>
          ) : null}
          <Dialog.Root onOpenChange={setConfirmOpen} open={confirmOpen}>
            <Dialog.Trigger
              className={styles.dialogTrigger}
              disabled={
                !session ||
                !plan.disk.spaceOK ||
                settingsBlocked ||
                !statusInitialized ||
                statusDisconnected ||
                restoreAttemptedRef.current ||
                restoreBusy
              }
              render={<button className={styles.dangerButton} type="button" />}
            >
              确认替换当前站点
            </Dialog.Trigger>
            <Dialog.Portal>
              <Dialog.Backdrop className={styles.dialogBackdrop} />
              <Dialog.Viewport className={styles.dialogViewport}>
                <Dialog.Popup className={styles.restoreDialog}>
                  <Dialog.Title>用这份备份替换当前站点？</Dialog.Title>
                  <Dialog.Description>
                    将以 {plan.counts.entries} 条 Moment、{plan.counts.comments}{" "}
                    条评论和 {plan.counts.media} 个媒体记录，替换当前的{" "}
                    {plan.replacement.counts.entries} 条 Moment、
                    {plan.replacement.counts.comments} 条评论和{" "}
                    {plan.replacement.counts.media}{" "}
                    个媒体记录。服务器会先自动备份当前数据。
                  </Dialog.Description>
                  <div
                    aria-label="最终恢复影响"
                    className={styles.dialogImpact}
                  >
                    <div>
                      <span>导入</span>
                      <strong>
                        {plan.counts.entries} Moment · {plan.counts.comments}{" "}
                        评论 · {plan.counts.media} 媒体
                      </strong>
                    </div>
                    <div>
                      <span>替换</span>
                      <strong>
                        {plan.replacement.counts.entries} Moment ·{" "}
                        {plan.replacement.counts.comments} 评论 ·{" "}
                        {plan.replacement.counts.media} 媒体
                      </strong>
                    </div>
                    <div>
                      <span>容量</span>
                      <strong>
                        已通过 · 还需{" "}
                        {formatBytes(plan.disk.requiredAdditionalBytes)} · 可用{" "}
                        {formatBytes(plan.disk.availableBytes)}
                      </strong>
                    </div>
                  </div>
                  <p className={styles.dialogRisk}>
                    <strong>这是覆盖恢复，不会合并数据。</strong>
                    确认后服务器仍会先备份当前站点；备份或恢复后验证失败都不会报告成功。
                  </p>
                  <div className={styles.dialogActions}>
                    <Dialog.Close className={styles.secondaryButton}>
                      返回检查
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
                      确认并开始恢复
                    </button>
                  </div>
                </Dialog.Popup>
              </Dialog.Viewport>
            </Dialog.Portal>
          </Dialog.Root>
        </div>
      ) : null}
    </div>
  );
}
