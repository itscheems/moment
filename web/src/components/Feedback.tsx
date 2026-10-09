import { Toast } from "@base-ui/react/toast";
import {
  type CSSProperties,
  type ReactNode,
  useLayoutEffect,
  useState,
} from "react";

import { Icon } from "./icons";
import { LoginToastGlass } from "./LoginToastGlass";
import { CodeCopyFeedback } from "./content/CodeCopyFeedback";
import styles from "./Feedback.module.css";

interface FeedbackData {
  placement?: "login";
}

export const feedback = Toast.createToastManager<FeedbackData>();

const SUCCESS_TIMEOUT = 4_000;
const EXPLANATORY_TIMEOUT = 6_000;
const LOGIN_TIMEOUT = 3_000;

interface FeedbackAction {
  label: string;
  onAction: () => void;
}

interface FeedbackOptions {
  id?: string;
  placement?: "login";
  description?: string;
  type?: "success" | "error";
  action?: FeedbackAction;
}

export function notify(title: string, options: FeedbackOptions = {}) {
  const type = options.type ?? "success";
  let id = "";
  id = feedback.add({
    id: options.id,
    title,
    description: options.description,
    type,
    priority: type === "error" ? "high" : "low",
    data: { placement: options.placement },
    timeout:
      options.placement === "login"
        ? LOGIN_TIMEOUT
        : type === "error" || options.description
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

function useLoginViewportStyle(active: boolean): CSSProperties | undefined {
  const [bounds, setBounds] = useState({ top: 0, right: 0, width: 0 });
  useLayoutEffect(() => {
    if (!active) return;
    const viewport = window.visualViewport;
    const update = () => {
      const width = viewport?.width ?? window.innerWidth;
      setBounds({
        top: viewport?.offsetTop ?? 0,
        right: Math.max(
          0,
          window.innerWidth - (viewport?.offsetLeft ?? 0) - width,
        ),
        width,
      });
    };
    update();
    viewport?.addEventListener("resize", update);
    viewport?.addEventListener("scroll", update);
    window.addEventListener("resize", update);
    return () => {
      viewport?.removeEventListener("resize", update);
      viewport?.removeEventListener("scroll", update);
      window.removeEventListener("resize", update);
    };
  }, [active]);
  if (!active || !bounds.width) return undefined;
  return {
    "--feedback-visual-top": `${bounds.top}px`,
    "--feedback-visual-right": `${bounds.right}px`,
    "--feedback-visual-width": `${bounds.width}px`,
  } as CSSProperties;
}

function FeedbackViewport() {
  const { toasts } = Toast.useToastManager<FeedbackData>();
  // Retain placement through the closing animation, including after typing
  // closes the message, so it cannot jump back to the ordinary bottom stack.
  const loginPlacement = toasts.some(
    (toast) => toast.data?.placement === "login",
  );
  const viewportStyle = useLoginViewportStyle(loginPlacement);
  return (
    <Toast.Viewport
      className={`${styles.viewport} ${loginPlacement ? styles.loginViewport : ""}`}
      style={viewportStyle}
    >
      {toasts.map((toast) => (
        <Toast.Root
          className={`${styles.toast} ${toast.data?.placement === "login" ? styles.loginToast : ""}`}
          key={toast.id}
          toast={toast}
        >
          {toast.data?.placement === "login" ? <LoginToastGlass /> : null}
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
      ))}
    </Toast.Viewport>
  );
}

export function FeedbackProvider({ children }: { children: ReactNode }) {
  return (
    <Toast.Provider limit={3} timeout={SUCCESS_TIMEOUT} toastManager={feedback}>
      {children}
      <CodeCopyFeedback />
      <Toast.Portal>
        <FeedbackViewport />
      </Toast.Portal>
    </Toast.Provider>
  );
}
