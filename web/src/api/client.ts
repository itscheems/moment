import { decodeResponse, PublicApiError } from "./public";

export type RegistrationMode = "open" | "approval" | "closed";

export interface PostMediaInput {
  mediaId: string;
  position: number;
  altText: string | null;
}

export type LocationProvider = "manual" | "amap" | "geoapify";

export type CoordinateSystem = "wgs84" | "gcj02";

export interface LinkCardInput {
  source_url: string;
  canonical_url: string;
  provider: string;
  card_kind: string;
  title: string;
  description: string;
  site_name: string;
  author_name: string;
  duration_seconds: number | null;
  view_count: number | null;
  comment_count: number | null;
  like_count: number | null;
  favicon_media_id: string | null;
  thumbnail_media_id: string | null;
  thumbnail_mime_type: string | null;
  thumbnail_width: number | null;
  thumbnail_height: number | null;
  embed_url: string | null;
  captured_at_ms: number | null;
}

export interface LocationInput {
  name: string;
  displayName: string;
  countryCode: string;
  country: string;
  region: string;
  city: string;
  district: string;
  category: string;
  provider: LocationProvider;
  providerPlaceId: string;
  latitude: number | null;
  longitude: number | null;
  coordinateSystem: CoordinateSystem | null;
}

export type Fetcher = typeof fetch;

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function invalidResponse(message: string): PublicApiError {
  return new PublicApiError(message, {
    code: "API_RESPONSE_INVALID",
    status: 0,
  });
}

export function stringField(
  record: Record<string, unknown>,
  field: string,
): string {
  const value = record[field];
  if (typeof value !== "string") {
    throw invalidResponse(`response field ${field} must be a string`);
  }
  return value;
}

export function booleanField(
  record: Record<string, unknown>,
  field: string,
): boolean {
  const value = record[field];
  if (typeof value !== "boolean") {
    throw invalidResponse(`response field ${field} must be a boolean`);
  }
  return value;
}

export function objectData(data: unknown): Record<string, unknown> {
  if (!isRecord(data)) throw invalidResponse("response data must be an object");
  return data;
}

export function hasExactKeys(
  record: Record<string, unknown>,
  expected: string[],
) {
  const actual = Object.keys(record).sort();
  const sortedExpected = [...expected].sort();
  return (
    actual.length === sortedExpected.length &&
    actual.every((value, index) => value === sortedExpected[index])
  );
}

export function positiveIntegerField(
  record: Record<string, unknown>,
  field: string,
): number {
  const value = record[field];
  if (!Number.isSafeInteger(value) || Number(value) <= 0) {
    throw invalidResponse(`response field ${field} must be a positive integer`);
  }
  return Number(value);
}

export function nonNegativeIntegerField(
  record: Record<string, unknown>,
  field: string,
): number {
  const value = record[field];
  if (!Number.isSafeInteger(value) || Number(value) < 0) {
    throw invalidResponse(
      `response field ${field} must be a non-negative safe integer`,
    );
  }
  return Number(value);
}

export function utcInstantField(
  record: Record<string, unknown>,
  field: string,
): string {
  const value = stringField(record, field);
  const match = value.match(
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/,
  );
  if (!match) {
    throw invalidResponse(`response field ${field} must be a UTC instant`);
  }
  const [, yearText, monthText, dayText, hourText, minuteText, secondText] =
    match;
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const hour = Number(hourText);
  const minute = Number(minuteText);
  const second = Number(secondText);
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const monthDays = [
    31,
    leapYear ? 29 : 28,
    31,
    30,
    31,
    30,
    31,
    31,
    30,
    31,
    30,
    31,
  ];
  if (
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > monthDays[month - 1] ||
    hour > 23 ||
    minute > 59 ||
    second > 59
  ) {
    throw invalidResponse(`response field ${field} must be a UTC instant`);
  }
  return value;
}

