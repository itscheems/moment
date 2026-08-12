import {
  decodeCommentResponse,
  decodeCreatedComment,
  decodeResponse,
  isCanonicalUlid,
  parseComment,
  parseDeletedCommentMeta,
  parsePost,
  parseSiteIdentity,
  PublicApiError,
  type CreatedComment,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
} from "./public";

export interface OwnerSession {
  csrfToken: string;
  expiresAt: string;
}

export interface OwnerStatus {
  initialized: boolean;
  sessionEpoch: number;
  initializedAt: string | null;
  rotatedAt: string | null;
}

export interface RotatedCredential {
  token: string;
  initialized: boolean;
  sessionEpoch: number;
  initializedAt: string;
  rotatedAt: string;
}

export interface SiteSettingsUpdate {
  title?: string;
  bio?: string;
  timezone?: string;
  publicBaseUrl?: string | null;
  avatarMediaId?: string | null;
  coverMediaId?: string | null;
  commentsEnabled?: boolean;
}

export interface DeletedComment {
  comment: PublicComment;
  repliesAffected: number;
}

export interface UploadedMedia {
  id: string;
  byteSize: number;
  width: number;
  height: number;
  animatedOriginal: boolean;
  previewUrl: string;
  originalUrl: string;
}

export interface PostMediaInput {
  mediaId: string;
  position: number;
  altText: string | null;
}

export interface ImportCounts {
  entries: number;
  comments: number;
  media: number;
}

export interface ImportDiskEstimate {
  archiveBytes: number;
  expandedBytes: number;
  stagingBytes: number;
  preImportBackupBytes: number;
  rollbackReserveBytes: number;
  safetyMarginBytes: number;
  requiredAdditionalBytes: number;
  estimatedPeakFootprintBytes: number;
  availableBytes: number;
  spaceOK: boolean;
}

export interface ImportPlan {
  formatVersion: number;
  archiveSHA256: string;
  counts: ImportCounts;
  timeRange: { from: string | null; to: string | null };
  mediaBytes: number;
  missingAlt: number;
  disk: ImportDiskEstimate;
  replacement: {
    counts: ImportCounts;
    mediaBytes: number;
    dataBytes: number;
  };
  warnings: string[];
}

export type ImportPhase =
  | "claimed"
  | "backing_up"
  | "finalizing"
  | "switching"
  | "verifying"
  | "rolling_back"
  | "succeeded"
  | "failed"
  | "rolled_back"
  | "needs_attention";

export interface ImportStatus {
  operationId: string;
  phase: ImportPhase;
  activeGeneration: string;
  previousGeneration: string;
  newGeneration: string;
  backupVerified: boolean;
  rollbackAttempted: boolean;
  rollbackSucceeded: boolean;
  errorCode: string | null;
  startedAt: string;
  updatedAt: string;
  completedAt: string | null;
}

type Fetcher = typeof fetch;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function invalidResponse(message: string): PublicApiError {
  return new PublicApiError(message, {
    code: "API_RESPONSE_INVALID",
    status: 0,
  });
}

function stringField(record: Record<string, unknown>, field: string): string {
  const value = record[field];
  if (typeof value !== "string") {
    throw invalidResponse(`response field ${field} must be a string`);
  }
  return value;
}

function booleanField(record: Record<string, unknown>, field: string): boolean {
  const value = record[field];
  if (typeof value !== "boolean") {
    throw invalidResponse(`response field ${field} must be a boolean`);
  }
  return value;
}

function objectData(data: unknown): Record<string, unknown> {
  if (!isRecord(data)) throw invalidResponse("response data must be an object");
  return data;
}

function hasExactKeys(record: Record<string, unknown>, expected: string[]) {
  const actual = Object.keys(record).sort();
  const sortedExpected = [...expected].sort();
  return (
    actual.length === sortedExpected.length &&
    actual.every((value, index) => value === sortedExpected[index])
  );
}

function positiveIntegerField(
  record: Record<string, unknown>,
  field: string,
): number {
  const value = record[field];
  if (!Number.isSafeInteger(value) || Number(value) <= 0) {
    throw invalidResponse(`response field ${field} must be a positive integer`);
  }
  return Number(value);
}

