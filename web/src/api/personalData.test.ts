import { expect, it } from "vitest";
import { PersonalDataApi } from "./personalData";

const zero = {
  entries: 0,
  comments: 0,
  media: 0,
  users: 0,
  identities: 0,
  articles: 0,
  article_drafts: 0,
};
const hash = "b".repeat(64);
function response(dryRun: boolean, sha = hash, entries = 2) {
  return new Response(
    JSON.stringify({
      data: {
        dry_run: dryRun,
        archive_sha256: sha,
        account_id: "a".repeat(32),
        counts: { ...zero, entries },
        added: zero,
        skipped: zero,
      },
      meta: { request_id: "personal-import" },
    }),
    { headers: { "Content-Type": "application/json" } },
  );
}

it("uploads the reviewed file with CSRF and binds restore to its hash", async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fetcher: typeof fetch = async (url, init) => {
    calls.push({ url: String(url), init });
    return response(calls.length === 1);
  };
  const api = new PersonalDataApi(fetcher),
    file = new File(["zip"], "personal.zip"),
    signal = new AbortController().signal;
  const plan = await api.import(file, "csrf", null, signal);
  expect(plan.counts.entries).toBe(2);
  await api.import(file, "csrf", plan.archiveSHA256, signal);
  expect(calls.map((call) => call.url)).toEqual([
    "/api/v1/account/import/plan",
    "/api/v1/account/import/restore",
  ]);
  expect(calls[1].init?.body).toBe(file);
  const headers = new Headers(calls[1].init?.headers);
  expect(headers.get("X-CSRF-Token")).toBe("csrf");
  expect(headers.get("X-Moment-Archive-SHA256")).toBe(hash);
  expect(calls[1].init?.signal).toBe(signal);
});

it("refuses a result for a different archive or invalid counts", async () => {
  const file = new File(["zip"], "personal.zip"),
    signal = new AbortController().signal;
  const changed = new PersonalDataApi(async () =>
    response(false, "c".repeat(64)),
  );
  await expect(
    changed.import(file, "csrf", hash, signal),
  ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  const invalid = new PersonalDataApi(async () => response(true, hash, -1));
  await expect(
    invalid.import(file, "csrf", null, signal),
  ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
});
