import { useTranslation } from "react-i18next";
import styles from "../Editor.module.css";

export function ConflictNotice({
  busy,
  onKeepMine,
  onTakeServer,
}: {
  busy: boolean;
  onKeepMine: () => void;
  onTakeServer: () => void;
}) {
  const { t } = useTranslation();

  return (
    <div className={styles.conflict} role="alert">
      <p className={styles.conflictTitle}>{t("这份草稿在别处改过了")}</p>
      <p className={styles.conflictBody}>
        {t(
          "自动保存已经停下，不会覆盖别处的改动。可以用这里的版本盖过去，也可以放弃这里的改动、读回服务器上的那一份。",
        )}
      </p>
      <div className={styles.conflictActions}>
        <button
          className={styles.primaryAction}
          disabled={busy}
          onClick={onKeepMine}
          type="button"
        >
          {t("用这里的版本覆盖")}
        </button>
        <button
          className={styles.secondaryAction}
          disabled={busy}
          onClick={onTakeServer}
          type="button"
        >
          {t("读回服务器上的版本")}
        </button>
      </div>
    </div>
  );
}
