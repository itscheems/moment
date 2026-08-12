import { Menu } from "@base-ui/react/menu";
import { PreviewCard } from "@base-ui/react/preview-card";
import { Tooltip } from "@base-ui/react/tooltip";
import type { ReactElement } from "react";

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
  return (
    <Tooltip.Root>
      <Tooltip.Trigger closeDelay={80} delay={350} render={children} />
      <Tooltip.Portal>
        <Tooltip.Positioner
          className={styles.tooltipPositioner}
          side="bottom"
          sideOffset={5}
        >
          <Tooltip.Popup className={styles.tooltip} role="tooltip">
            {exact}
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
  const title = site.title.trim() || "Moment";
  return (
    <PreviewCard.Root>
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
              <div className={styles.identityAvatar}>
                <SiteAvatar eager site={site} />
              </div>
              <h2>{title}</h2>
              {site.bio.trim() ? <p>{site.bio.trim()}</p> : null}
              <InternalLink
                className={styles.identityProfileLink}
                href="/profile"
              >
                查看个人资料
              </InternalLink>
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
      <Menu.Trigger render={trigger} />
      <Menu.Portal>
        <Menu.Positioner
          align={align}
          className={styles.sharePositioner}
          collisionAvoidance={{ side: "flip", align: "shift" }}
          collisionPadding={8}
          side="bottom"
          sideOffset={4}
        >
          <Menu.Popup aria-label="分享" className={styles.sharePopup}>
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
