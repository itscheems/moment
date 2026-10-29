import { useTranslation } from "react-i18next";
import { useCallback, type ComponentProps } from "react";
import { InternalLink, useNavigation } from "../../app/navigation";
import { loginHref } from "./returnPath";
import { useSession } from "./SessionProvider";

/** Share return-path and history behavior across every account-entry action. */
export function useLoginNavigation(returnTo?: string) {
  const { pathname, search, navigate, replace } = useNavigation();
  const href = loginHref(
    returnTo ?? `${pathname}${search}${window.location.hash}`,
  );
  const requestLogin = useCallback(
    (options?: { replace?: boolean }) => {
      if (options?.replace) replace(href);
      else navigate(href);
    },
    [href, navigate, replace],
  );
  return { href, requestLogin };
}

export function LoginLink({
  returnTo,
  ...props
}: Omit<ComponentProps<typeof InternalLink>, "href"> & { returnTo?: string }) {
  const { href } = useLoginNavigation(returnTo);
  return <InternalLink {...props} href={href} />;
}

/** Account readiness/approval never renders a second sign-in form. */
export function AccountAccessNotice() {
  const { t } = useTranslation();

  const { state, account, retryStatus } = useSession();
  if (state === "unavailable")
    return (
      <div role="alert">
        <p>{t("暂时无法确认登录状态，请稍后重试。")}</p>
        <button type="button" onClick={retryStatus}>
          {t("重试检查")}
        </button>
      </div>
    );
  if (state === "signed-in" && account?.status === "pending")
    return (
      <p role="status">{t("账号正在等待管理员批准，批准后就能发布和回复。")}</p>
    );
  return <p role="status">{t("正在确认登录状态…")}</p>;
}
