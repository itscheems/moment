import { Menu } from "@base-ui/react/menu";
import { Tabs } from "@base-ui/react/tabs";
import { motion, useReducedMotion, type HTMLMotionProps } from "motion/react";
import { useRef, useState, type ReactNode } from "react";

import { Icon } from "../../components/icons";
import { profileFilters, type ProfileFilter } from "./profileTimeline";
import interactionStyles from "../../components/TimelineInteractions.module.css";
import styles from "./ProfilePage.module.css";

export function ProfileTabs({
  filter,
  onSelect: selectFilter,
  loading,
  children,
}: {
  filter: ProfileFilter;
  onSelect: (filter: ProfileFilter) => void;
  loading?: boolean;
  children: (filter: ProfileFilter) => ReactNode;
}) {
  const [tabState, setTabState] = useState<{
    filter: ProfileFilter;
    leftFilter: "all" | "moment";
    direction: "left" | "right";
    slide: boolean;
  }>(() => ({
    filter,
    leftFilter: filter === "moment" ? "moment" : "all",
    direction: "right",
    slide: false,
  }));
  // Derive the transition before rendering children, including history navigation.
  // The two left-hand choices replace content in place; only crossing columns slides.
  if (tabState.filter !== filter) {
    setTabState({
      filter,
      leftFilter: filter === "article" ? tabState.leftFilter : filter,
      direction: filter === "article" ? "right" : "left",
      slide: filter === "article" || tabState.filter === "article",
    });
  }
  const { leftFilter, direction, slide } = tabState;
  const leftTabSlot = useRef<HTMLDivElement>(null);
  const reducedMotion = useReducedMotion();
  const label = leftFilter === "all" ? "全部" : "帖子";
  const labelAnimation = (selected: boolean) => ({
    width: selected ? "auto" : 0,
    opacity: selected ? 1 : 0,
    "--label-fade": selected ? "0px" : "16px",
  });
  const tabTransition = {
    duration: reducedMotion ? 0 : 0.24,
    ease: [0.22, 1, 0.36, 1] as const,
  };
  const labelTransition = {
    ...tabTransition,
    opacity: { duration: tabTransition.duration, ease: "linear" as const },
  };
  const labelContentAnimation = (selected: boolean) => ({
    x: selected ? "0%" : "-100%",
  });

  return (
    <Tabs.Root
      value={filter}
      onValueChange={(value) => selectFilter(value as ProfileFilter)}
      data-feed-direction={direction}
      data-feed-motion={slide ? "slide" : "none"}
      className={styles.contentTabs}
    >
      <Tabs.List
        aria-label="帖子类型"
        className={styles.tabList}
        activateOnFocus={false}
      >
        <div className={styles.tabSlot} ref={leftTabSlot}>
          <Tabs.Tab
            value={leftFilter}
            aria-label={label}
            className={styles.typeTab}
            onClick={() => {
              if (filter === leftFilter) selectFilter(leftFilter);
            }}
          />
          {/* Sibling controls share a full-column hit area without nested buttons. */}
          <div
            className={styles.tabContent}
            data-active={filter !== "article" ? "" : undefined}
          >
            <Icon name="archive" variant="navigation" />
            <motion.span
              className={styles.tabLabel}
              initial={false}
              animate={labelAnimation(filter !== "article")}
              transition={labelTransition}
              aria-hidden={filter === "article"}
              inert={filter === "article"}
            >
              <motion.span
                aria-hidden="true"
                className={styles.tabLabelContent}
                initial={false}
                animate={labelContentAnimation(filter !== "article")}
                transition={tabTransition}
              >
                {label}
              </motion.span>
            </motion.span>
            {/* The dropdown must not lengthen the text's percentage-based slide. */}
            <motion.span
              className={styles.filterSlot}
              initial={false}
              animate={{
                width: filter !== "article" ? "auto" : 0,
                opacity: filter !== "article" ? 1 : 0,
              }}
              transition={labelTransition}
              aria-hidden={filter === "article"}
              inert={filter === "article"}
            >
              <Menu.Root>
                <Menu.Trigger
                  aria-label="选择帖子类型"
                  className={styles.filterTrigger}
                  disabled={filter === "article"}
                >
                  <Icon name="chevron-down" />
                </Menu.Trigger>
                <Menu.Portal>
                  <Menu.Positioner
                    anchor={leftTabSlot}
                    side="bottom"
                    align="center"
                    sideOffset={4}
                    collisionPadding={8}
                    className={interactionStyles.sharePositioner}
                  >
                    <Menu.Popup
                      aria-label="帖子类型筛选"
                      className={interactionStyles.sharePopup}
                    >
                      {(["all", "moment"] as const).map((kind) => (
                        <Menu.Item
                          key={kind}
                          className={interactionStyles.shareItem}
                          onClick={() => {
                            selectFilter(kind);
                          }}
                        >
                          <span>{kind === "all" ? "全部" : "帖子"}</span>
                          {leftFilter === kind ? <Icon name="check" /> : null}
                        </Menu.Item>
                      ))}
                    </Menu.Popup>
                  </Menu.Positioner>
                </Menu.Portal>
              </Menu.Root>
            </motion.span>
          </div>
        </div>
        <div className={styles.tabSlot}>
          <Tabs.Tab
            value="article"
            aria-label="文章"
            className={styles.typeTab}
            onClick={() => {
              if (filter === "article") selectFilter("article");
            }}
          >
            <svg viewBox="0 0 24 24" aria-hidden="true" fill="currentColor">
              <g>
                <path
                  clipRule="evenodd"
                  fillRule="evenodd"
                  d="M18.067 12.701c1.169-1.168 3.063-1.168 4.232 0 1.168 1.169 1.168 3.063 0 4.232L17.83 21.4c-.827.827-1.918 1.339-3.083 1.445l-2.851.259.258-2.852c.106-1.165.618-2.256 1.445-3.083l4.468-4.469zm2.818 1.414c-.388-.387-1.016-.387-1.404 0l-4.468 4.469c-.497.496-.803 1.152-.867 1.85l-.042.46.461-.04c.7-.064 1.354-.37 1.85-.867l4.47-4.468c.387-.388.387-1.016 0-1.404z"
                />
                <path d="M16 2c.684 0 1.257-.001 1.724.037.478.04.933.125 1.365.345.658.335 1.194.87 1.53 1.53.22.43.305.886.344 1.364C21 5.743 21 6.316 21 7v2h-2V7c0-.717 0-1.194-.03-1.56-.03-.355-.08-.518-.133-.62-.144-.283-.374-.513-.656-.657-.103-.052-.266-.104-.62-.133C17.194 4 16.716 4 16 4H8c-.717 0-1.194 0-1.56.03-.355.03-.518.08-.62.133-.283.144-.513.374-.657.656-.052.103-.104.266-.133.62C5 5.806 5 6.283 5 7v10c0 .716 0 1.194.03 1.56.03.355.08.518.133.62.144.283.374.513.656.657.103.052.266.104.62.133.367.03.844.03 1.561.03h1v2H8c-.684 0-1.257.001-1.724-.037-.478-.04-.933-.125-1.365-.345-.658-.335-1.194-.87-1.53-1.53-.22-.43-.305-.886-.344-1.364C3 18.257 3 17.684 3 17V7c0-.684-.001-1.257.037-1.724.04-.478.125-.933.345-1.365.335-.658.87-1.194 1.53-1.53.43-.22.886-.305 1.364-.344C6.743 2 7.316 2 8 2h8z" />
                <path d="M13 13H8v-2h5v2zm3-4H8V7h8v2z" />
              </g>
            </svg>
            <motion.span
              aria-hidden="true"
              className={styles.tabLabel}
              initial={false}
              animate={labelAnimation(filter === "article")}
              transition={labelTransition}
            >
              <motion.span
                className={styles.tabLabelContent}
                initial={false}
                animate={labelContentAnimation(filter === "article")}
                transition={tabTransition}
              >
                文章
              </motion.span>
            </motion.span>
          </Tabs.Tab>
        </div>
        <Tabs.Indicator
          className={styles.tabIndicator}
          render={
            <motion.span
              initial={false}
              animate={{ x: filter === "article" ? "250%" : "0%" }}
              transition={tabTransition}
            />
          }
        />
      </Tabs.List>
      <div className={styles.feedViewport} aria-busy={loading}>
        {profileFilters.map((kind) => (
          <Tabs.Panel
            key={kind}
            value={kind}
            className={styles.feedPanel}
            render={(props, state) => (
              <motion.div
                {...(props as HTMLMotionProps<"div">)}
                initial={
                  slide &&
                  !reducedMotion &&
                  state.transitionStatus === "starting"
                    ? {
                        x: direction === "right" ? "100%" : "-100%",
                      }
                    : false
                }
                animate={{
                  x:
                    slide &&
                    !reducedMotion &&
                    state.transitionStatus === "ending"
                      ? direction === "right"
                        ? "-100%"
                        : "100%"
                      : "0%",
                }}
                transition={{
                  ...tabTransition,
                  duration: slide ? tabTransition.duration : 0,
                }}
              />
            )}
          >
            {children(kind)}
          </Tabs.Panel>
        ))}
      </div>
    </Tabs.Root>
  );
}
