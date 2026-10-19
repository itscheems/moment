import { readFile } from "node:fs/promises";
import { setTimeout as sleep } from "node:timers/promises";
import { pathToFileURL } from "node:url";

const shaPattern = /^[a-f\d]{40}$/;
const transientCodes = new Set([
  "ECONNRESET",
  "ECONNREFUSED",
  "EPIPE",
  "ETIMEDOUT",
  "EAI_AGAIN",
  "UND_ERR_CONNECT_TIMEOUT",
  "UND_ERR_HEADERS_TIMEOUT",
  "UND_ERR_BODY_TIMEOUT",
  "UND_ERR_SOCKET",
]);

export function assertMergeSettings(settings) {
  for (const [name, expected] of Object.entries({
    mergeCommitAllowed: true,
    squashMergeAllowed: false,
    rebaseMergeAllowed: false,
  })) {
    if (settings?.[name] !== expected) {
      throw new Error(
        `Merge policy requires ${name}=${expected}; missing data is not compliance.`,
      );
    }
  }
}

export function assertMergeCommit(commit, head, mergeSha) {
  if (
    !shaPattern.test(head ?? "") ||
    !shaPattern.test(mergeSha ?? "") ||
    commit?.sha !== mergeSha ||
    commit?.parents?.length !== 2 ||
    !shaPattern.test(commit.parents[0]?.sha ?? "") ||
    commit.parents[0].sha === head ||
    commit.parents[1]?.sha !== head
  ) {
    throw new Error(
      "Merge-result audit failed: the recorded merge SHA must have two parents and retain the recorded PR head as its second parent; squash and rebase results are forbidden.",
    );
  }
}

function transientNetworkError(error) {
  return (
    error?.name === "TimeoutError" ||
    transientCodes.has(error?.code) ||
    (error?.cause && transientNetworkError(error.cause))
  );
}

function retryDelay(response, rateLimited, attempt) {
  const retryAfter = response.headers.get("retry-after");
  const reset = response.headers.get("x-ratelimit-reset");
  const delays = [500 * 2 ** attempt];
  if (retryAfter !== null) {
    const seconds = Number(retryAfter);
    const delay = Number.isFinite(seconds)
      ? seconds * 1000
      : Date.parse(retryAfter) - Date.now();
    if (!Number.isFinite(delay))
      throw new Error("Invalid GitHub Retry-After header.");
    delays.push(delay);
  }
  if (response.headers.get("x-ratelimit-remaining") === "0" && reset !== null) {
    if (!/^\d+$/.test(reset))
      throw new Error("Invalid GitHub rate-limit reset header.");
    delays.push(Number(reset) * 1000 - Date.now());
  }
  if (
    rateLimited &&
    retryAfter === null &&
    !(response.headers.get("x-ratelimit-remaining") === "0" && reset !== null)
  ) {
    delays.push(60_000 * 2 ** attempt);
  }
  return Math.max(...delays);
}

// Production always uses GitHub. A local endpoint and short timings let tests
// exercise real HTTP failures without credentials or external services.
export function createGitHubClient({
  token,
  baseUrl = "https://api.github.com",
  timeoutMs = 10_000,
  maxAttempts = 3,
  maxRetryDelayMs = 60_000,
} = {}) {
  if (!token)
    throw new Error(
      "Set GH_TOKEN or GITHUB_TOKEN with repository read access.",
    );
  if (
    !Number.isInteger(maxAttempts) ||
    maxAttempts < 1 ||
    maxAttempts > 3 ||
    !Number.isFinite(timeoutMs) ||
    timeoutMs <= 0 ||
    timeoutMs > 10_000 ||
    !Number.isFinite(maxRetryDelayMs) ||
    maxRetryDelayMs < 0 ||
    maxRetryDelayMs > 60_000
  ) {
    throw new Error("Invalid bounded GitHub request limits.");
  }
  return async function request(path, body) {
    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      let delay;
      let failure;
      try {
        const response = await fetch(`${baseUrl}${path}`, {
          method: body ? "POST" : "GET",
          headers: {
            authorization: `Bearer ${token}`,
            "content-type": "application/json",
            "user-agent": "Moment merge policy",
          },
          body: body ? JSON.stringify(body) : undefined,
          signal: AbortSignal.timeout(timeoutMs),
        });
        if (
          !response.ok &&
          ![403, 408, 429, 500, 502, 503, 504].includes(response.status)
        ) {
          throw new Error(
            `GitHub lookup failed: HTTP ${response.status}; authorization, permissions and missing resources require correction.`,
          );
        }
        // Read inside the timeout too. An empty/malformed success is not a pass.
        const raw = await response.text();
        let result;
        try {
          result = JSON.parse(raw);
        } catch {
          if (response.ok) throw new Error("GitHub returned invalid JSON.");
        }
        const errors = result?.errors;
        const rateLimited =
          response.status === 429 ||
          (response.status === 403 &&
            (response.headers.has("retry-after") ||
              response.headers.get("x-ratelimit-remaining") === "0" ||
              /secondary rate limit|API rate limit exceeded/i.test(
                result?.message ?? "",
              ))) ||
          (response.ok &&
            Array.isArray(errors) &&
            errors.length > 0 &&
            errors.every((error) => error.type === "RATE_LIMITED"));
        const temporary = [408, 500, 502, 503, 504].includes(response.status);
        if (rateLimited || temporary) {
          failure = new Error(
            `GitHub lookup temporarily unavailable (HTTP ${response.status}${rateLimited ? ", rate limited" : ""}).`,
          );
          delay = retryDelay(response, rateLimited, attempt);
        } else {
          if (!response.ok)
            throw new Error(
              `GitHub lookup failed: HTTP ${response.status}; authorization, permissions and missing resources require correction.`,
            );
          if (errors !== undefined) {
            if (!Array.isArray(errors) || errors.length)
              throw new Error(
                "GitHub GraphQL lookup failed; no policy result is available.",
              );
          }
          if (!result || typeof result !== "object")
            throw new Error("GitHub returned missing data.");
          return result;
        }
      } catch (error) {
        if (!transientNetworkError(error)) throw error;
        failure = new Error("GitHub network request failed or timed out.", {
          cause: error,
        });
        delay = 500 * 2 ** attempt;
      }
      if (attempt + 1 === maxAttempts)
        throw new Error(`${failure.message} Retry limit exhausted.`, {
          cause: failure,
        });
      if (delay > maxRetryDelayMs)
        throw new Error(
          `${failure.message} Required retry delay exceeds the bounded wait budget; try again later.`,
          { cause: failure },
        );
      await sleep(delay);
    }
  };
}

