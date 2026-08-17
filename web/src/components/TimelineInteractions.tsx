import { Menu } from "@base-ui/react/menu";
import { PreviewCard } from "@base-ui/react/preview-card";
import { Tooltip } from "@base-ui/react/tooltip";
import { useRef, type ReactElement, type SyntheticEvent } from "react";

import type { SiteIdentity } from "../api/public";
import { InternalLink } from "../app/navigation";
import { SiteAvatar, SiteCover } from "./SiteIdentity";
import { notify } from "./Feedback";
import { Icon } from "./icons";
import styles from "./TimelineInteractions.module.css";

export function ExactTimeTooltip({
  children,
  exact,
}: {
  children: ReactElement;
  exact: string;
}) {
  return <ActionTooltip label={exact}>{children}</ActionTooltip>;
}

export function ActionTooltip({
  children,
  label,
  wide = false,
}: {
  children: ReactElement;
  label: string;
  wide?: boolean;
}) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger closeDelay={80} delay={350} render={children} />
      <Tooltip.Portal>
        <Tooltip.Positioner
          className={styles.tooltipPositioner}
          side="bottom"
          sideOffset={5}
        >
          <Tooltip.Popup
            className={`${styles.tooltip} ${wide ? styles.tooltipWide : ""}`}
            role="tooltip"
          >
            {label}
          </Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}

export function SiteIdentityPreview({
  children,
  site,
}: {
  children: ReactElement;
  site: SiteIdentity;
}) {
  const title = site.displayName.trim() || site.title.trim() || "Moment";
  const profileHref = `/@${site.username}`;
  const previewActionsRef = useRef<PreviewCard.Root.Actions>(null);
  const unmountBeforeNavigation = () => {
    previewActionsRef.current?.unmount();
  };
  return (
    <PreviewCard.Root actionsRef={previewActionsRef}>
      <PreviewCard.Trigger closeDelay={160} delay={450} render={children} />
      <PreviewCard.Portal>
        <PreviewCard.Positioner
          align="start"
          className={styles.identityPositioner}
          side="bottom"
          sideOffset={6}
        >
          <PreviewCard.Popup className={styles.identityCard}>
            <div className={styles.identityCover}>
              <SiteCover eager site={site} />
            </div>
            <div className={styles.identityBody}>
              <InternalLink
                aria-label={`查看 ${title} 的个人资料`}
                className={styles.identityAvatarLink}
                href={profileHref}
                onClick={unmountBeforeNavigation}
              >
                <span className={styles.identityAvatar}>
                  <SiteAvatar eager site={site} />
                </span>
              </InternalLink>
              <h2>
                <InternalLink
                  className={styles.identityNameLink}
                  href={profileHref}
                  onClick={unmountBeforeNavigation}
                >
                  {title}
                </InternalLink>
              </h2>
              <p className={styles.identityHandle}>@{site.username}</p>
              {site.bio.trim() ? (
                <p className={styles.identityBio}>{site.bio.trim()}</p>
              ) : null}
            </div>
          </PreviewCard.Popup>
        </PreviewCard.Positioner>
      </PreviewCard.Portal>
    </PreviewCard.Root>
  );
}

export function ShareMenu({
  align = "end",
  baseUrl,
  href,
  title,
  trigger,
}: {
  align?: "start" | "center" | "end";
  baseUrl: string | null;
  href: string;
  title: string;
  trigger: ReactElement;
}) {
  const triggerRef = useRef<HTMLButtonElement>(null);
  const stopCardNavigation = (event: SyntheticEvent) => {
    event.stopPropagation();
  };
  const url = () =>
    new URL(href, baseUrl?.trim() || window.location.origin).toString();

  const copy = async () => {
    try {
      if (!navigator.clipboard) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(url());
      notify("链接已复制");
    } catch {
      notify("暂时无法复制链接", { type: "error" });
    }
  };

  const share = async () => {
    try {
      await navigator.share({ title, url: url() });
      notify("已打开系统分享");
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") return;
      notify("暂时无法分享", { type: "error" });
    }
  };

  return (
    <Menu.Root>
      <Tooltip.Root>
        <Tooltip.Trigger
          closeDelay={80}
          delay={350}
          render={<Menu.Trigger ref={triggerRef} render={trigger} />}
        />
        <Tooltip.Portal>
          <Tooltip.Positioner
            className={styles.tooltipPositioner}
            side="bottom"
            sideOffset={5}
          >
            <Tooltip.Popup className={styles.tooltip} role="tooltip">
              分享
            </Tooltip.Popup>
          </Tooltip.Positioner>
        </Tooltip.Portal>
      </Tooltip.Root>
      <Menu.Portal>
        <Menu.Positioner
          align={align}
          className={styles.sharePositioner}
          collisionAvoidance={{ side: "flip", align: "shift" }}
          collisionPadding={8}
          side="bottom"
          sideOffset={4}
        >
          <Menu.Popup
            aria-label="分享"
            className={styles.sharePopup}
            finalFocus={triggerRef}
            onClick={stopCardNavigation}
          >
            <Menu.Item className={styles.shareItem} onClick={() => void copy()}>
              <Icon name="link" />
              复制链接
            </Menu.Item>
            {typeof navigator.share === "function" ? (
              <Menu.Item
                className={styles.shareItem}
                onClick={() => void share()}
              >
                <Icon name="share" />
                系统分享
              </Menu.Item>
            ) : null}
            <Menu.Item
              className={styles.shareItem}
              onClick={() =>
                window.open(url(), "_blank", "noopener,noreferrer")
              }
            >
              <Icon name="open" />
              在新标签页打开
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
