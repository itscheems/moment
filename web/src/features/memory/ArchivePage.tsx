import {
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  PublicApi,
  PublicApiError,
  type ArchiveMonth,
  type ArchiveMonthCount,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { TimelinePage } from "../timeline/TimelinePage";
import {
  archiveHref,
  dateForMonthDay,
  nextDate,
  parseArchiveLocation,
} from "./memoryState";
import styles from "./Memory.module.css";

const weekDays = ["一", "二", "三", "四", "五", "六", "日"];

function ArchiveMessage({
  title,
  detail,
  action,
  alert = false,
}: {
  title: string;
  detail: string;
  action?: ReactNode;
  alert?: boolean;
}) {
  return (
    <section className={styles.message} role={alert ? "alert" : "status"}>
      <div>
        <h2>{title}</h2>
        <p>{detail}</p>
        {action}
      </div>
    </section>
  );
}

function todayInTimezone(timezone: string, now = new Date()): string {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: timezone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(now);
  const value = (type: "year" | "month" | "day") =>
    parts.find((part) => part.type === type)?.value ?? "";
  return `${value("year")}-${value("month")}-${value("day")}`;
}

function archiveDayLabel(value: string): string {
  const [, month, day] = value.split("-").map(Number);
  return `${month} 月 ${day} 日`;
}

function monthKey(item: Pick<ArchiveMonthCount, "year" | "month">): string {
  return `${String(item.year).padStart(4, "0")}-${String(item.month).padStart(2, "0")}`;
}

function Calendar({
  calendar,
  selectedDay,
  today,
  year,
}: {
  calendar: ArchiveMonth;
  selectedDay: string | null;
  today: string;
  year: number;
}) {
  const [yearText, monthText] = calendar.month.split("-");
  const firstWeekDay = new Date(
    Date.UTC(Number(yearText), Number(monthText) - 1, 1),
  ).getUTCDay();
  const mondayOffset = (firstWeekDay + 6) % 7;
  const cells: ReactNode[] = Array.from(
    { length: mondayOffset },
    (_, index) => (
      <li
        aria-hidden="true"
        className={styles.calendarBlank}
        key={`blank-${index}`}
      />
    ),
  );
  const counts = new Map(calendar.days.map((item) => [item.day, item.count]));
  const daysInMonth = new Date(
    Date.UTC(Number(yearText), Number(monthText), 0),
  ).getUTCDate();
  for (let day = 1; day <= daysInMonth; day += 1) {
    const count = counts.get(day);
    const date = dateForMonthDay(calendar.month, day);
    const isToday = date === today;
    const isSelected = selectedDay === date;
    cells.push(
      <li
        className={count ? styles.calendarDay : styles.calendarEmptyDay}
        key={day}
      >
        {count ? (
          <InternalLink
            aria-current={
              isSelected || (isToday && !selectedDay) ? "date" : undefined
            }
            aria-label={`${date}${isToday ? "，今天" : ""}，${count} 条 Moment${isSelected ? "，已选择" : ""}`}
            data-focus-key={`archive-day-${date}`}
            data-selected={isSelected ? "true" : undefined}
            href={archiveHref({
              year,
              month: calendar.month,
              from: date,
              to: nextDate(date),
            })}
          >
            <strong>{day}</strong>
            <span>{count}</span>
          </InternalLink>
        ) : (
          <span aria-label={`${date}${isToday ? "，今天" : ""}，没有 Moment`}>
            {day}
          </span>
        )}
      </li>,
    );
  }
  return (
    <section
      aria-labelledby="archive-calendar-title"
      className={styles.calendarSection}
    >
      <header>
        <h2 id="archive-calendar-title">
          {Number(yearText)} 年 {Number(monthText)} 月
        </h2>
        <InternalLink href={archiveHref({ year })}>← {year} 年</InternalLink>
      </header>
      <ol aria-hidden="true" className={styles.weekHeader}>
        {weekDays.map((day) => (
          <li key={day}>{day}</li>
        ))}
      </ol>
      <ol className={styles.calendarGrid}>{cells}</ol>
    </section>
  );
}

export function ArchivePage({
  api,
  owner,
  search,
  site,
}: {
  api: PublicApi;
  owner: boolean;
  search: string;
  site: SiteIdentity;
}) {
  const location = useNavigation();
  const stateRef = useRef(parseArchiveLocation(search));
  const state = stateRef.current;
  const [months, setMonths] = useState<ArchiveMonthCount[]>([]);
  const [monthsLoading, setMonthsLoading] = useState(!state.error);
  const [monthsError, setMonthsError] = useState<unknown>(null);
  const [calendar, setCalendar] = useState<ArchiveMonth | null>(null);
  const [calendarLoading, setCalendarLoading] = useState(Boolean(state.month));
  const [calendarError, setCalendarError] = useState<unknown>(null);
  const activeMonths = useRef<AbortController | null>(null);
  const activeCalendar = useRef<AbortController | null>(null);
  const entryRestored = useRef(false);

  const loadMonths = useCallback(async () => {
    if (state.error) return;
    activeMonths.current?.abort();
    const controller = new AbortController();
    activeMonths.current = controller;
    setMonthsLoading(true);
    setMonthsError(null);
    try {
      const result = await api.archiveMonths({ signal: controller.signal });
      if (!controller.signal.aborted) setMonths(result.items);
    } catch (error) {
      if (!controller.signal.aborted) setMonthsError(error);
    } finally {
      if (!controller.signal.aborted) setMonthsLoading(false);
    }
  }, [api, state.error]);

  const loadCalendar = useCallback(async () => {
    if (!state.month || state.error) return;
    activeCalendar.current?.abort();
    const controller = new AbortController();
    activeCalendar.current = controller;
    setCalendarLoading(true);
    setCalendarError(null);
    try {
      const result = await api.archiveMonth(state.month, controller.signal);
      if (!controller.signal.aborted) setCalendar(result);
    } catch (error) {
      if (!controller.signal.aborted) setCalendarError(error);
    } finally {
      if (!controller.signal.aborted) setCalendarLoading(false);
    }
  }, [api, state.error, state.month]);

  useEffect(() => {
    void loadMonths();
    void loadCalendar();
    return () => {
      activeMonths.current?.abort();
      activeCalendar.current?.abort();
    };
  }, [loadCalendar, loadMonths]);

  const years = useMemo(
    () => Array.from(new Set(months.map((item) => item.year))),
    [months],
  );

  useEffect(() => {
    const ready =
      !state.error &&
      !monthsLoading &&
      !monthsError &&
      (!state.month || (!calendarLoading && !calendarError));
    if (!ready || entryRestored.current) return;
    entryRestored.current = true;
    location.restoreCurrentEntry();
  }, [
    calendarError,
    calendarLoading,
    location,
    monthsError,
    monthsLoading,
    state.error,
    state.month,
  ]);

  const grouped = useMemo(
    () =>
      years
        .filter((year) => state.year === null || year === state.year)
        .map((year) => ({
          year,
          months: months.filter((item) => item.year === year),
        })),
    [months, state.year, years],
  );
  const selectedDay =
    state.from && state.to === nextDate(state.from) ? state.from : null;
  const selectedRange = Boolean(state.from || state.to);
  const invalidResponse =
    monthsError instanceof PublicApiError &&
    monthsError.code === "API_RESPONSE_INVALID";
  const today = todayInTimezone(site.timezone);

  return (
    <div className={styles.memoryPage}>
      {state.error ? (
        <ArchiveMessage
          action={<InternalLink href="/archive">返回全部年份</InternalLink>}
          alert
          detail="请从归档首页重新选择。"
          title="归档链接无效"
        />
      ) : null}
      {monthsLoading ? (
        <section
          aria-busy="true"
          aria-label="正在加载归档"
          className={styles.loading}
        >
          <span>正在加载归档…</span>
        </section>
      ) : null}
      {!monthsLoading && monthsError ? (
        <ArchiveMessage
          action={
            <button onClick={() => void loadMonths()} type="button">
              重试归档
            </button>
          }
          alert
          detail={
            invalidResponse
              ? "返回的归档不完整，请重试。"
              : "请检查网络后重试。"
          }
          title="归档加载失败"
        />
      ) : null}
      {!monthsLoading && !monthsError && months.length === 0 ? (
        <ArchiveMessage
          action={<InternalLink href="/">回到时间流</InternalLink>}
          detail="发布第一条 Moment 后，可以在这里按日期查找。"
          title="还没有 Moment"
        />
      ) : null}

      {!monthsLoading && !monthsError && months.length > 0 ? (
        <>
          {!state.month ? (
            grouped.length > 0 ? (
              <section
                aria-label="有内容的年月"
                className={styles.archiveYears}
              >
                {grouped.map((group) => (
                  <section
                    aria-labelledby={`archive-year-${group.year}`}
                    key={group.year}
                  >
                    <header>
                      <h2 id={`archive-year-${group.year}`}>{group.year}</h2>
                      <span>
                        {group.months.reduce(
                          (sum, item) => sum + item.count,
                          0,
                        )}{" "}
                        条
                      </span>
                    </header>
                    <ol>
                      {group.months.map((item) => (
                        <li key={monthKey(item)}>
                          <InternalLink
                            data-focus-key={`archive-month-${monthKey(item)}`}
                            href={archiveHref({
                              year: item.year,
                              month: monthKey(item),
                            })}
                          >
                            <strong>{item.month} 月</strong>
                            <span>{item.count} 条 Moment</span>
                          </InternalLink>
                        </li>
                      ))}
                    </ol>
                  </section>
                ))}
              </section>
            ) : (
              <ArchiveMessage
                action={
                  <InternalLink href="/archive">查看全部年份</InternalLink>
                }
                detail="返回全部年份继续查找。"
                title={`${state.year} 年没有 Moment`}
              />
            )
          ) : null}

          {state.month && calendarLoading ? (
            <section
              aria-busy="true"
              aria-label="正在加载月历"
              className={styles.loading}
            >
              <span>正在加载日历…</span>
            </section>
          ) : null}
          {state.month && !calendarLoading && calendarError ? (
            <ArchiveMessage
              action={
                <button onClick={() => void loadCalendar()} type="button">
                  重试月历
                </button>
              }
              alert
              detail="请重试或返回年份列表。"
              title="日历加载失败"
            />
          ) : null}
          {state.month && !calendarLoading && !calendarError && calendar ? (
            <Calendar
              calendar={calendar}
              selectedDay={selectedDay}
              today={today}
              year={state.year!}
            />
          ) : null}

          {selectedRange && !calendarLoading && !calendarError ? (
            <section
              aria-labelledby="archive-day-results"
              className={styles.dayResults}
            >
              <header>
                <h2 id="archive-day-results">
                  {selectedDay ? archiveDayLabel(selectedDay) : "所选日期"}
                </h2>
                <InternalLink
                  href={archiveHref({ year: state.year, month: state.month })}
                >
                  清除日期
                </InternalLink>
              </header>
              <TimelinePage
                api={api}
                initialSearch={search}
                owner={owner}
                site={site}
              />
            </section>
          ) : null}
        </>
      ) : null}
    </div>
  );
}
