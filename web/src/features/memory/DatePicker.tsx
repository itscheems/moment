import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { useEffect, useId, useMemo, useRef, useState } from "react";

import { Icon } from "../../components/icons";
import styles from "./DatePicker.module.css";

const WEEKDAYS = ["日", "一", "二", "三", "四", "五", "六"];

type CalendarDay = {
  date: string;
  day: number;
  inMonth: boolean;
};

function canonicalDate(year: number, month: number, day: number): string {
  return `${year.toString().padStart(4, "0")}-${(month + 1)
    .toString()
    .padStart(2, "0")}-${day.toString().padStart(2, "0")}`;
}

function dateParts(
  value: string,
): { year: number; month: number; day: number } | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]) - 1;
  const day = Number(match[3]);
  const candidate = new Date(Date.UTC(year, month, day));
  return candidate.getUTCFullYear() === year &&
    candidate.getUTCMonth() === month &&
    candidate.getUTCDate() === day
    ? { year, month, day }
    : null;
}

function todayParts(now: Date): { year: number; month: number; day: number } {
  return {
    year: now.getFullYear(),
    month: now.getMonth(),
    day: now.getDate(),
  };
}

function displayDate(value: string): string {
  const parts = dateParts(value);
  return parts
    ? t("{{value1}}年{{value2}}月{{value3}}日", {
        value1: parts.year,
        value2: parts.month + 1,
        value3: parts.day,
      })
    : t("选择日期");
}

function monthDays(year: number, month: number): CalendarDay[] {
  const firstWeekday = new Date(Date.UTC(year, month, 1)).getUTCDay();
  return Array.from({ length: 42 }, (_, index) => {
    const date = new Date(Date.UTC(year, month, index - firstWeekday + 1));
    return {
      date: canonicalDate(
        date.getUTCFullYear(),
        date.getUTCMonth(),
        date.getUTCDate(),
      ),
      day: date.getUTCDate(),
      inMonth: date.getUTCMonth() === month,
    };
  });
}

export function DatePicker({
  align = "start",
  id,
  label,
  now = new Date(),
  onChange,
  value,
}: {
  align?: "start" | "end";
  id: string;
  label: string;
  now?: Date;
  onChange: (value: string) => void;
  value: string;
}) {
  const { t } = useTranslation();

  const fallbackId = useId();
  const dialogId = `${id || fallbackId}-calendar`;
  const current = dateParts(value) ?? todayParts(now);
  const today = todayParts(now);
  const todayValue = canonicalDate(today.year, today.month, today.day);
  const [open, setOpen] = useState(false);
  const [visibleMonth, setVisibleMonth] = useState({
    year: current.year,
    month: current.month,
  });
  const rootRef = useRef<HTMLDivElement>(null);
  const days = useMemo(
    () => monthDays(visibleMonth.year, visibleMonth.month),
    [visibleMonth],
  );

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePress = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        rootRef.current
          ?.querySelector<HTMLButtonElement>("[aria-haspopup]")
          ?.focus();
      }
    };
    document.addEventListener("pointerdown", closeOnOutsidePress);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutsidePress);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  const showMonth = (offset: number) => {
    const next = new Date(
      Date.UTC(visibleMonth.year, visibleMonth.month + offset, 1),
    );
    setVisibleMonth({ year: next.getUTCFullYear(), month: next.getUTCMonth() });
  };

  const choose = (nextValue: string) => {
    onChange(nextValue);
    setOpen(false);
    rootRef.current
      ?.querySelector<HTMLButtonElement>("[aria-haspopup]")
      ?.focus();
  };

  return (
    <div className={styles.picker} ref={rootRef}>
      <label id={`${id}-label`}>{label}</label>
      <button
        aria-controls={dialogId}
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-label={`${label}：${displayDate(value)}`}
        className={styles.trigger}
        id={id}
        onClick={() => {
          const next = dateParts(value) ?? todayParts(now);
          setVisibleMonth({ year: next.year, month: next.month });
          setOpen((currentOpen) => !currentOpen);
        }}
        type="button"
      >
        <span className={value ? undefined : styles.placeholder}>
          {displayDate(value)}
        </span>
        <Icon name="archive" />
      </button>
      {open ? (
        <div
          aria-labelledby={`${id}-label`}
          className={`${styles.popover} ${align === "end" ? styles.alignEnd : ""}`}
          id={dialogId}
          role="dialog"
        >
          <header>
            <strong>
              {visibleMonth.year}
              {t("年")}
              {visibleMonth.month + 1}
              {t("月")}
            </strong>
            <div>
              <button
                aria-label={t("上个月")}
                onClick={() => showMonth(-1)}
                type="button"
              >
                <Icon name="back" />
              </button>
              <button
                aria-label={t("下个月")}
                className={styles.nextMonth}
                onClick={() => showMonth(1)}
                type="button"
              >
                <Icon name="back" />
              </button>
            </div>
          </header>
          <div aria-hidden="true" className={styles.weekdays}>
            {WEEKDAYS.map((weekday) => (
              <span key={weekday}>{t(weekday)}</span>
            ))}
          </div>
          <div className={styles.days}>
            {days.map((item) => (
              <button
                aria-current={item.date === todayValue ? "date" : undefined}
                aria-label={displayDate(item.date)}
                aria-pressed={item.date === value}
                className={item.inMonth ? undefined : styles.outsideMonth}
                key={item.date}
                onClick={() => choose(item.date)}
                type="button"
              >
                {item.day}
              </button>
            ))}
          </div>
          <footer>
            <button disabled={!value} onClick={() => choose("")} type="button">
              {t("清除")}
            </button>
            <button onClick={() => choose(todayValue)} type="button">
              {t("今天")}
            </button>
          </footer>
        </div>
      ) : null}
    </div>
  );
}