export async function checkSettings(request, repository) {
  const [owner, name] = repository.split("/");
  const result = await request("/graphql", {
    query:
      "query($owner:String!,$name:String!){repository(owner:$owner,name:$name){mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed}}",
    variables: { owner, name },
  });
  assertMergeSettings(result.data?.repository);
}

export async function auditMerge(request, repository, pr) {
  if (pr?.merged !== true)
    throw new Error("Merge-result audit requires a merged pull request.");
  const sha = pr.merge_commit_sha;
  const head = pr.head?.sha;
  if (!shaPattern.test(sha ?? "") || !shaPattern.test(head ?? "")) {
    throw new Error(
      "Merge-result audit is missing the recorded merge SHA or PR head.",
    );
  }
  // Never look up today's main: later merges cannot invalidate this result.
  assertMergeCommit(
    await request(`/repos/${repository}/git/commits/${sha}`),
    head,
    sha,
  );
  return { mergeSha: sha, head };
}

export function parseInvocation(args, eventName, event = {}) {
  if (eventName === "merge_group")
    throw new Error(
      "Merge Queue is unsupported; its topology has not been validated.",
    );
  if (args.length === 0) return { mode: "check" };
  if (
    args[0] === "audit" &&
    args.length === 3 &&
    args[1] === "--pr" &&
    /^[1-9]\d*$/.test(args[2])
  ) {
    return { mode: "audit", number: args[2] };
  }
  if (
    args.length === 1 &&
    args[0] === "audit" &&
    eventName === "pull_request" &&
    event.action === "closed" &&
    event.pull_request?.merged === true
  ) {
    return { mode: "audit", pr: event.pull_request };
  }
  throw new Error(
    "Usage: check-merge-policy.mjs [audit --pr NUMBER]; audit without a number requires a merged PR closed event.",
  );
}

async function main() {
  const event = process.env.GITHUB_EVENT_PATH
    ? JSON.parse(await readFile(process.env.GITHUB_EVENT_PATH, "utf8"))
    : {};
  const invocation = parseInvocation(
    process.argv.slice(2),
    process.env.GITHUB_EVENT_NAME,
    event,
  );
  const repository = process.env.GITHUB_REPOSITORY || "itscheems/moment";
  if (!/^[\w.-]+\/[\w.-]+$/.test(repository))
    throw new Error("Invalid repository.");
  const request = createGitHubClient({
    token: process.env.GH_TOKEN || process.env.GITHUB_TOKEN,
  });
  if (invocation.mode === "check") {
    await checkSettings(request, repository);
    console.log("Merge settings gate passed: ordinary Merge Commit only.");
  } else {
    const pr =
      invocation.pr ??
      (await request(`/repos/${repository}/pulls/${invocation.number}`));
    const result = await auditMerge(request, repository, pr);
    console.log(
      `Merge-result audit passed: ${result.mergeSha} retains PR head ${result.head}. This is a retrospective audit, not a pre-merge gate.`,
    );
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  main().catch((error) => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
