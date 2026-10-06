import { Toast } from "@base-ui/react/toast";
import type { ReactNode } from "react";

import { Icon } from "./icons";
import { CodeCopyFeedback } from "./content/CodeCopyFeedback";
import styles from "./Feedback.module.css";

export const feedback = Toast.createToastManager();

const SUCCESS_TIMEOUT = 4_000;
const EXPLANATORY_TIMEOUT = 6_000;

interface FeedbackAction {
  label: string;
  onAction: () => void;
}

interface FeedbackOptions {
  description?: string;
  type?: "success" | "error";
  action?: FeedbackAction;
}

export function notify(title: string, options: FeedbackOptions = {}) {
  const type = options.type ?? "success";
  let id = "";
  id = feedback.add({
    title,
    description: options.description,
    type,
    priority: type === "error" ? "high" : "low",
    timeout:
      type === "error" || options.description
        ? EXPLANATORY_TIMEOUT
        : SUCCESS_TIMEOUT,
    actionProps: options.action
      ? {
          children: options.action.label,
          onClick: () => {
            feedback.close(id);
            options.action?.onAction();
          },
        }
      : undefined,
  });
  return id;
}

function FeedbackList() {
  const { toasts } = Toast.useToastManager();
  return toasts.map((toast) => (
    <Toast.Root className={styles.toast} key={toast.id} toast={toast}>
      <Toast.Content className={styles.content}>
        <div className={styles.message}>
          <Toast.Title className={styles.title} />
          {toast.description ? (
            <Toast.Description className={styles.description} />
          ) : null}
        </div>
        <div className={styles.actions}>
          {/* The action is a secondary offer, not the point of the message, so
              it reads as a link rather than competing with a filled button. */}
          <Toast.Action className={styles.action} />
          <button
            aria-label="关闭通知"
            className={styles.close}
            onClick={() => feedback.close(toast.id)}
            type="button"
          >
            <Icon name="close" />
          </button>
        </div>
      </Toast.Content>
    </Toast.Root>
  ));
}

export function FeedbackProvider({ children }: { children: ReactNode }) {
  return (
    <Toast.Provider limit={3} timeout={SUCCESS_TIMEOUT} toastManager={feedback}>
      {children}
      <CodeCopyFeedback />
      <Toast.Portal>
        <Toast.Viewport className={styles.viewport}>
          <FeedbackList />
        </Toast.Viewport>
      </Toast.Portal>
    </Toast.Provider>
  );
}
