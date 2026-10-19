import { useState } from "react";
import { Collapsible } from "@base-ui/react/collapsible";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { HeadlessMantineProvider, Pagination } from "@mantine/core";
import { Icon } from "../../components/icons";
import { ActionTooltip } from "../../components/TimelineInteractions";
import { MapPlaces } from "./MapPlaces";
import type { MapCardData, MapPlace } from "./types";
import styles from "./Maps.module.css";

const PAGE_SIZE = 10;

export function MapPlaceDisclosure({
  card,
  onSelect,
}: {
  card: MapCardData;
  onSelect: (place: MapPlace) => void;
}) {
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState(0);
  const reduceMotion = useReducedMotion();
  const pageCount = Math.ceil(card.places.length / PAGE_SIZE);
  const currentPage = Math.min(page, pageCount - 1);
  const pagePlaces = card.places.slice(
    currentPage * PAGE_SIZE,
    (currentPage + 1) * PAGE_SIZE,
  );
  return (
    <Collapsible.Root open={open} onOpenChange={setOpen}>
      <div className={styles.inlinePlacesHeader}>
        <span>地点 · {card.places.length}</span>
        <ActionTooltip label={open ? "收起地点列表" : "展开地点列表"}>
          <Collapsible.Trigger
            className={styles.inlinePlacesToggle}
            aria-label={open ? "收起地点列表" : "展开地点列表"}
          >
            <span className={styles.inlinePlacesChevron}>
              <Icon name="chevron-down" />
            </span>
          </Collapsible.Trigger>
        </ActionTooltip>
      </div>
      <Collapsible.Panel className={styles.inlinePlacesPanel}>
        <AnimatePresence initial={false} mode="wait">
          <motion.div
            key={currentPage}
            initial={{ opacity: 0, y: reduceMotion ? 0 : 3 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: reduceMotion ? 0 : -3 }}
            transition={{ duration: reduceMotion ? 0 : 0.12 }}
          >
            <MapPlaces
              card={card}
              visiblePlaces={pagePlaces}
              onSelect={onSelect}
            />
          </motion.div>
        </AnimatePresence>
        {pageCount > 1 && (
          <nav aria-label="地图地点分页">
            <HeadlessMantineProvider>
              <Pagination.Root
                total={pageCount}
                value={currentPage + 1}
                onChange={(value) => setPage(value - 1)}
                classNames={{
                  root: styles.placePagination,
                  control: styles.placePageControl,
                }}
                getItemProps={(value) => ({ "aria-label": `第${value}页地点` })}
              >
                <Pagination.Previous aria-label="上一页地点" />
                <Pagination.Items />
                <Pagination.Next aria-label="下一页地点" />
              </Pagination.Root>
            </HeadlessMantineProvider>
          </nav>
        )}
      </Collapsible.Panel>
    </Collapsible.Root>
  );
}
