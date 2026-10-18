import { loadedThroughFromSearch } from "../timeline/cursorContinuity";

const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/;
const MONTH_PATTERN = /^\d{4}-\d{2}$/;
const YEAR_PATTERN = /^\d{4}$/;
const SEARCH_LOCATION_ERROR =
  "链接里的搜索词、日期或分页位置无效。请调整条件后重新搜索。";
const ARCHIVE_LOCATION_ERROR =
  "链接里的年份、月份、日期或分页位置无效。请从归档首页重新选择。";

export interface SearchLocationState {
  query: string | null;
  from: string | null;
  to: string | null;
  loadedThrough: string | null;
  error: string | null;
}

/**
 * Where the archive's list starts. At most one of the three is set: a day says
 * everything a month would, and a month everything a year would. The list runs
 * back from the end of it, so an archive opened without one starts at the
 * newest post.
 */
export interface ArchiveLocationState {
  day: string | null;
  month: string | null;
  year: number | null;
  loadedThrough: string | null;
  loadedNewer: string | null;
  error: string | null;
}

function single(
  params: URLSearchParams,
  name: string,
): { value: string | null; error: boolean } {
  const values = params.getAll(name);
  return { value: values[0] ?? null, error: values.length > 1 };
}

export function isCanonicalDate(value: string): boolean {
  if (!DATE_PATTERN.test(value)) return false;
  const [year, month, day] = value.split("-").map(Number);
  const date = new Date(Date.UTC(year, month - 1, day));
  return (
    date.getUTCFullYear() === year &&
    date.getUTCMonth() === month - 1 &&
    date.getUTCDate() === day
  );
}

function validDateRange(from: string | null, to: string | null): boolean {
  return (
    (from === null || isCanonicalDate(from)) &&
    (to === null || isCanonicalDate(to)) &&
    (from === null || to === null || from < to)
  );
}

function unsupported(params: URLSearchParams, allowed: Set<string>): boolean {
  return Array.from(params.keys()).some((key) => !allowed.has(key));
}

export function hasValidRawQueryEncoding(search: string): boolean {
  const raw = search.startsWith("?") ? search.slice(1) : search;
  try {
    for (const component of raw.split(/[&=]/)) {
      decodeURIComponent(component.replace(/\+/g, "%20"));
    }
    return true;
  } catch {
    return false;
  }
}

export function parseSearchLocation(search: string): SearchLocationState {
  if (!hasValidRawQueryEncoding(search)) {
    return {
      query: null,
      from: null,
      to: null,
      loadedThrough: null,
      error: SEARCH_LOCATION_ERROR,
    };
  }
  const params = new URLSearchParams(search);
  const q = single(params, "q");
  const from = single(params, "from");
  const to = single(params, "to");
  const loaded = loadedThroughFromSearch(search);
  const query = q.value;
  const invalid =
    q.error ||
    from.error ||
    to.error ||
    unsupported(params, new Set(["q", "from", "to", "loaded_through"])) ||
    (query !== null && (!query.trim() || Array.from(query).length > 1_000)) ||
    !validDateRange(from.value, to.value);
  return {
    query,
    from: from.value,
    to: to.value,
    loadedThrough: query === null ? null : loaded,
    error: invalid ? SEARCH_LOCATION_ERROR : null,
  };
}

export function isCanonicalMonth(value: string): boolean {
  if (!MONTH_PATTERN.test(value)) return false;
  const year = Number(value.slice(0, 4));
  const month = Number(value.slice(5, 7));
  return year > 0 && month >= 1 && month <= 12;
}

const INVALID_ARCHIVE_LOCATION: ArchiveLocationState = {
  day: null,
  month: null,
  year: null,
  loadedThrough: null,
  loadedNewer: null,
  error: ARCHIVE_LOCATION_ERROR,
};

/**
 * Reads `/archive?day=` | `?month=` | `?year=`, plus the shape links carried
 * before the archive became a list: `year=&month=&from=D&to=D+1`, which named
 * one day as a window and still opens on that day. Anything the parameters
 * disagree about -- a month outside its year, a day outside its month, a
 * window that is not exactly one day -- is a broken link rather than a guess.
 */
