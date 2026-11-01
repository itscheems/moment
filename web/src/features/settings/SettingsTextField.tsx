import type { InputHTMLAttributes } from "react";
import styles from "./Settings.module.css";

export function SettingsTextField({
  label,
  ...input
}: Omit<InputHTMLAttributes<HTMLInputElement>, "placeholder"> & {
  label: string;
}) {
  return (
    <label
      className={styles.floatingField}
      data-filled={String(input.value ?? input.defaultValue ?? "").length > 0}
    >
      <span>{label}</span>
      <input {...input} placeholder=" " />
    </label>
  );
}
