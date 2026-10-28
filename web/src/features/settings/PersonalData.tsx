import { useEffect, useRef, useState } from "react";
import { personalDataApi, type PersonalImport } from "../../api/personalData";
import { PublicApiError } from "../../api/public";
import { useSession } from "../auth/SessionProvider";
import styles from "./Settings.module.css";

function importError(error: unknown, committing: boolean): string {
  if (error instanceof PublicApiError) {
    if (error.code === "EXPORT_VERSION_UNSUPPORTED")
      return "这个个人导出文件不支持恢复。请使用当前版本重新导出。";
    if (error.code === "IMPORT_DISK_SPACE_INSUFFICIENT")
      return "服务器空间不足。请释放空间后重新检查文件。";
    if (
      ["EXPORT_INVALID", "EXPORT_CHECKSUM_MISMATCH", "IMPORT_INVALID"].includes(
        error.code,
      )
    )
      return "文件不完整或校验失败。请重新导出并选择原始 ZIP 文件。";
  }
  return committing
    ? "未能确认导入结果。可以重新检查并导入，相同内容不会重复添加，已有内容会保留。"
    : "文件检查未完成，现有内容没有改变。请重新选择文件或重试。";
}

export function PersonalData() {
  const { account, session, expireIfInvalidSession } = useSession();
  const [file, setFile] = useState<File | null>(null);
  const [plan, setPlan] = useState<PersonalImport | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const operation = useRef<AbortController | null>(null);
  const checkButton = useRef<HTMLButtonElement>(null);
  useEffect(() => () => operation.current?.abort(), []);
  async function run(commit: boolean) {
    if (!file || !session || !account || busy || (commit && !plan)) return;
    const controller = new AbortController();
    operation.current = controller;
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const result = await personalDataApi.import(
        file,
        session.csrfToken,
        commit ? (plan?.archiveSHA256 ?? null) : null,
        controller.signal,
      );
      if (controller.signal.aborted) return;
      if (result.accountId !== account.userId)
        throw new Error("personal import selected a different account");
      if (commit) {
        setPlan(null);
        setNotice(
          `已添加 ${result.added.entries} 条内容、${result.added.comments} 条评论和 ${result.added.article_drafts} 篇草稿。已有内容已保留，重复内容已跳过。`,
        );
      } else setPlan(result);
    } catch (next) {
      if (!controller.signal.aborted && !expireIfInvalidSession(next))
        setError(importError(next, commit));
    } finally {
      if (operation.current === controller) {
        operation.current = null;
        setBusy(false);
        if (commit || controller.signal.aborted)
          requestAnimationFrame(() => checkButton.current?.focus());
      }
    }
  }
  function download() {
    setNotice(null);
    setError(null);
    const link = document.createElement("a");
    link.href = "/api/v1/account/export";
    link.download = "moment-account-export.zip";
    document.body.append(link);
    link.click();
    link.remove();
    setNotice("已请求个人数据下载。请在浏览器下载列表确认完成状态。");
  }
  return (
    <section aria-label="个人数据" aria-busy={busy}>
      <header className={styles.sectionHeader}>
        <h2>个人数据</h2>
      </header>
      <div className={styles.portableStep}>
        <div>
          <h3>导出我的数据</h3>
          <p>
            保存你的内容、文章草稿，以及这些内容下的评论、图片和评论人资料。引用的他人内容不会包含在文件中。
          </p>
          <button
            className={styles.secondaryButton}
            type="button"
            onClick={download}
            disabled={!session || busy}
          >
            下载个人数据
          </button>
        </div>
      </div>
      <div className={styles.portableStep}>
        <div>
          <h3>导入个人数据</h3>
          <p>
            添加到当前账号，保留现有内容、账号资料和登录方式。相同来源的内容再次导入时会跳过。
          </p>
          <label>
            选择个人导出文件{" "}
            <input
              aria-label="个人导出文件"
              type="file"
              accept=".zip,application/zip"
              disabled={busy || account?.status !== "active"}
              onChange={(event) => {
                setFile(event.currentTarget.files?.[0] ?? null);
                setPlan(null);
                setError(null);
                setNotice(null);
              }}
            />
          </label>
          {account?.status !== "active" ? (
            <p>账号启用后可以导入个人数据。</p>
          ) : null}
          <button
            className={styles.secondaryButton}
            type="button"
            disabled={!file || busy || account?.status !== "active"}
            ref={checkButton}
            onClick={() => void run(false)}
          >
            {busy ? "正在处理…" : "检查文件"}
          </button>
          {plan ? (
            <div role="region" aria-label="个人导入预览">
              <p>
                将读取 {plan.counts.entries} 条内容（含 {plan.counts.articles}{" "}
                篇文章）、{plan.counts.comments} 条评论、
                {plan.counts.article_drafts} 篇草稿和 {plan.counts.media}{" "}
                个媒体文件。
              </p>
              <p>当前站点已有的内容不会被删除或覆盖。</p>
              <button
                className={styles.secondaryButton}
                type="button"
                disabled={busy}
                onClick={() => void run(true)}
              >
                确认添加到当前账号
              </button>
            </div>
          ) : null}
          {busy ? (
            <button
              className={styles.secondaryButton}
              type="button"
              onClick={() => {
                operation.current?.abort();
                setNotice(
                  "已取消等待。可重新检查并导入，相同内容不会重复添加。",
                );
              }}
            >
              取消等待
            </button>
          ) : null}
        </div>
      </div>
      {error ? (
        <p className={styles.dataError} role="alert">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className={styles.dataNotice} role="status">
          {notice}
        </p>
      ) : null}
    </section>
  );
}
