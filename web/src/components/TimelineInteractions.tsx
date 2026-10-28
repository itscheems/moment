import { AccountName } from "./AccountName";
import type { AccountIdentityKey } from "./accountVerification";
import { Menu } from "@base-ui/react/menu";
import { PreviewCard } from "@base-ui/react/preview-card";
import { Tooltip } from "@base-ui/react/tooltip";
import {
  useRef,
  useState,
  useCallback,
  type ReactElement,
  type SyntheticEvent,
} from "react";

import { ShareCardDialog } from "../features/share/ShareCardDialog";
import type { ShareCardSource } from "../features/share/shareCard";
import { InternalLink } from "../app/navigation";
import {
  SiteAvatar,
  SiteCover,
  siteProfileHref,
  siteDisplayName,
  type IdentityFace,
} from "./SiteIdentity";
import { notify } from "./Feedback";
import { Icon } from "./icons";
import styles from "./TimelineInteractions.module.css";
import tooltipStyles from "./Tooltip.module.css";

export interface ActionTooltipPlacement {
  side?: "top" | "bottom" | "left" | "right";
  sideOffset?: number;
}

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
  placement,
  container,
}: {
  children: ReactElement;
  label: string;
  wide?: boolean;
  placement?: ActionTooltipPlacement;
  container?: HTMLElement | null;
}) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger closeDelay={80} delay={350} render={children} />
      <Tooltip.Portal container={container}>
        <Tooltip.Positioner
          className={tooltipStyles.positioner}
          side={placement?.side ?? "bottom"}
          sideOffset={placement?.sideOffset ?? 5}
        >
          <Tooltip.Popup
            className={`${tooltipStyles.popup} ${wide ? tooltipStyles.wide : ""}`}
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
  site: IdentityFace & AccountIdentityKey & { username: string; bio: string };
}) {
  const title = siteDisplayName(site);
  const profileHref = siteProfileHref(site);
  const previewActionsRef = useRef<PreviewCard.Root.Actions>(null);
  const previewTriggerRef = useRef<HTMLAnchorElement>(null);
  const closeBeforeNavigation = () => {
    const actions = previewActionsRef.current;
    actions?.close();
    actions?.unmount();
  };
  return (
    <PreviewCard.Root actionsRef={previewActionsRef}>
      <PreviewCard.Trigger
        ref={previewTriggerRef}
        closeDelay={300}
        delay={600}
        render={children}
      />
      <PreviewCard.Portal>
        <PreviewCard.Positioner
          align="center"
          alignOffset={({ anchor, positioner }) => {
            const trigger = previewTriggerRef.current;
            if (!trigger) return 0;
            const rect = trigger.getBoundingClientRect();
            const center = rect.left + rect.width / 2;
            const viewportWidth =
              trigger.ownerDocument.documentElement.clientWidth;
            // Prefer an element edge when centering would leave the viewport.
            if (center - positioner.width / 2 < 0)
              return (positioner.width - anchor.width) / 2;
            if (center + positioner.width / 2 > viewportWidth)
              return -(positioner.width - anchor.width) / 2;
            return 0;
          }}
          className={styles.identityPositioner}
          collisionAvoidance={{
            side: "flip",
            align: "shift",
            fallbackAxisSide: "none",
          }}
          side="bottom"
          sideOffset={10}
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
                onClick={closeBeforeNavigation}
              >
                <span className={styles.identityAvatar}>
                  <SiteAvatar eager site={site} />
                </span>
              </InternalLink>
              <h2>
                <InternalLink
                  className={styles.identityNameLink}
                  href={profileHref}
                  onClick={closeBeforeNavigation}
                >
                  <AccountName identity={site} name={title} prominent />
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
  card,
  href,
  title,
  trigger,
  tooltipPlacement,
}: {
  align?: "start" | "center" | "end";
  baseUrl: string | null;
  /**
   * What a picture of this would contain. Absent where there is nothing to
   * draw, which is what keeps the entry out of the menu rather than offering
   * something that opens onto an error.
   */
  card?: ShareCardSource;
  href: string;
  title: string;
  trigger: ReactElement;
  tooltipPlacement?: ActionTooltipPlacement;
}) {
  const triggerRef = useRef<HTMLButtonElement>(null);
  const [cardOpen, setCardOpen] = useState(false);
  const cardOwnsFocus = useRef(false);
  const restoreMenuFocus = useCallback(
    () => (cardOwnsFocus.current ? false : triggerRef.current),
    [],
  );
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
      <ActionTooltip label="分享" placement={tooltipPlacement}>
        <Menu.Trigger ref={triggerRef} render={trigger} />
      </ActionTooltip>
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
            finalFocus={restoreMenuFocus}
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
            {card ? (
              <Menu.Item
                className={styles.shareItem}
                onClick={() => {
                  cardOwnsFocus.current = true;
                  setCardOpen(true);
                }}
              >
                <Icon name="media" />
                生成分享图片
              </Menu.Item>
            ) : null}
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
      {card ? (
        <ShareCardDialog
          input={card}
          finalFocus={triggerRef}
          onOpenChange={(open) => {
            cardOwnsFocus.current = open;
            setCardOpen(open);
          }}
          open={cardOpen}
        />
      ) : null}
    </Menu.Root>
  );
}