export function parseArchiveLocation(search: string): ArchiveLocationState {
  if (!hasValidRawQueryEncoding(search)) return INVALID_ARCHIVE_LOCATION;
  const params = new URLSearchParams(search);
  const yearValue = single(params, "year");
  const monthValue = single(params, "month");
  const dayValue = single(params, "day");
  const from = single(params, "from");
  const to = single(params, "to");
  const loaded = loadedThroughFromSearch(search);
  const loadedNewer = loadedThroughFromSearch(search, "loaded_newer");
  if (
    yearValue.error ||
    monthValue.error ||
    dayValue.error ||
    from.error ||
    to.error ||
    unsupported(
      params,
      new Set([
        "year",
        "month",
        "day",
        "from",
        "to",
        "loaded_through",
        "loaded_newer",
      ]),
    )
  ) {
    return INVALID_ARCHIVE_LOCATION;
  }

  let year: number | null = null;
  if (yearValue.value !== null) {
    if (!YEAR_PATTERN.test(yearValue.value) || Number(yearValue.value) === 0) {
      return INVALID_ARCHIVE_LOCATION;
    }
    year = Number(yearValue.value);
  }

  let month: string | null = null;
  if (monthValue.value !== null) {
    if (!isCanonicalMonth(monthValue.value)) return INVALID_ARCHIVE_LOCATION;
    if (year !== null && Number(monthValue.value.slice(0, 4)) !== year) {
      return INVALID_ARCHIVE_LOCATION;
    }
    month = monthValue.value;
  }

  let day: string | null = null;
  if (dayValue.value !== null) {
    if (!isCanonicalDate(dayValue.value)) return INVALID_ARCHIVE_LOCATION;
    day = dayValue.value;
  }
  if (from.value !== null || to.value !== null) {
    if (
      from.value === null ||
      to.value === null ||
      !isCanonicalDate(from.value) ||
      nextDate(from.value) !== to.value ||
      (day !== null && day !== from.value)
    ) {
      return INVALID_ARCHIVE_LOCATION;
    }
    day = from.value;
  }
  if (day !== null) {
    if (month !== null && !day.startsWith(`${month}-`)) {
      return INVALID_ARCHIVE_LOCATION;
    }
    if (year !== null && Number(day.slice(0, 4)) !== year) {
      return INVALID_ARCHIVE_LOCATION;
    }
  }

  return {
    day,
    month: day === null ? month : null,
    year: day === null && month === null ? year : null,
    loadedThrough: loaded,
    loadedNewer,
    error: null,
  };
}

export function nextDate(value: string): string {
  const [year, month, day] = value.split("-").map(Number);
  const next = new Date(Date.UTC(year, month - 1, day + 1));
  return [
    String(next.getUTCFullYear()).padStart(4, "0"),
    String(next.getUTCMonth() + 1).padStart(2, "0"),
    String(next.getUTCDate()).padStart(2, "0"),
  ].join("-");
}

export function previousDate(value: string): string {
  const [year, month, day] = value.split("-").map(Number);
  const previous = new Date(Date.UTC(year, month - 1, day - 1));
  return [
    String(previous.getUTCFullYear()).padStart(4, "0"),
    String(previous.getUTCMonth() + 1).padStart(2, "0"),
    String(previous.getUTCDate()).padStart(2, "0"),
  ].join("-");
}

export function dateForMonthDay(month: string, day: number): string {
  return `${month}-${String(day).padStart(2, "0")}`;
}

export function activeSearchYear(
  from: string | null,
  to: string | null,
): number | null {
  if (!from || !to || !/^\d{4}-01-01$/.test(from)) return null;
  const year = Number(from.slice(0, 4));
  return to === `${String(year + 1).padStart(4, "0")}-01-01` ? year : null;
}

export function searchHref(input: {
  query: string;
  from?: string | null;
  to?: string | null;
  loadedThrough?: string | null;
}): string {
  const params = new URLSearchParams({ q: input.query });
  if (input.from) params.set("from", input.from);
  if (input.to) params.set("to", input.to);
  if (input.loadedThrough) params.set("loaded_through", input.loadedThrough);
  return `/search?${params}`;
}

/** The archive at a position: the most specific of a day, a month, a year. */
export function archiveHref(input: {
  day?: string | null;
  month?: string | null;
  year?: number | null;
}): string {
  if (input.day) return `/archive?day=${input.day}`;
  if (input.month) return `/archive?month=${input.month}`;
  if (input.year) return `/archive?year=${input.year}`;
  return "/archive";
}