function nonNegativeIntegerField(
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

function utcInstantField(
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

function optionalInstantField(
  record: Record<string, unknown>,
  field: string,
): string | null {
  return record[field] === undefined ? null : utcInstantField(record, field);
}

function canonicalUTCField(
  record: Record<string, unknown>,
  field: string,
): string {
  const value = utcInstantField(record, field);
  const fraction = value.match(/\.(\d+)Z$/)?.[1];
  if (fraction?.endsWith("0")) {
    throw invalidResponse(`response field ${field} must be canonical UTC`);
  }
  return value;
}

function canonicalUTCKey(value: string): string {
  const match = value.match(
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/,
  );
  if (!match) throw invalidResponse("UTC instant cannot be ordered");
  const [, year, month, day, hour, minute, second, fraction = ""] = match;
  return `${year}${month}${day}${hour}${minute}${second}${fraction.padEnd(9, "0")}`;
}

function compareCanonicalUTC(left: string, right: string): number {
  return canonicalUTCKey(left).localeCompare(canonicalUTCKey(right));
}

function parseImportCounts(value: unknown): ImportCounts {
  const record = objectData(value);
  if (!hasExactKeys(record, ["entries", "comments", "media"])) {
    throw invalidResponse("import counts contain unsupported fields");
  }
  return {
    entries: nonNegativeIntegerField(record, "entries"),
    comments: nonNegativeIntegerField(record, "comments"),
    media: nonNegativeIntegerField(record, "media"),
  };
}

function parseImportDisk(value: unknown): ImportDiskEstimate {
  const record = objectData(value);
  const fields = [
    "archive_bytes",
    "expanded_bytes",
    "staging_bytes",
    "pre_import_backup_bytes",
    "rollback_reserve_bytes",
    "safety_margin_bytes",
    "required_additional_bytes",
    "estimated_peak_footprint_bytes",
    "available_bytes",
    "space_ok",
  ];
  if (!hasExactKeys(record, fields)) {
    throw invalidResponse("import disk estimate contains unsupported fields");
  }
  const disk = {
    archiveBytes: nonNegativeIntegerField(record, "archive_bytes"),
    expandedBytes: nonNegativeIntegerField(record, "expanded_bytes"),
    stagingBytes: nonNegativeIntegerField(record, "staging_bytes"),
    preImportBackupBytes: nonNegativeIntegerField(
      record,
      "pre_import_backup_bytes",
    ),
    rollbackReserveBytes: nonNegativeIntegerField(
      record,
      "rollback_reserve_bytes",
    ),
    safetyMarginBytes: nonNegativeIntegerField(record, "safety_margin_bytes"),
    requiredAdditionalBytes: nonNegativeIntegerField(
      record,
      "required_additional_bytes",
    ),
    estimatedPeakFootprintBytes: nonNegativeIntegerField(
      record,
      "estimated_peak_footprint_bytes",
    ),
    availableBytes: nonNegativeIntegerField(record, "available_bytes"),
    spaceOK: booleanField(record, "space_ok"),
  };
  const required =
    disk.stagingBytes +
    disk.preImportBackupBytes +
    disk.rollbackReserveBytes +
    disk.safetyMarginBytes;
  if (
    !Number.isSafeInteger(required) ||
    required !== disk.requiredAdditionalBytes ||
    disk.spaceOK !== disk.availableBytes >= disk.requiredAdditionalBytes ||
    disk.estimatedPeakFootprintBytes < disk.requiredAdditionalBytes
  ) {
    throw invalidResponse("import disk estimate is internally inconsistent");
  }
  return disk;
}

function parseImportPlan(
  data: unknown,
  expectedArchiveBytes: number,
): ImportPlan {
  const record = objectData(data);
  if (
    !hasExactKeys(record, [
      "dry_run",
      "format_version",
      "archive_sha256",
      "counts",
      "time_range",
      "media_bytes",
      "missing_alt",
      "disk",
      "replacement",
      "warnings",
    ]) ||
    record.dry_run !== true ||
    record.format_version !== 1
  ) {
    throw invalidResponse("import plan contract is invalid");
  }
  const archiveSHA256 = stringField(record, "archive_sha256");
  if (!/^[0-9a-f]{64}$/.test(archiveSHA256)) {
    throw invalidResponse("import plan archive SHA-256 is invalid");
  }
  const timeRange = objectData(record.time_range);
  if (
    !hasExactKeys(
      timeRange,
      ["from", "to"].filter((field) => field in timeRange),
    )
  ) {
    throw invalidResponse("import time range contains unsupported fields");
  }
  const from =
    "from" in timeRange ? canonicalUTCField(timeRange, "from") : null;
  const to = "to" in timeRange ? canonicalUTCField(timeRange, "to") : null;
  if (
    (from === null) !== (to === null) ||
    (from && to && compareCanonicalUTC(from, to) > 0)
  ) {
    throw invalidResponse("import time range is inconsistent");
  }
  const replacement = objectData(record.replacement);
  if (!hasExactKeys(replacement, ["counts", "media_bytes", "data_bytes"])) {
    throw invalidResponse(
      "import replacement impact contains unsupported fields",
    );
  }
  if (
    !Array.isArray(record.warnings) ||
    record.warnings.some(
      (warning) => typeof warning !== "string" || warning.length > 2_000,
    )
  ) {
    throw invalidResponse("import warnings are invalid");
  }
  const counts = parseImportCounts(record.counts);
  const mediaBytes = nonNegativeIntegerField(record, "media_bytes");
  const missingAlt = nonNegativeIntegerField(record, "missing_alt");
  const disk = parseImportDisk(record.disk);
  const replacementCounts = parseImportCounts(replacement.counts);
  const replacementMediaBytes = nonNegativeIntegerField(
    replacement,
    "media_bytes",
  );
  const replacementDataBytes = nonNegativeIntegerField(
    replacement,
    "data_bytes",
  );
  const warnings = [...record.warnings];
  const jsonBytes = disk.expandedBytes - mediaBytes;
  const stagingBytes = mediaBytes + jsonBytes + jsonBytes;
  const baseRequired =
    stagingBytes + replacementDataBytes + replacementDataBytes;
  const safetyMargin = Math.max(Math.floor(baseRequired / 10), 2 ** 31);
  const requiredAdditional = baseRequired + safetyMargin;
  const peak = expectedArchiveBytes + replacementDataBytes + requiredAdditional;
  if (
    expectedArchiveBytes <= 0 ||
    counts.entries > 100_000 ||
    counts.comments > 500_000 ||
    counts.media > 100_000 ||
    replacementMediaBytes > replacementDataBytes ||
    mediaBytes > disk.expandedBytes ||
    disk.archiveBytes !== expectedArchiveBytes ||
    disk.expandedBytes <= 0 ||
    disk.stagingBytes !== stagingBytes ||
    disk.preImportBackupBytes !== replacementDataBytes ||
    disk.rollbackReserveBytes !== replacementDataBytes ||
    disk.safetyMarginBytes !== safetyMargin ||
    disk.requiredAdditionalBytes !== requiredAdditional ||
    disk.estimatedPeakFootprintBytes !== peak ||
    !disk.spaceOK ||
    ![stagingBytes, baseRequired, safetyMargin, requiredAdditional, peak].every(
      Number.isSafeInteger,
    )
  ) {
    throw invalidResponse("import preview relationships are invalid");
  }
  if (
    counts.entries === 0
      ? counts.comments !== 0 ||
        missingAlt !== 0 ||
        from !== null ||
        to !== null
      : from === null || to === null
  ) {
    throw invalidResponse("import preview time range is invalid");
  }
  const expectedWarning = `${missingAlt} media usages have no image description`;
  if (
    warnings.length > 1 ||
    (missingAlt === 0
      ? warnings.length !== 0
      : warnings.length !== 1 || warnings[0] !== expectedWarning)
  ) {
    throw invalidResponse("import warnings do not match the preview");
  }
  return {
    formatVersion: 1,
    archiveSHA256,
    counts,
    timeRange: { from, to },
    mediaBytes,
    missingAlt,
    disk,
    replacement: {
      counts: replacementCounts,
      mediaBytes: replacementMediaBytes,
      dataBytes: replacementDataBytes,
    },
    warnings,
  };
}

const importPhases = new Set<ImportPhase>([
  "claimed",
  "backing_up",
  "finalizing",
  "switching",
  "verifying",
  "rolling_back",
  "succeeded",
  "failed",
  "rolled_back",
  "needs_attention",
]);

function validImportOperationId(value: string): boolean {
  const match = value.match(
    /^import-(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})(\d{3})Z-([0-9a-f]{16})$/,
  );
  if (!match) return false;
  const [, year, month, day, hour, minute, second, milliseconds] = match;
  const instant = new Date(
    `${year}-${month}-${day}T${hour}:${minute}:${second}.${milliseconds}Z`,
  );
  return (
    Number.isFinite(instant.valueOf()) &&
    instant.toISOString() ===
      `${year}-${month}-${day}T${hour}:${minute}:${second}.${milliseconds}Z`
  );
}

function parseImportStatus(data: unknown): ImportStatus {
  const record = objectData(data);
  const required = [
    "operation_id",
    "phase",
    "active_generation",
    "previous_generation",
    "new_generation",
    "backup_verified",
    "rollback_attempted",
    "rollback_succeeded",
    "started_at",
    "updated_at",
  ];
  const optional = ["backup_name", "error_code", "completed_at"];
  if (
    !hasExactKeys(record, [
      ...required,
      ...optional.filter((field) => field in record),
    ])
  ) {
    throw invalidResponse("import status contains unsupported fields");
  }
  const phase = stringField(record, "phase") as ImportPhase;
  if (!importPhases.has(phase)) {
    throw invalidResponse("import status phase is invalid");
  }
  const operationId = stringField(record, "operation_id");
  const activeGeneration = stringField(record, "active_generation");
  const previousGeneration = stringField(record, "previous_generation");
  const newGeneration = stringField(record, "new_generation");
  const generationPattern = /^[A-Za-z0-9_-]{1,128}$/;
  if (
    !validImportOperationId(operationId) ||
    operationId !== newGeneration ||
    !generationPattern.test(activeGeneration) ||
    !generationPattern.test(previousGeneration) ||
    previousGeneration === newGeneration ||
    (activeGeneration !== previousGeneration &&
      activeGeneration !== newGeneration)
  ) {
    throw invalidResponse("import status generation binding is invalid");
  }
  const completedAt =
    "completed_at" in record ? canonicalUTCField(record, "completed_at") : null;
  const completionRequired = ["succeeded", "failed", "rolled_back"].includes(
    phase,
  );
  const completionForbidden = activePhasesForResponse.has(phase);
  if (
    (completionRequired && completedAt === null) ||
    (completionForbidden && completedAt !== null)
  ) {
    throw invalidResponse("import status completion is inconsistent");
  }
  const startedAt = canonicalUTCField(record, "started_at");
  const updatedAt = canonicalUTCField(record, "updated_at");
  if (
    compareCanonicalUTC(updatedAt, startedAt) < 0 ||
    (completedAt && compareCanonicalUTC(completedAt, updatedAt) < 0)
  ) {
    throw invalidResponse("import status timestamps are inconsistent");
  }
  const backupVerified = booleanField(record, "backup_verified");
  const rollbackAttempted = booleanField(record, "rollback_attempted");
  const rollbackSucceeded = booleanField(record, "rollback_succeeded");
  const backupName =
    "backup_name" in record ? stringField(record, "backup_name") : null;
  if (
    backupVerified !== (backupName !== null) ||
    (backupName !== null &&
      (!backupName.endsWith(".zip") ||
        backupName.includes("/") ||
        backupName.includes("\\")))
  ) {
    throw invalidResponse("import status backup proof is invalid");
  }
  const errorCode =
    "error_code" in record ? stringField(record, "error_code") : null;
  if (
    (errorCode !== null && !/^[A-Z0-9_]{1,128}$/.test(errorCode)) ||
    (errorCode === null) !== !("error_code" in record) ||
    (rollbackSucceeded && !rollbackAttempted)
  ) {
    throw invalidResponse("import status rollback proof is invalid");
  }
  const terminalTimeMatches = completedAt === updatedAt;
  const invalidPhase = (() => {
    switch (phase) {
      case "claimed":
      case "backing_up":
        return (
          backupVerified ||
          rollbackAttempted ||
          completedAt !== null ||
          errorCode !== null ||
          activeGeneration !== previousGeneration
        );
      case "finalizing":
        return (
          !backupVerified ||
          rollbackAttempted ||
          completedAt !== null ||
          errorCode !== null ||
          activeGeneration !== previousGeneration
        );
      case "switching":
        return (
          !backupVerified ||
          rollbackAttempted ||
          completedAt !== null ||
          errorCode !== null
        );
      case "verifying":
        return (
          !backupVerified ||
          rollbackAttempted ||
          completedAt !== null ||
          errorCode !== null ||
          activeGeneration !== newGeneration
        );
      case "rolling_back":
        return (
          !backupVerified ||
          !rollbackAttempted ||
          rollbackSucceeded ||
          completedAt !== null ||
          errorCode !== null
        );
      case "succeeded":
        return (
          !backupVerified ||
          rollbackAttempted ||
          errorCode !== null ||
          !terminalTimeMatches ||
          activeGeneration !== newGeneration
        );
      case "failed":
        return (
          rollbackAttempted ||
          errorCode === null ||
          !terminalTimeMatches ||
          activeGeneration !== previousGeneration
        );
      case "rolled_back":
        return (
          !backupVerified ||
          !rollbackAttempted ||
          !rollbackSucceeded ||
          errorCode === null ||
          !terminalTimeMatches ||
          activeGeneration !== previousGeneration
        );
      case "needs_attention":
        return errorCode === null || rollbackSucceeded;
    }
  })();
  if (invalidPhase) {
    throw invalidResponse("import status fields contradict its phase");
  }
  return {
    operationId,
    phase,
    activeGeneration,
    previousGeneration,
    newGeneration,
    backupVerified,
    rollbackAttempted,
    rollbackSucceeded,
    errorCode,
    startedAt,
    updatedAt,
    completedAt,
  };
}

async function decodeImportSuccess(
  response: Response,
  expectedStatus: number,
): Promise<unknown> {
  const text = await response.text();
  assertNoDuplicateJSONKeys(text);
  const replay = new Response(text, {
    status: response.status,
    statusText: response.statusText,
    headers: response.headers,
  });
  const { data, meta } = await decodeResponse(replay, {
    strictSuccessEnvelope: response.ok,
    requireJsonSuccess: response.ok,
  });
  const requestID = hasExactKeys(meta, ["request_id"])
    ? stringField(meta, "request_id")
    : "";
  if (
    response.status !== expectedStatus ||
    !/^req_[A-Za-z0-9_-]{1,124}$/.test(requestID)
  ) {
    throw invalidResponse("import response metadata is invalid");
  }
  return data;
}

const activePhasesForResponse = new Set<ImportPhase>([
  "claimed",
  "backing_up",
  "finalizing",
  "switching",
  "verifying",
  "rolling_back",
]);

function assertNoDuplicateJSONKeys(source: string): void {
  let index = 0;
  const whitespace = () => {
    while (/\s/.test(source[index] ?? "")) index += 1;
  };
  const stringToken = (): string => {
    if (source[index] !== '"') throw invalidResponse("JSON string is invalid");
    const start = index;
    index += 1;
    while (index < source.length) {
      const character = source[index];
      if (character === '"') {
        index += 1;
        try {
          return JSON.parse(source.slice(start, index)) as string;
        } catch {
          throw invalidResponse("JSON string is invalid");
        }
      }
      if (character === "\\") index += 1;
      index += 1;
    }
    throw invalidResponse("JSON string is incomplete");
  };
  const value = (): void => {
    whitespace();
    if (source[index] === "{") {
      index += 1;
      whitespace();
      const keys = new Set<string>();
      if (source[index] === "}") {
        index += 1;
        return;
      }
      while (index < source.length) {
        const key = stringToken();
        if (keys.has(key))
          throw invalidResponse("JSON object has duplicate keys");
        keys.add(key);
        whitespace();
        if (source[index] !== ":")
          throw invalidResponse("JSON object is invalid");
        index += 1;
        value();
        whitespace();
        if (source[index] === "}") {
          index += 1;
          return;
        }
        if (source[index] !== ",")
          throw invalidResponse("JSON object is invalid");
        index += 1;
        whitespace();
      }
      throw invalidResponse("JSON object is incomplete");
    }
    if (source[index] === "[") {
      index += 1;
      whitespace();
      if (source[index] === "]") {
        index += 1;
        return;
      }
      while (index < source.length) {
        value();
        whitespace();
        if (source[index] === "]") {
          index += 1;
          return;
        }
        if (source[index] !== ",")
          throw invalidResponse("JSON array is invalid");
        index += 1;
      }
      throw invalidResponse("JSON array is incomplete");
    }
    if (source[index] === '"') {
      stringToken();
      return;
    }
    const match = source
      .slice(index)
      .match(
        /^(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/,
      );
    if (!match) throw invalidResponse("JSON value is invalid");
    index += match[0].length;
  };
  value();
  whitespace();
  if (index !== source.length) throw invalidResponse("JSON has trailing data");
}

function uploadArchive(
  path: string,
  csrfToken: string,
  file: File,
  expectedSHA256: string | null,
  onProgress: (percent: number | null) => void,
  signal?: AbortSignal,
): Promise<unknown> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(
        new PublicApiError("archive upload was cancelled", {
          code: "CANCELLED",
          status: 0,
        }),
      );
      return;
    }
    const request = new XMLHttpRequest();
    let settled = false;
    const finish = (action: () => void) => {
      if (settled) return;
      settled = true;
      signal?.removeEventListener("abort", abort);
      action();
    };
    const abort = () => request.abort();
    request.open("POST", path);
    request.withCredentials = true;
    request.setRequestHeader("Accept", "application/json");
    request.setRequestHeader("Content-Type", "application/zip");
    request.setRequestHeader("X-CSRF-Token", csrfToken);
    if (expectedSHA256) {
      request.setRequestHeader("X-Moment-Archive-SHA256", expectedSHA256);
    }
    request.upload.addEventListener("progress", (event) => {
      if (event.lengthComputable && event.total > 0) {
        onProgress(
          Math.min(99, Math.round((event.loaded / event.total) * 100)),
        );
      } else {
        onProgress(null);
      }
    });
    request.addEventListener("error", () =>
      finish(() =>
        reject(
          new PublicApiError("archive upload connection was lost", {
            code: "NETWORK_ERROR",
            status: 0,
          }),
        ),
      ),
    );
    request.addEventListener("abort", () =>
      finish(() =>
        reject(
          new PublicApiError("archive upload was cancelled", {
            code: "CANCELLED",
            status: 0,
          }),
        ),
      ),
    );
    request.addEventListener("load", () => {
      if (
        request.status < 200 ||
        request.status > 599 ||
        request.status === 204 ||
        request.status === 205 ||
        request.status === 304
      ) {
        finish(() =>
          reject(invalidResponse("archive upload response status is invalid")),
        );
        return;
      }
      const headers = new Headers();
      const contentType = request.getResponseHeader("Content-Type");
      if (contentType) headers.set("Content-Type", contentType);
      let response: Response;
      try {
        response = new Response(request.responseText, {
          status: request.status,
          headers,
        });
      } catch {
        finish(() =>
          reject(invalidResponse("archive upload response is invalid")),
        );
        return;
      }
      void decodeImportSuccess(response, 200)
        .then((data) => finish(() => resolve(data)))
        .catch((error: unknown) => finish(() => reject(error)));
    });
    signal?.addEventListener("abort", abort, { once: true });
    onProgress(null);
    request.send(file);
  });
}

