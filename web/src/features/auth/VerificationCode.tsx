import { OTPField } from "@base-ui/react/otp-field";
import { useTranslation } from "react-i18next";
import forms from "../../components/Forms.module.css";
import styles from "./VerificationCode.module.css";

export function VerificationCode({
  id,
  value,
  onChange,
  disabled,
  invalid,
  descriptionId,
}: {
  id: string;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  invalid?: boolean;
  descriptionId?: string;
}) {
  const { t } = useTranslation();
  return (
    <div className={styles.field}>
      <label className={forms.srOnly} htmlFor={id}>
        {t("验证码")}
      </label>
      <OTPField.Root
        id={id}
        className={styles.root}
        length={6}
        value={value}
        onValueChange={onChange}
        validationType="numeric"
        autoComplete="one-time-code"
        disabled={disabled}
        aria-describedby={descriptionId}
      >
        {[0, 1, 2, 3, 4, 5].map((index) => (
          <span className={styles.slot} key={index}>
            <OTPField.Input
              className={styles.input}
              placeholder=" "
              aria-label={
                index === 0
                  ? undefined
                  : t("验证码第 {{value1}} 位", { value1: index + 1 })
              }
              aria-invalid={invalid || undefined}
              aria-describedby={descriptionId}
            />
          </span>
        ))}
      </OTPField.Root>
    </div>
  );
}
