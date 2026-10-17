import {
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";

import {
  PublicApi,
  PublicApiError,
  type ArchiveMonthCount,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { useRailSlot } from "../../components/railSlot";
import {
  RAIL_VIEWPORT_QUERY,
  useMediaQuery,
} from "../../components/useMediaQuery";
import { siteToday } from "../timeline/timelineDays";
import { TimelinePage } from "../timeline/TimelinePage";
import { ArchiveCalendar } from "./ArchiveCalendar";
import {
  monthKeyOf,
  monthRange,
  positionAnchor,
  requestedMonth,
  shiftMonth,
  timezoneNote,
} from "./archiveDates";
import { parseArchiveLocation } from "./memoryState";
import styles from "./Memory.module.css";

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

/**
 * Browsing by date: the timeline, with a calendar beside it.
 *
 * **The archive is the list, not an index of it.** This page used to open on
 * a wall of month cards, put a calendar where the cards had been once one was
 * chosen, and then show exactly one day under the calendar -- three clicks to
 * the first Moment, and a dead end when the day held one. Now the list is on
 * screen from the start, and the calendar is the instrument that positions it.
 *
 * **A day is where the list starts, not what it is filtered to.** Picking the
 * 8th opens the timeline at the end of the 8th (`/posts?anchor=`) and it reads
 * both ways from there: into the 7th, the 6th and back below, and into the
 * 9th, the 10th and on above, each end loading as the reader nears it -- the
 * same lazy list the home page is, started somewhere else. The rows are the
 * timeline's own, dated in their bylines; there is no heading over each day,
 * which the reader asked for -- the byline already says it.
 *
 * **Where the calendar stands depends on the shell.** At desktop widths the
 * shell draws a right rail and the calendar goes there, sticky beside the list
 * -- the split the phone cannot afford. Narrower, it sits at the top of the
 * column and the list runs under it, which is the iOS screen.
 *
 * The calendar's month is the page's own state: the chevrons page it without
 * touching the list, and the URL reseats it whenever the position changes.
 */
export function ArchivePage({
  api,
  canPublish,
  search,
  site,
}: {
  api: PublicApi;
  canPublish: boolean;
  search: string;
  site: SiteIdentity;
}) {
  const state = useMemo(() => parseArchiveLocation(search), [search]);
  const railSlot = useRailSlot();
  const railViewport = useMediaQuery(RAIL_VIEWPORT_QUERY);
  const inRail = railViewport && railSlot !== null;
  const today = siteToday(site.timezone);
  const note = useMemo(() => timezoneNote(site.timezone), [site.timezone]);

  const [months, setMonths] = useState<ArchiveMonthCount[] | null>(null);
  const [monthsLoading, setMonthsLoading] = useState(!state.error);
  const [monthsError, setMonthsError] = useState<unknown>(null);
  const activeMonths = useRef<AbortController | null>(null);

  const loadMonths = useCallback(async () => {
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
  }, [api]);

  useEffect(() => {
    if (state.error) return;
    void loadMonths();
    return () => activeMonths.current?.abort();
  }, [loadMonths, state.error]);

  const monthCounts = useMemo(
    () => new Map((months ?? []).map((item) => [monthKeyOf(item), item.count])),
    [months],
  );
  const todayMonth = today.slice(0, 7);
  const range = useMemo(
    () => (months ? monthRange(months, todayMonth) : null),
    [months, todayMonth],
  );
  const wantedMonth =
    months && range ? requestedMonth(state, months, range) : null;
  const anchor = positionAnchor(state);
  const positionKey =
    state.day ?? state.month ?? (state.year === null ? "" : String(state.year));

  // The month on the calendar. Seated by the position whenever the position
  // (or the archive under it) changes, and otherwise left to the reader's
  // paging -- so loading another page of the list, which rewrites the URL's
  // progress, does not snap a calendar they have paged elsewhere.
  const [visibleMonth, setVisibleMonth] = useState<string | null>(null);
  const [seatedFor, setSeatedFor] = useState<string | null>(null);
  const seat = wantedMonth === null ? null : `${positionKey}|${wantedMonth}`;
  if (seat !== null && seat !== seatedFor) {
    setSeatedFor(seat);
    setVisibleMonth(wantedMonth);
  }

  // Per-day counts, kept for every month fetched so paging back is free, and
  // asked for a month early on either side of the visible one. A month's
  // counts failing to arrive is not the archive failing: that month draws
  // without links, and the reader can page away and back to ask again.
  const [dayCounts, setDayCounts] = useState<
    ReadonlyMap<string, ReadonlyMap<number, number>>
  >(() => new Map());
  const inFlight = useRef(new Set<string>());
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (visibleMonth === null) return;
    for (const target of [
      visibleMonth,
      shiftMonth(visibleMonth, -1),
      shiftMonth(visibleMonth, 1),
    ]) {
      if (
        !monthCounts.has(target) ||
        dayCounts.has(target) ||
        inFlight.current.has(target)
      ) {
        continue;
      }
      inFlight.current.add(target);
      api
        .archiveMonth(target)
        .then((result) => {
          if (!mounted.current) return;
          setDayCounts((current) =>
            new Map(current).set(
              target,
              new Map(result.days.map((item) => [item.day, item.count])),
            ),
          );
        })
        .catch(() => undefined)
        .finally(() => inFlight.current.delete(target));
    }
  }, [api, dayCounts, monthCounts, visibleMonth]);

  // The list is read with the anchor and whatever progress the URL carries in
  // either direction; the timeline puts the anchored row under the header
  // itself, on a phone where the calendar stands above it as on a desktop
  // where it stands beside it.
  const listSearch = useMemo(() => {
    const params = new URLSearchParams();
    if (anchor) params.set("anchor", anchor);
    if (state.loadedThrough) params.set("loaded_through", state.loadedThrough);
    if (state.loadedNewer) params.set("loaded_newer", state.loadedNewer);
    const query = params.toString();
    return query ? `?${query}` : "";
  }, [anchor, state.loadedNewer, state.loadedThrough]);

  const monthsEmpty = months !== null && months.length === 0;
  let calendar: ReactNode = null;
  if (!state.error && !monthsEmpty) {
    if (monthsLoading) {
      calendar = (
        <section
          aria-busy="true"
          aria-label="正在加载归档"
          className={`${styles.calendarLoading} ${
            inRail ? styles.calendarRail : styles.calendarWide
          }`}
        >
          <span>正在加载归档…</span>
        </section>
      );
    } else if (monthsError) {
      calendar = (
        <ArchiveMessage
          action={
            <button onClick={() => void loadMonths()} type="button">
              重试归档
            </button>
          }
          alert
          detail={
            monthsError instanceof PublicApiError &&
            monthsError.code === "API_RESPONSE_INVALID"
              ? "返回的归档不完整，请重试。"
              : "请检查网络后重试。"
          }
          title="归档加载失败"
        />
      );
    } else if (months && range && visibleMonth) {
      calendar = (
        <ArchiveCalendar
          dayCounts={dayCounts.get(visibleMonth) ?? null}
          monthCounts={monthCounts}
          months={months}
          note={note}
          onVisibleMonthChange={setVisibleMonth}
          positioned={anchor !== null}
          range={range}
          selectedDay={state.day}
          today={today}
          variant={inRail ? "rail" : "column"}
          visibleMonth={visibleMonth}
        />
      );
    }
  }

  return (
    <div className={styles.memoryPage}>
      {calendar !== null && inRail && railSlot
        ? createPortal(calendar, railSlot)
        : null}
      {calendar !== null && !inRail ? (
        <div className={styles.calendarColumn}>{calendar}</div>
      ) : null}
      {state.error ? (
        <ArchiveMessage
          action={<InternalLink href="/archive">回到归档</InternalLink>}
          alert
          detail="请从归档重新选择日期。"
          title="归档链接无效"
        />
      ) : null}
      {monthsEmpty ? (
        <ArchiveMessage
          action={<InternalLink href="/">回到时间流</InternalLink>}
          detail="发布第一条 Moment 后，可以在这里按日期回看。"
          title="还没有 Moment"
        />
      ) : null}
      {!state.error && !monthsEmpty ? (
        <div className={styles.archiveList}>
          <TimelinePage
            api={api}
            emptyContext="archive"
            initialSearch={listSearch}
            key={anchor ?? "latest"}
            canPublish={canPublish}
            site={site}
          />
        </div>
      ) : null}
    </div>
  );
}
