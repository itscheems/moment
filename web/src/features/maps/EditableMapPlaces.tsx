import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  MouseSensor,
  TouchSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { restrictToVerticalAxis } from "@dnd-kit/modifiers";
import { CSS } from "@dnd-kit/utilities";
import {
  AnimatePresence,
  animate,
  motion,
  useIsPresent,
  useMotionValue,
  useReducedMotion,
} from "motion/react";
import { ContextMenu } from "@base-ui/react/context-menu";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { Icon } from "../../components/icons";
import { mapPlaceIdentity } from "./geometry";
import { PlaceContent } from "./MapPlaceContent";
import type { MapCardData, MapPlace } from "./types";
import styles from "./Maps.module.css";

// IDs belong to this rendered list, while persistence continues to store places.
function placeRows(places: MapPlace[]) {
  const occurrences = new Map<string, number>();
  return places.map((place) => {
    const identity = mapPlaceIdentity(place);
    const occurrence = occurrences.get(identity) ?? 0;
    occurrences.set(identity, occurrence + 1);
    return { id: `${identity}:${occurrence}`, place };
  });
}
type PlaceRow = ReturnType<typeof placeRows>[number];

function SortablePlace({
  row,
  disabled,
  sorting,
  revealed,
  onReveal,
  onRemove,
  register,
}: {
  row: PlaceRow;
  disabled: boolean;
  sorting: boolean;
  revealed: boolean;
  onReveal: (id: string | null) => void;
  onRemove: (row: PlaceRow) => void;
  register: (id: string, element: HTMLElement | null) => void;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: row.id, disabled });
  const present = useIsPresent();
  const reduceMotion = useReducedMotion();
  const swipeWidth = 52;
  const x = useMotionValue(0);
  const verticalGesture = useRef(false);
  useEffect(() => {
    if (isDragging) verticalGesture.current = true;
  }, [isDragging]);
  useEffect(() => {
    if (reduceMotion || sorting) {
      x.set(revealed && !sorting ? -swipeWidth : 0);
      return;
    }
    const animation = animate(x, revealed ? -swipeWidth : 0, {
      type: "spring",
      stiffness: 500,
      damping: 38,
    });
    return () => animation.stop();
  }, [revealed, sorting, reduceMotion, x]);
  return (
    <motion.li
      ref={setNodeRef}
      className={styles.sortablePlace}
      aria-hidden={!present || undefined}
      inert={!present}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      initial={false}
      animate={{ opacity: isDragging ? 0.2 : 1, height: "auto" }}
      exit={{ opacity: 0, height: 0 }}
      transition={{ duration: reduceMotion ? 0 : 0.16 }}
    >
      <button
        className={styles.swipeDelete}
        type="button"
        aria-label={`删除${row.place.name}`}
        disabled={disabled || !revealed}
        tabIndex={revealed ? 0 : -1}
        aria-hidden={!revealed}
        onClick={() => onRemove(row)}
      >
        <Icon name="delete" />
      </button>
      <ContextMenu.Root
        onOpenChange={(open, details) => {
          if (
            open &&
            (disabled ||
              sorting ||
              details.event.type.startsWith("touch") ||
              ("pointerType" in details.event &&
                details.event.pointerType === "touch"))
          )
            details.cancel();
        }}
      >
        <ContextMenu.Trigger
          render={
            <motion.div
              {...attributes}
              {...listeners}
              role="group"
              aria-label={`${row.place.name}，按住拖动排序`}
              data-map-place=""
              data-dragging={isDragging || undefined}
              ref={(element) => register(row.id, element)}
              className={styles.placeRow}
              drag={disabled || sorting ? false : "x"}
              dragConstraints={{ left: -swipeWidth, right: 0 }}
              dragElastic={0.06}
              dragMomentum={false}
              style={{ x }}
              onPointerDown={() => {
                verticalGesture.current = false;
              }}
              transition={
                reduceMotion
                  ? { duration: 0 }
                  : { type: "spring", stiffness: 500, damping: 38 }
              }
              onDragEnd={(_event, info) => {
                const reveal =
                  !sorting &&
                  !verticalGesture.current &&
                  (x.get() < -26 || info.velocity.x < -180);
                onReveal(reveal ? row.id : null);
                if (reduceMotion || sorting || verticalGesture.current)
                  x.set(0);
                else
                  animate(x, reveal ? -swipeWidth : 0, {
                    type: "spring",
                    stiffness: 500,
                    damping: 38,
                  });
              }}
              onKeyDown={(event) => {
                if (event.target !== event.currentTarget) return;
                listeners?.onKeyDown?.(event);
                if (event.key === "Escape") onReveal(null);
                if (
                  (event.key === "Delete" || event.key === "Backspace") &&
                  !disabled &&
                  !sorting
                ) {
                  event.preventDefault();
                  onRemove(row);
                }
              }}
            />
          }
        >
          <PlaceContent place={row.place} />
        </ContextMenu.Trigger>
        <ContextMenu.Portal>
          <ContextMenu.Positioner
            className={styles.placeMenuPositioner}
            sideOffset={4}
          >
            <ContextMenu.Popup className={styles.placeMenu}>
              <ContextMenu.Item
                className={styles.placeMenuDelete}
                disabled={disabled}
                onClick={() => onRemove(row)}
              >
                <Icon name="delete" /> 删除地点
              </ContextMenu.Item>
            </ContextMenu.Popup>
          </ContextMenu.Positioner>
        </ContextMenu.Portal>
      </ContextMenu.Root>
    </motion.li>
  );
}