async function decodeExactOwnerSuccess(
  response: Response,
  dataKeys: string[],
  optionalDataKeys: string[] = [],
): Promise<Record<string, unknown>> {
  const result = await decodeResponse(response, {
    strictSuccessEnvelope: response.ok,
    requireJsonSuccess: response.ok,
  });
  if (response.status !== 200) {
    throw invalidResponse("owner settings success must return HTTP 200");
  }
  if (
    !hasExactKeys(result.meta, ["request_id"]) ||
    !stringField(result.meta, "request_id")
  ) {
    throw invalidResponse("owner settings metadata is invalid");
  }
  const data = objectData(result.data);
  const expectedDataKeys = [
    ...dataKeys,
    ...optionalDataKeys.filter((key) => key in data),
  ];
  if (!hasExactKeys(data, expectedDataKeys)) {
    throw invalidResponse("owner settings data contains unsupported fields");
  }
  return data;
}

function sitePayload(update: SiteSettingsUpdate): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  if (update.title !== undefined) payload.title = update.title;
  if (update.bio !== undefined) payload.bio = update.bio;
  if (update.timezone !== undefined) payload.timezone = update.timezone;
  if (update.publicBaseUrl !== undefined) {
    payload.public_base_url = update.publicBaseUrl;
  }
  if (update.avatarMediaId !== undefined) {
    payload.avatar_media_id = update.avatarMediaId;
  }
  if (update.coverMediaId !== undefined) {
    payload.cover_media_id = update.coverMediaId;
  }
  if (update.commentsEnabled !== undefined) {
    payload.comments_enabled = update.commentsEnabled;
  }
  return payload;
}

