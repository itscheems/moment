import { Popover } from "@base-ui/react/popover";
import { type ReactNode, useMemo, useState } from "react";

import type { ArchiveMonthCount } from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { Icon } from "../../components/icons";
import {
  daysInMonth,
  mondayOffset,
  monthKeyOf,
  newestMonthIn,
  pickerYear,
  shiftMonth,
  splitMonth,
  yearsOf,
} from "./archiveDates";
import { archiveHref, dateForMonthDay } from "./memoryState";
import styles from "./Memory.module.css";

// Monday first, as a literal: the site's week, not the locale's. The iOS app
// transcribes this row rather than asking its platform, which would answer
// Sunday for a zh-Hans reader.
const WEEK_DAYS = ["一", "二", "三", "四", "五", "六", "日"];

export interface ArchiveCalendarProps {
  /** Every month that holds a post, newest first. */
  months: readonly ArchiveMonthCount[];
  monthCounts: ReadonlyMap<string, number>;
  /** The visible month's per-day counts, or null while they are on the way. */
  dayCounts: ReadonlyMap<number, number> | null;
  visibleMonth: string;
  /** How far the chevrons may page. */
  range: { oldest: string; newest: string };
  selectedDay: string | null;
  /** Today on the site's calendar. */
  today: string;
  /** The list starts somewhere other than the newest post. */
  positioned: boolean;
  /** Whose calendar this is, when the reader's clock would disagree. */
  note: string | null;
  /** In the shell's right rail, or at the top of the column. */
  variant: "rail" | "column";
  onVisibleMonthChange: (month: string) => void;
  /** A link on the calendar is about to move the list. */
  onNavigate?: () => void;
}

/**
 * The month title, which is also the way to any other year and month.
 *
 * The chevrons beside it move one month per press, which is right for the
 * month before this one and wrong for five years ago. This is the long jump:
 * the years that hold posts down one side, the twelve months of the chosen
 * year with their counts on the other. Both halves are decisions -- a year
 * moves the calendar to its newest month with posts and leaves the picker up,
 * and a month moves the list and closes it. A month with nothing in it is
 * drawn in its place and is not a control: a grid listing only the four
 * months of 2021 that hold something would read as "2021 had four months".
 */