export default function EditableMapPlaces({
  card,
  onChange,
  disabled = false,
}: {
  card: MapCardData;
  onChange: (card: MapCardData | null) => void;
  disabled?: boolean;
}) {
  const listId = useId();
  const rows = placeRows(card.places);
  const listRef = useRef<HTMLOListElement>(null);
  const elements = useRef(new Map<string, HTMLElement>());
  const [activeId, setActiveId] = useState<string | null>(null);
  const [revealedId, setRevealedId] = useState<string | null>(null);
  const [remove, setRemove] = useState<PlaceRow | null>(null);
  const removeFocus = useRef<string | null>(null);
  const [overlayWidth, setOverlayWidth] = useState<number>();
  const requestRemove = (row: PlaceRow) => {
    if (disabled) return;
    removeFocus.current = row.id;
    setRemove(row);
  };
  const reduceMotion = useReducedMotion();
  const sensors = useSensors(
    useSensor(MouseSensor, {
      activationConstraint: { delay: 220, tolerance: 6 },
    }),
    useSensor(TouchSensor, {
      activationConstraint: { delay: 220, tolerance: 6 },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );
  const active = rows.find((row) => row.id === activeId);
  const finishSort = ({ active, over }: DragEndEvent) => {
    setActiveId(null);
    if (disabled || !over || active.id === over.id) return;
    const from = rows.findIndex((row) => row.id === active.id);
    const to = rows.findIndex((row) => row.id === over.id);
    if (from >= 0 && to >= 0)
      onChange({ ...card, places: arrayMove(card.places, from, to) });
  };
  const list = (
    <ol
      className={styles.places}
      ref={listRef}
      tabIndex={-1}
      aria-label="地图地点"
    >
      <AnimatePresence initial={false}>
        {rows.map((row) => (
          <SortablePlace
            key={row.id}
            row={row}
            disabled={disabled}
            sorting={activeId !== null}
            revealed={revealedId === row.id}
            onReveal={setRevealedId}
            onRemove={requestRemove}
            register={(id, element) => {
              if (element) elements.current.set(id, element);
              else elements.current.delete(id);
            }}
          />
        ))}
      </AnimatePresence>
    </ol>
  );
  return (
    <>
      <DndContext
        id={listId}
        sensors={sensors}
        collisionDetection={closestCenter}
        modifiers={[restrictToVerticalAxis]}
        accessibility={{
          screenReaderInstructions: {
            draggable:
              "按空格开始排序，上下方向键移动，再按空格确认，Escape 取消。",
          },
          announcements: {
            onDragStart: ({ active }) =>
              `正在移动${rows.find((row) => row.id === active.id)?.place.name ?? "地点"}。`,
            onDragOver: ({ over }) =>
              over
                ? `移动到第${rows.findIndex((row) => row.id === over.id) + 1}个位置。`
                : undefined,
            onDragEnd: () => "地点顺序已更新。",
            onDragCancel: () => "已取消排序。",
          },
        }}
        onDragStart={({ active }) => {
          setOverlayWidth(active.rect.current.initial?.width);
          setActiveId(String(active.id));
          setRevealedId(null);
        }}
        onDragCancel={() => setActiveId(null)}
        onDragEnd={finishSort}
      >
        <SortableContext
          items={rows.map((row) => row.id)}
          strategy={verticalListSortingStrategy}
        >
          {list}
        </SortableContext>
        {createPortal(
          <DragOverlay
            zIndex={124}
            dropAnimation={
              reduceMotion ? null : { duration: 160, easing: "ease-out" }
            }
          >
            {active ? (
              <div
                className={`${styles.placeRow} ${styles.placeDragOverlay}`}
                style={{
                  width: overlayWidth,
                }}
              >
                <PlaceContent place={active.place} />
              </div>
            ) : null}
          </DragOverlay>,
          document.body,
        )}
      </DndContext>
      <ConfirmDialog
        open={remove !== null}
        onOpenChange={(open) => {
          if (!open) {
            setRemove(null);
            setRevealedId(null);
          }
        }}
        title="删除地点？"
        description={remove ? `从地图中删除「${remove.place.name}」？` : ""}
        confirmLabel="删除"
        cancelLabel="取消"
        danger
        pending={disabled}
        finalFocus={() =>
          removeFocus.current
            ? (elements.current.get(removeFocus.current) ?? listRef.current)
            : listRef.current
        }
        onConfirm={() => {
          if (disabled) return;
          const places = rows
            .filter((row) => row.id !== remove?.id)
            .map((row) => row.place);
          onChange(
            places.length
              ? {
                  places,
                  connectPoints: places.length > 1 && card.connectPoints,
                }
              : null,
          );
        }}
      />
    </>
  );
}