export async function decodeExactSuccess(
  response: Response,
  dataKeys: string[],
  optionalDataKeys: string[] = [],
): Promise<Record<string, unknown>> {
  const result = await decodeResponse(response, {
    strictSuccessEnvelope: response.ok,
    requireJsonSuccess: response.ok,
  });
  if (response.status !== 200) {
    throw invalidResponse("account API success must return HTTP 200");
  }
  // mirrors the Success envelope's meta (internal/transport/http/gin/response.go)
  if (
    !hasExactKeys(result.meta, ["request_id"]) ||
    !stringField(result.meta, "request_id")
  ) {
    throw invalidResponse("account API metadata is invalid");
  }
  const data = objectData(result.data);
  const expectedDataKeys = [
    ...dataKeys,
    ...optionalDataKeys.filter((key) => key in data),
  ];
  // mirrors ginhttp.identityView (internal/transport/http/gin/accounts.go)
  if (!hasExactKeys(data, expectedDataKeys)) {
    throw invalidResponse("account API data contains unsupported fields");
  }
  return data;
}

export function mediaPayload(
  media: PostMediaInput[],
): Record<string, unknown>[] {
  return media.map((usage, position) => ({
    media_id: usage.mediaId,
    position,
    alt_text: usage.altText,
  }));
}

export function nullableNumberField(
  record: Record<string, unknown>,
  field: string,
): number | null {
  const value = record[field];
  if (
    value !== null &&
    (typeof value !== "number" || !Number.isFinite(value))
  ) {
    throw invalidResponse(`response field ${field} must be a number or null`);
  }
  return value as number | null;
}

export function parseLocationInput(value: unknown): LocationInput {
  if (!isRecord(value)) throw invalidResponse("location must be an object");
  const keys = [
    "name",
    "display_name",
    "country_code",
    "country",
    "region",
    "city",
    "district",
    "category",
    "provider",
    "provider_place_id",
    "latitude",
    "longitude",
    "coordinate_system",
  ];
  // mirrors the caller's Go type; see each exactObject call
  if (!hasExactKeys(value, keys)) {
    throw invalidResponse("location contains unsupported fields");
  }
  const provider = stringField(value, "provider");
  if (!["manual", "amap", "geoapify"].includes(provider)) {
    throw invalidResponse("location provider is invalid");
  }
  const coordinate = value.coordinate_system;
  if (coordinate !== null && coordinate !== "wgs84" && coordinate !== "gcj02") {
    throw invalidResponse("location coordinate system is invalid");
  }
  const latitude = nullableNumberField(value, "latitude");
  const longitude = nullableNumberField(value, "longitude");
  if (
    (latitude === null) !== (longitude === null) ||
    (latitude === null) !== (coordinate === null)
  ) {
    throw invalidResponse(
      "location coordinate fields must be supplied together",
    );
  }
  return {
    name: stringField(value, "name"),
    displayName: stringField(value, "display_name"),
    countryCode: stringField(value, "country_code"),
    country: stringField(value, "country"),
    region: stringField(value, "region"),
    city: stringField(value, "city"),
    district: stringField(value, "district"),
    category: stringField(value, "category"),
    provider: provider as LocationProvider,
    providerPlaceId: stringField(value, "provider_place_id"),
    latitude,
    longitude,
    coordinateSystem: coordinate as CoordinateSystem | null,
  };
}

export function locationPayload(
  location: LocationInput,
): Record<string, unknown> {
  return {
    name: location.name,
    display_name: location.displayName,
    country_code: location.countryCode,
    country: location.country,
    region: location.region,
    city: location.city,
    district: location.district,
    category: location.category,
    provider: location.provider,
    provider_place_id: location.providerPlaceId,
    latitude: location.latitude,
    longitude: location.longitude,
    coordinate_system: location.coordinateSystem,
  };
}

export function requestInit(
  method: string,
  csrfToken?: string,
  body?: unknown,
): RequestInit {
  const headers = new Headers({ Accept: "application/json" });
  if (body !== undefined) headers.set("Content-Type", "application/json");
  if (csrfToken) headers.set("X-CSRF-Token", csrfToken);
  return {
    method,
    credentials: "same-origin",
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  };
}

export class ApiClient {
  protected readonly fetcher: Fetcher;
  constructor(fetcher: Fetcher = (input, init) => window.fetch(input, init)) {
    this.fetcher = fetcher;
  }
}
