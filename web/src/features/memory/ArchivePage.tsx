import {
  type MouseEventHandler,
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
  type ArchiveMonthCount,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { useReadRecovery } from "../../components/useReadRecovery";
import { useRailSlot } from "../../components/railSlot";
import {
  RAIL_VIEWPORT_QUERY,
  useMediaQuery,
} from "../../components/useMediaQuery";
import { siteDate, siteToday } from "../timeline/timelineDays";
import { TimelinePage } from "../timeline/TimelinePage";
import { ReadRecoveryToast } from "../timeline/PublicStates";
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
import {
  archiveJumpDirection,
  archiveWindowSearch,
  archiveRestorePosition,
  type ArchiveJumpRequest,
} from "./archiveMotion";
import styles from "./Memory.module.css";

function archiveListSearch(search: string): string {
  const state = parseArchiveLocation(search);
  const params = new URLSearchParams();
  const anchor = positionAnchor(state);
  if (anchor) params.set("anchor", anchor);
  if (state.loadedThrough) params.set("loaded_through", state.loadedThrough);
  if (state.loadedNewer) params.set("loaded_newer", state.loadedNewer);
  return archiveWindowSearch(params.size ? `?${params}` : "");
}

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
  const { pathname, navigate } = useNavigation();
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
  const monthsRecovering = useReadRecovery({
    error: monthsError,
    loading: monthsLoading,
    onRetry: loadMonths,
    enabled: pathname === "/archive" && !state.error,
  });

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
  const [jump, setJump] = useState<ArchiveJumpRequest | null>(null);
  const jumpId = useRef(0);
  useEffect(() => {
    const restore = (event: PopStateEvent) => {
      if (window.location.pathname !== "/archive") return;
      setJump({
        id: ++jumpId.current,
        search: archiveListSearch(window.location.search),
        direction: null,
        restore: archiveRestorePosition(event.state),
      });
    };
    window.addEventListener("popstate", restore);
    return () => window.removeEventListener("popstate", restore);
  }, []);
  const onNavigate: MouseEventHandler<HTMLAnchorElement> = (event) => {
    if (
      !event.defaultPrevented &&
      event.button === 0 &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.shiftKey &&
      !event.altKey
    ) {
      const headerBottom =
        document
          .querySelector("#main-content > header")
          ?.getBoundingClientRect().bottom ?? 0;
      const visible = Array.from(
        document.querySelectorAll<HTMLElement>("[data-timeline-at]"),
      ).find((row) => {
        const bounds = row.getBoundingClientRect();
        return (
          bounds.bottom > headerBottom + 1 && bounds.top < window.innerHeight
        );
      });
      const from = visible?.dataset.timelineAt
        ? siteDate(visible.dataset.timelineAt, site.timezone)
        : (anchor ?? today);
      const target = new URL(event.currentTarget.href);
      const to = positionAnchor(parseArchiveLocation(target.search));
      const current = new URLSearchParams(window.location.search);
      current.delete("loaded_through");
      current.delete("loaded_newer");
      const repeat = current.toString() === target.searchParams.toString();
      event.preventDefault();
      if (!repeat)
        navigate(`${target.pathname}${target.search}`, {
          preserveScroll: true,
        });
      setJump({
        id: ++jumpId.current,
        search: to ? `?anchor=${to}` : "",
        repeat,
        direction: archiveJumpDirection(from, to),
      });
    }
  };

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
  // counts failing to arrive is not the archive failing: the calendar stays
  // visible while the missing counts are requested again in the background.
  const [dayCounts, setDayCounts] = useState<
    ReadonlyMap<string, ReadonlyMap<number, number>>
  >(() => new Map());
  const inFlight = useRef(new Set<string>());
  const [dayErrors, setDayErrors] = useState<ReadonlyMap<string, unknown>>(
    () => new Map(),
  );
  const [dayAttempt, setDayAttempt] = useState(0);
  const wantedDayMonths = useMemo(
    () =>
      visibleMonth === null
        ? []
        : [
            visibleMonth,
            shiftMonth(visibleMonth, -1),
            shiftMonth(visibleMonth, 1),
          ].filter((month) => monthCounts.has(month)),
    [visibleMonth, monthCounts],
  );
  const dayError =
    wantedDayMonths.map((month) => dayErrors.get(month)).find(Boolean) ?? null;
  useReadRecovery({
    error: dayError,
    loading: false,
    onRetry: () => setDayAttempt((value) => value + 1),
    enabled: pathname === "/archive",
    scope: visibleMonth ?? "",
  });
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (visibleMonth === null) return;
    for (const target of wantedDayMonths) {
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
          setDayErrors((current) => {
            const next = new Map(current);
            next.delete(target);
            return next;
          });
          setDayCounts((current) =>
            new Map(current).set(
              target,
              new Map(result.days.map((item) => [item.day, item.count])),
            ),
          );
        })
        .catch((error: unknown) => {
          if (mounted.current)
            setDayErrors((current) => new Map(current).set(target, error));
        })
        .finally(() => inFlight.current.delete(target));
    }
  }, [api, dayCounts, monthCounts, visibleMonth, wantedDayMonths, dayAttempt]);

  // The list is read with the anchor and whatever progress the URL carries in
  // either direction; the timeline puts the anchored row under the header
  // itself, on a phone where the calendar stands above it as on a desktop
  // where it stands beside it.
  const listSearch = useMemo(() => archiveListSearch(search), [search]);

  const monthsEmpty = months !== null && months.length === 0;
  let calendar: ReactNode = null;
  if (!state.error && !monthsEmpty) {
    if (monthsLoading && months === null) {
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
    } else if (monthsError && months === null && monthsRecovering) {
      calendar = (
        <ReadRecoveryToast active={pathname === "/archive"}>
          正在重新获取归档…
        </ReadRecoveryToast>
      );
    } else if (monthsError && months === null) {
      calendar = (
        <ArchiveMessage
          alert
          detail="请确认登录状态和访问链接。"
          title="暂时无法查看归档"
        />
      );
    } else if (months && range && visibleMonth) {
      calendar = (
        <ArchiveCalendar
          dayCounts={dayCounts.get(visibleMonth) ?? null}
          monthCounts={monthCounts}
          months={months}
          note={note}
          onNavigate={onNavigate}
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
    <div className={styles.memoryPage} data-archive-page>
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
            archiveJump={jump}
            canPublish={canPublish}
            site={site}
          />
        </div>
      ) : null}
    </div>
  );
}
