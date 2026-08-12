import { Toast } from "@base-ui/react/toast";
import type { ReactNode } from "react";

import styles from "./Feedback.module.css";

export const feedback = Toast.createToastManager();

export function notify(
  title: string,
  options: { description?: string; type?: "success" | "error" } = {},
) {
  feedback.add({
    title,
    description: options.description,
    type: options.type ?? "success",
    priority: options.type === "error" ? "high" : "low",
    timeout: 2_600,
  });
}

function FeedbackList() {
  const { toasts } = Toast.useToastManager();
  return toasts.map((toast) => (
    <Toast.Root className={styles.toast} key={toast.id} toast={toast}>
      <Toast.Content>
        <Toast.Title className={styles.title} />
        <Toast.Description className={styles.description} />
      </Toast.Content>
    </Toast.Root>
  ));
}

export function FeedbackProvider({ children }: { children: ReactNode }) {
  return (
    <Toast.Provider limit={3} timeout={2_600} toastManager={feedback}>
      {children}
      <Toast.Portal>
        <Toast.Viewport className={styles.viewport}>
          <FeedbackList />
        </Toast.Viewport>
      </Toast.Portal>
    </Toast.Provider>
  );
}
