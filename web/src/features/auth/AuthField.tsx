import type { InputHTMLAttributes, ReactNode } from "react";
import { Icon } from "../../components/icons";
import styles from "./AuthField.module.css";

export function AuthField({
  label,
  error,
  end,
  ...input
}: InputHTMLAttributes<HTMLInputElement> & {
  label: string;
  error?: boolean;
  end?: ReactNode;
}) {
  const invalid =
    error === true ||
    input["aria-invalid"] === true ||
    input["aria-invalid"] === "true";
  const filled = String(input.value ?? input.defaultValue ?? "").length > 0;
  return (
    <div
      className={styles.field}
      data-has-value={filled || undefined}
      data-invalid={invalid || undefined}
      data-has-end={!!end || undefined}
    >
      <input
        {...input}
        className={styles.input}
        placeholder={input.placeholder ?? label}
        aria-invalid={invalid || undefined}
      />
      <label className={styles.label} htmlFor={input.id}>
        {label}
      </label>
      {end && <span className={styles.end}>{end}</span>}
    </div>
  );
}

export function PasswordVisibilityToggle({
  shown,
  onToggle,
  label,
  disabled,
  controls,
}: {
  shown: boolean;
  onToggle: () => void;
  label: string;
  disabled?: boolean;
  controls?: string;
}) {
  return (
    <button
      className={styles.visibility}
      type="button"
      aria-controls={controls}
      aria-label={label}
      aria-pressed={shown}
      disabled={disabled}
      onMouseDown={(event) => event.preventDefault()}
      onClick={onToggle}
    >
      <Icon name={shown ? "eye-off" : "eye"} />
    </button>
  );
}
