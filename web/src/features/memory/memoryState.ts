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

export interface ArchiveLocationState {
  year: number | null;
  month: string | null;
  from: string | null;
  to: string | null;
  loadedThrough: string | null;
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

function validCursor(value: string | null): boolean {
  return value === null || (value.length > 0 && value.length <= 4_096);
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
  const loaded = single(params, "loaded_through");
  const query = q.value;
  const invalid =
    q.error ||
    from.error ||
    to.error ||
    loaded.error ||
    unsupported(params, new Set(["q", "from", "to", "loaded_through"])) ||
    (query !== null && (!query.trim() || Array.from(query).length > 1_000)) ||
    !validDateRange(from.value, to.value) ||
    !validCursor(loaded.value) ||
    (loaded.value !== null && query === null);
  return {
    query,
    from: from.value,
    to: to.value,
    loadedThrough: loaded.value,
    error: invalid ? SEARCH_LOCATION_ERROR : null,
  };
}

export function parseArchiveLocation(search: string): ArchiveLocationState {
  if (!hasValidRawQueryEncoding(search)) {
    return {
      year: null,
      month: null,
      from: null,
      to: null,
      loadedThrough: null,
      error: ARCHIVE_LOCATION_ERROR,
    };
  }
  const params = new URLSearchParams(search);
  const yearValue = single(params, "year");
  const month = single(params, "month");
  const from = single(params, "from");
  const to = single(params, "to");
  const loaded = single(params, "loaded_through");
  const monthMatch = month.value?.match(MONTH_PATTERN);
  const derivedYear = monthMatch ? Number(month.value?.slice(0, 4)) : null;
  const explicitYear = yearValue.value ? Number(yearValue.value) : null;
  const year = explicitYear ?? derivedYear;
  const parsedMonth = month.value ? Number(month.value.slice(5, 7)) : null;
  const hasDateBoundary = from.value !== null || to.value !== null;
  const canonicalDayRange =
    from.value !== null &&
    to.value !== null &&
    month.value !== null &&
    from.value.startsWith(`${month.value}-`) &&
    nextDate(from.value) === to.value;
  const invalid =
    yearValue.error ||
    month.error ||
    from.error ||
    to.error ||
    loaded.error ||
    unsupported(
      params,
      new Set(["year", "month", "from", "to", "loaded_through"]),
    ) ||
    (yearValue.value !== null &&
      (!YEAR_PATTERN.test(yearValue.value) || explicitYear === 0)) ||
    (month.value !== null &&
      (!monthMatch ||
        derivedYear === 0 ||
        parsedMonth === null ||
        parsedMonth < 1 ||
        parsedMonth > 12)) ||
    (explicitYear !== null &&
      derivedYear !== null &&
      explicitYear !== derivedYear) ||
    !validDateRange(from.value, to.value) ||
    (hasDateBoundary && !canonicalDayRange) ||
    !validCursor(loaded.value) ||
    (loaded.value !== null && !canonicalDayRange);
  return {
    year,
    month: month.value,
    from: from.value,
    to: to.value,
    loadedThrough: loaded.value,
    error: invalid ? ARCHIVE_LOCATION_ERROR : null,
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

export function archiveHref(input: {
  year?: number | null;
  month?: string | null;
  from?: string | null;
  to?: string | null;
}): string {
  const params = new URLSearchParams();
  if (input.year) params.set("year", String(input.year));
  if (input.month) params.set("month", input.month);
  if (input.from) params.set("from", input.from);
  if (input.to) params.set("to", input.to);
  const query = params.toString();
  return query ? `/archive?${query}` : "/archive";
}