function parseUpload(data: unknown): UploadedMedia {
  const result = objectData(data);
  if (
    !hasExactKeys(result, ["media", "preview", "original_url", "preview_url"])
  ) {
    throw invalidResponse("upload result contains unsupported fields");
  }
  const media = objectData(result.media);
  const preview = objectData(result.preview);
  const id = stringField(media, "id");
  if (!isCanonicalUlid(id)) {
    throw invalidResponse("uploaded media id is invalid");
  }
  const originalUrl = stringField(result, "original_url");
  const previewUrl = stringField(result, "preview_url");
  const expectedOriginal = `/api/v1/media/${id}`;
  if (
    originalUrl !== expectedOriginal ||
    previewUrl !== `${expectedOriginal}/preview`
  ) {
    throw invalidResponse("upload result contains an uncontrolled media URL");
  }
  const animatedOriginal = booleanField(media, "animated_original");
  if (booleanField(preview, "animated_original") !== animatedOriginal) {
    throw invalidResponse("upload preview animation metadata is inconsistent");
  }
  return {
    id,
    byteSize: positiveIntegerField(media, "byte_size"),
    width: positiveIntegerField(media, "width"),
    height: positiveIntegerField(media, "height"),
    animatedOriginal,
    previewUrl,
    originalUrl,
  };
}