function MonthPicker({
  months,
  monthCounts,
  onNavigate,
  onVisibleMonthChange,
  visibleMonth,
}: Pick<
  ArchiveCalendarProps,
  | "months"
  | "monthCounts"
  | "onNavigate"
  | "onVisibleMonthChange"
  | "visibleMonth"
>) {
  const years = useMemo(() => yearsOf(months), [months]);
  const yearCounts = useMemo(() => {
    const counts = new Map<number, number>();
    for (const item of months) {
      counts.set(item.year, (counts.get(item.year) ?? 0) + item.count);
    }
    return counts;
  }, [months]);
  const shown = splitMonth(visibleMonth);
  const [open, setOpen] = useState(false);
  const [year, setYear] = useState(
    () => pickerYear(visibleMonth, years) ?? shown.year,
  );

  const cells: ReactNode[] = [];
  for (let index = 1; index <= 12; index += 1) {
    const key = monthKeyOf({ year, month: index });
    const count = monthCounts.get(key) ?? 0;
    cells.push(
      <li key={key}>
        {count > 0 ? (
          <InternalLink
            aria-current={key === visibleMonth ? "date" : undefined}
            aria-label={`${year}年${index}月，${count} 条 Moment`}
            href={archiveHref({ month: key })}
            onClick={() => {
              onNavigate?.();
              setOpen(false);
            }}
          >
            <strong>{index}月</strong>
            <small>{count} 条</small>
          </InternalLink>
        ) : (
          <span
            aria-label={`${year}年${index}月，没有 Moment`}
            className={styles.pickerEmptyMonth}
          >
            <strong>{index}月</strong>
            <small>—</small>
          </span>
        )}
      </li>,
    );
  }

  return (
    <Popover.Root
      onOpenChange={(next) => {
        // Reseated on every opening: the chevrons may have paged the calendar
        // into a year the picker does not list, and the nearest listed one is
        // the honest place to start.
        if (next) setYear(pickerYear(visibleMonth, years) ?? shown.year);
        setOpen(next);
      }}
      open={open}
    >
      <h2 className={styles.calendarTitle}>
        <Popover.Trigger
          aria-label={`${shown.year}年${shown.month}月，选择年月`}
          className={styles.monthTitle}
        >
          <span>
            {shown.year}年{shown.month}月
          </span>
          <Icon name="chevron-down" />
        </Popover.Trigger>
      </h2>
      <Popover.Portal>
        <Popover.Positioner
          align="start"
          className={styles.pickerPositioner}
          collisionPadding={8}
          side="bottom"
          sideOffset={6}
        >
          <Popover.Popup aria-label="选择年月" className={styles.pickerPopup}>
            <div aria-label="年份" className={styles.pickerYears} role="group">
              {years.map((value) => (
                <button
                  aria-pressed={value === year}
                  className={styles.pickerYear}
                  key={value}
                  onClick={() => {
                    setYear(value);
                    const target = newestMonthIn(value, months);
                    if (target) onVisibleMonthChange(target);
                  }}
                  type="button"
                >
                  <strong>{value}</strong>
                  <small>{yearCounts.get(value)} 条</small>
                </button>
              ))}
            </div>
            <ol aria-label={`${year} 年的月份`} className={styles.pickerMonths}>
              {cells}
            </ol>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

/**
 * The month grid: a bold date over a count badge for a day that holds
 * something, a plain grey date for one that does not, the selected day
 * inverted onto the accent. The form is shared with the iOS app, which
 * transcribed it from here; what this component decides is only where it
 * stands and what its links do.
 *
 * A day with nothing on it is not a link. Opening an empty list tells the
 * reader after they act; a plain number tells them before.
 */
export function ArchiveCalendar({
  dayCounts,
  monthCounts,
  months,
  note,
  onNavigate,
  onVisibleMonthChange,
  positioned,
  range,
  selectedDay,
  today,
  variant,
  visibleMonth,
}: ArchiveCalendarProps) {
  const { month } = splitMonth(visibleMonth);
  const previous = shiftMonth(visibleMonth, -1);
  const next = shiftMonth(visibleMonth, 1);
  const monthCount = monthCounts.get(visibleMonth) ?? 0;

  const cells: ReactNode[] = [];
  const blanks = mondayOffset(visibleMonth);
  for (let index = 0; index < blanks; index += 1) {
    cells.push(
      <li
        aria-hidden="true"
        className={styles.calendarBlank}
        key={`blank-${index}`}
      />,
    );
  }
  const total = daysInMonth(visibleMonth);
  for (let day = 1; day <= total; day += 1) {
    const date = dateForMonthDay(visibleMonth, day);
    const count = dayCounts?.get(day) ?? 0;
    const isToday = date === today;
    const isSelected = date === selectedDay;
    const spoken = `${month}月${day}日${isToday ? "，今天" : ""}`;
    cells.push(
      <li
        className={count > 0 ? styles.calendarDay : styles.calendarEmptyDay}
        key={day}
      >
        {count > 0 ? (
          <InternalLink
            aria-current={
              isSelected || (isToday && !selectedDay) ? "date" : undefined
            }
            aria-label={`${spoken}，${count} 条 Moment${isSelected ? "，已选择" : ""}`}
            data-focus-key={`archive-day-${date}`}
            data-selected={isSelected ? "true" : undefined}
            href={archiveHref({ day: date })}
            onClick={onNavigate}
            title={`${count} 条 Moment`}
          >
            <strong>{day}</strong>
            <span>{count}</span>
          </InternalLink>
        ) : (
          <span aria-label={`${spoken}，没有 Moment`}>{day}</span>
        )}
      </li>,
    );
  }

  return (
    <section
      aria-label="归档日历"
      className={`${styles.calendar} ${
        variant === "rail" ? styles.calendarRail : styles.calendarWide
      }`}
    >
      <header className={styles.calendarHeader}>
        <MonthPicker
          monthCounts={monthCounts}
          months={months}
          onNavigate={onNavigate}
          onVisibleMonthChange={onVisibleMonthChange}
          visibleMonth={visibleMonth}
        />
        <div className={styles.calendarPager}>
          <button
            aria-label="上个月"
            className={styles.calendarPage}
            disabled={previous < range.oldest}
            onClick={() => onVisibleMonthChange(previous)}
            type="button"
          >
            <Icon name="back" />
          </button>
          <button
            aria-label="下个月"
            className={`${styles.calendarPage} ${styles.calendarPageNext}`}
            disabled={next > range.newest}
            onClick={() => onVisibleMonthChange(next)}
            type="button"
          >
            <Icon name="back" />
          </button>
        </div>
      </header>
      <ol aria-hidden="true" className={styles.weekHeader}>
        {WEEK_DAYS.map((day) => (
          <li key={day}>{day}</li>
        ))}
      </ol>
      <ol
        aria-busy={dayCounts === null && monthCount > 0 ? "true" : undefined}
        className={styles.calendarGrid}
      >
        {cells}
      </ol>
      {/* The archive's own count, which counts publications; the list below
          counts appearances, so a reposted Moment is one here and two rows
          there. Named next to the month it describes, where it is true. */}
      <p className={styles.calendarFooter}>
        <span>
          {monthCount > 0 ? `本月 ${monthCount} 条` : "这个月没有 Moment"}
        </span>
        {positioned ? (
          <InternalLink href="/archive" onClick={onNavigate}>
            回到最新
          </InternalLink>
        ) : null}
      </p>
      {note ? <p className={styles.calendarNote}>{note}</p> : null}
    </section>
  );
}