function mediaPayload(media: PostMediaInput[]): Record<string, unknown>[] {
  return media.map((usage, position) => ({
    media_id: usage.mediaId,
    position,
    alt_text: usage.altText,
  }));
}

function requestInit(
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

export class OwnerApi {
  private readonly fetcher: Fetcher;

  constructor(fetcher: Fetcher = window.fetch.bind(window)) {
    this.fetcher = fetcher;
  }

  async exchange(token: string): Promise<OwnerSession> {
    const response = await this.fetcher(
      "/api/v1/owner/session",
      requestInit("POST", undefined, { token }),
    );
    const { data } = await decodeResponse(response);
    const record = objectData(data);
    const expiresAt = stringField(record, "expires_at");
    if (!Number.isFinite(Date.parse(expiresAt))) {
      throw invalidResponse("response field expires_at must be an instant");
    }
    const csrfToken = stringField(record, "csrf_token");
    if (!csrfToken) throw invalidResponse("csrf_token cannot be empty");
    return { csrfToken, expiresAt };
  }

  async status(signal?: AbortSignal): Promise<OwnerStatus> {
    const response = await this.fetcher("/api/v1/owner/status", {
      ...requestInit("GET"),
      signal,
    });
    const preliminary = await decodeExactOwnerSuccess(
      response,
      ["initialized", "session_epoch", "initialized_at"],
      ["rotated_at"],
    );
    if (booleanField(preliminary, "initialized") !== true) {
      throw invalidResponse("owner status must be initialized");
    }
    return {
      initialized: true,
      sessionEpoch: positiveIntegerField(preliminary, "session_epoch"),
      initializedAt: utcInstantField(preliminary, "initialized_at"),
      rotatedAt: optionalInstantField(preliminary, "rotated_at"),
    };
  }

  async updateSite(
    csrfToken: string,
    update: SiteSettingsUpdate,
  ): Promise<SiteIdentity> {
    const response = await this.fetcher(
      "/api/v1/owner/site",
      requestInit("PATCH", csrfToken, sitePayload(update)),
    );
    const data = await decodeExactOwnerSuccess(response, [
      "title",
      "bio",
      "timezone",
      "public_base_url",
      "avatar_media_id",
      "cover_media_id",
      "pinned_post_id",
      "comments_enabled",
    ]);
    return parseSiteIdentity(data);
  }

  async rotateToken(csrfToken: string): Promise<RotatedCredential> {
    const response = await this.fetcher(
      "/api/v1/owner/token/rotate",
      requestInit("POST", csrfToken),
    );
    const record = await decodeExactOwnerSuccess(response, [
      "token",
      "initialized",
      "session_epoch",
      "initialized_at",
      "rotated_at",
    ]);
    const token = stringField(record, "token");
    const rotatedAt = utcInstantField(record, "rotated_at");
    if (!token) throw invalidResponse("rotated owner token cannot be empty");
    if (booleanField(record, "initialized") !== true) {
      throw invalidResponse("rotated owner credential must be initialized");
    }
    return {
      token,
      initialized: true,
      sessionEpoch: positiveIntegerField(record, "session_epoch"),
      initializedAt: utcInstantField(record, "initialized_at"),
      rotatedAt,
    };
  }

  async planImport(
    csrfToken: string,
    file: File,
    onProgress: (percent: number | null) => void = () => undefined,
    signal?: AbortSignal,
  ): Promise<ImportPlan> {
    const data = await uploadArchive(
      "/api/v1/owner/import/plan",
      csrfToken,
      file,
      null,
      onProgress,
      signal,
    );
    onProgress(100);
    return parseImportPlan(data, file.size);
  }

  async restoreImport(
    csrfToken: string,
    file: File,
    expectedSHA256: string,
    onProgress: (percent: number | null) => void = () => undefined,
    signal?: AbortSignal,
  ): Promise<ImportStatus> {
    if (!/^[0-9a-f]{64}$/.test(expectedSHA256)) {
      throw invalidResponse("expected archive SHA-256 is invalid");
    }
    const data = objectData(
      await uploadArchive(
        "/api/v1/owner/import/restore",
        csrfToken,
        file,
        expectedSHA256,
        onProgress,
        signal,
      ),
    );
    if (!hasExactKeys(data, ["status"])) {
      throw invalidResponse("restore result contains unsupported fields");
    }
    onProgress(100);
    const status = parseImportStatus(data.status);
    if (status.phase !== "succeeded") {
      throw invalidResponse(
        "restore response is not a successful terminal state",
      );
    }
    return status;
  }

  async importStatus(signal?: AbortSignal): Promise<ImportStatus> {
    const response = await this.fetcher("/api/v1/owner/import/status", {
      ...requestInit("GET"),
      signal,
    });
    return parseImportStatus(await decodeImportSuccess(response, 200));
  }

  async logout(csrfToken: string): Promise<void> {
    const response = await this.fetcher(
      "/api/v1/owner/session",
      requestInit("DELETE", csrfToken),
    );
    const { data } = await decodeResponse(response);
    const record = objectData(data);
    if (record.logged_out !== true) {
      throw invalidResponse("logout response was not confirmed");
    }
  }

  async create(
    csrfToken: string,
    contentMd: string,
    idempotencyKey: string,
    media: PostMediaInput[] = [],
  ): Promise<PublicPost> {
    const init = requestInit("POST", csrfToken, {
      content_md: contentMd,
      media: mediaPayload(media),
    });
    const headers = new Headers(init.headers);
    headers.set("Idempotency-Key", idempotencyKey);
    const response = await this.fetcher("/api/v1/owner/posts", {
      ...init,
      headers,
    });
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async update(
    csrfToken: string,
    id: string,
    contentMd: string,
    media?: PostMediaInput[],
  ): Promise<PublicPost> {
    const body: Record<string, unknown> = { content_md: contentMd };
    if (media) body.media = mediaPayload(media);
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(id)}`,
      requestInit("PATCH", csrfToken, body),
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async uploadMedia(
    csrfToken: string,
    file: File,
    onProgress: (percent: number) => void = () => undefined,
    signal?: AbortSignal,
  ): Promise<UploadedMedia> {
    return new Promise((resolve, reject) => {
      const request = new XMLHttpRequest();
      request.open("POST", "/api/v1/owner/media");
      request.withCredentials = true;
      request.setRequestHeader("Accept", "application/json");
      request.setRequestHeader("X-CSRF-Token", csrfToken);
      request.upload.addEventListener("progress", (event) => {
        if (event.lengthComputable && event.total > 0) {
          onProgress(
            Math.min(99, Math.round((event.loaded / event.total) * 100)),
          );
        }
      });
      request.addEventListener("error", () =>
        reject(
          new PublicApiError("media upload failed", {
            code: "NETWORK_ERROR",
            status: 0,
          }),
        ),
      );
      request.addEventListener("abort", () =>
        reject(
          new PublicApiError("media upload was cancelled", {
            code: "CANCELLED",
            status: 0,
          }),
        ),
      );
      request.addEventListener("load", () => {
        const headers = new Headers();
        const contentType = request.getResponseHeader("Content-Type");
        if (contentType) headers.set("Content-Type", contentType);
        const response = new Response(request.responseText, {
          status: request.status,
          headers,
        });
        if (
          request.status >= 200 &&
          request.status < 300 &&
          request.status !== 201
        ) {
          reject(invalidResponse("media upload success must return HTTP 201"));
          return;
        }
        void decodeResponse(response, {
          strictSuccessEnvelope: request.status === 201,
          requireJsonSuccess: request.status === 201,
        })
          .then(({ data, meta }) => {
            if (
              request.status !== 201 ||
              !hasExactKeys(meta, ["request_id"]) ||
              !stringField(meta, "request_id")
            ) {
              throw invalidResponse("media upload metadata is invalid");
            }
            const uploaded = parseUpload(data);
            onProgress(100);
            resolve(uploaded);
          })
          .catch(reject);
      });
      const abort = () => request.abort();
      if (signal?.aborted) {
        reject(
          new PublicApiError("media upload was cancelled", {
            code: "CANCELLED",
            status: 0,
          }),
        );
        return;
      }
      signal?.addEventListener("abort", abort, { once: true });
      request.addEventListener("loadend", () =>
        signal?.removeEventListener("abort", abort),
      );
      const body = new FormData();
      body.append("file", file, file.name);
      onProgress(0);
      request.send(body);
    });
  }

  async delete(csrfToken: string, id: string): Promise<PublicPost> {
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(id)}`,
      requestInit("DELETE", csrfToken),
    );
    const { data } = await decodeResponse(response);
    return parsePost(data);
  }

  async pinPost(csrfToken: string, id: string): Promise<SiteIdentity> {
    return this.pinPostChange(csrfToken, id, "PUT");
  }

  async unpinPost(csrfToken: string, id: string): Promise<SiteIdentity> {
    return this.pinPostChange(csrfToken, id, "DELETE");
  }

  private async pinPostChange(
    csrfToken: string,
    id: string,
    method: "PUT" | "DELETE",
  ): Promise<SiteIdentity> {
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(id)}/pin`,
      requestInit(method, csrfToken),
    );
    const data = await decodeExactOwnerSuccess(response, [
      "title",
      "bio",
      "timezone",
      "public_base_url",
      "avatar_media_id",
      "cover_media_id",
      "pinned_post_id",
      "comments_enabled",
    ]);
    return parseSiteIdentity(data);
  }

  async createComment(
    csrfToken: string,
    postId: string,
    contentMd: string,
    parentId: string | null,
    idempotencyKey: string,
  ): Promise<CreatedComment> {
    const init = requestInit("POST", csrfToken, {
      content_md: contentMd,
      parent_id: parentId,
    });
    const headers = new Headers(init.headers);
    headers.set("Idempotency-Key", idempotencyKey);
    const response = await this.fetcher(
      `/api/v1/owner/posts/${encodeURIComponent(postId)}/comments`,
      { ...init, headers },
    );
    return decodeCreatedComment(response, postId, {
      parentId,
      authorKind: "owner",
    });
  }

  async deleteComment(
    csrfToken: string,
    postId: string,
    commentId: string,
  ): Promise<DeletedComment> {
    const response = await this.fetcher(
      `/api/v1/owner/comments/${encodeURIComponent(commentId)}`,
      requestInit("DELETE", csrfToken),
    );
    const { data, meta } = await decodeCommentResponse(response, [
      "deleted",
      "dry_run",
      "replies_affected",
    ]);
    if (response.status !== 200) {
      throw invalidResponse("comment deletion must return HTTP 200");
    }
    const comment = parseComment(data, postId);
    const result = parseDeletedCommentMeta(meta);
    if (!result.deleted || result.dryRun || comment.id !== commentId) {
      throw invalidResponse("comment deletion was not confirmed");
    }
    if (comment.parentId !== null && result.repliesAffected !== 0) {
      throw invalidResponse("a reply deletion cannot affect child comments");
    }
    return { comment, repliesAffected: result.repliesAffected };
  }
}
