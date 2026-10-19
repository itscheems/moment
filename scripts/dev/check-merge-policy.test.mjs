import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import {
  assertMergeCommit,
  assertMergeSettings,
  auditMerge,
  checkSettings,
  createGitHubClient,
  parseInvocation,
} from "./check-merge-policy.mjs";

const settings = {
  mergeCommitAllowed: true,
  squashMergeAllowed: false,
  rebaseMergeAllowed: false,
};
const base = "a".repeat(40),
  head = "b".repeat(40),
  merge = "c".repeat(40);
const commit = { sha: merge, parents: [{ sha: base }, { sha: head }] };
const pr = { merged: true, merge_commit_sha: merge, head: { sha: head } };

test("function: only known ordinary-merge settings pass", () => {
  assertMergeSettings(settings);
  for (const name of Object.keys(settings)) {
    assert.throws(
      () => assertMergeSettings({ ...settings, [name]: !settings[name] }),
      /requires/,
    );
    assert.throws(
      () => assertMergeSettings({ ...settings, [name]: undefined }),
      /missing data/,
    );
  }
  assert.throws(() => assertMergeSettings(null));
});
test("function: exact merge SHA, two parents and original PR head are mandatory", () => {
  assertMergeCommit(commit, head, merge);
  for (const bad of [
    null,
    { ...commit, sha: base },
    { ...commit, parents: [] },
    { ...commit, parents: [{ sha: base }] },
    { ...commit, parents: [...commit.parents, { sha: merge }] },
    { ...commit, parents: [{ sha: head }, { sha: head }] },
    { ...commit, parents: [{ sha: base }, { sha: merge }] },
  ]) {
    assert.throws(() => assertMergeCommit(bad, head, merge), /audit failed/);
  }
  assert.throws(() => assertMergeCommit(commit, undefined, merge));
});
test("function: audit uses recorded merge/head, independently of today's settings or main", async () => {
  const paths = [];
  const result = await auditMerge(
    async (path) => {
      paths.push(path);
      return commit;
    },
    "owner/repo",
    pr,
  );
  assert.deepEqual(paths, [`/repos/owner/repo/git/commits/${merge}`]);
  assert.deepEqual(result, { mergeSha: merge, head });
  for (const invalid of [
    { ...pr, merged: false },
    { ...pr, merge_commit_sha: null },
    { ...pr, head: null },
  ]) {
    await assert.rejects(
      auditMerge(
        () => assert.fail("must not look up invalid metadata"),
        "owner/repo",
        invalid,
      ),
    );
  }
});
test("function: missing credentials and unbounded request options fail immediately", () => {
  assert.throws(() => createGitHubClient(), /Set GH_TOKEN/);
  for (const options of [
    { maxAttempts: 0 },
    { maxAttempts: 4 },
    { timeoutMs: 0 },
    { timeoutMs: 20_000 },
    { maxRetryDelayMs: 61_000 },
  ]) {
    assert.throws(
      () => createGitHubClient({ token: "local-test", ...options }),
      /bounded/,
    );
  }
});
test("CLI: a missing token exits nonzero without contacting GitHub", () => {
  const env = { ...process.env };
  for (const key of [
    "GH_TOKEN",
    "GITHUB_TOKEN",
    "GITHUB_EVENT_PATH",
    "GITHUB_EVENT_NAME",
  ])
    delete env[key];
  const result = spawnSync(
    process.execPath,
    [fileURLToPath(new URL("./check-merge-policy.mjs", import.meta.url))],
    { env, encoding: "utf8", timeout: 5000 },
  );
  assert.equal(result.status, 1);
  assert.match(result.stderr, /Set GH_TOKEN/);
});
test("events: edited/retargeted and other PR activity run the settings gate only", () => {
  for (const action of [
    "opened",
    "reopened",
    "synchronize",
    "ready_for_review",
    "edited",
  ]) {
    assert.deepEqual(
      parseInvocation([], "pull_request", {
        action,
        changes: { base: { ref: { from: "other" } } },
        pull_request: { ...pr, base: { ref: "main" } },
      }),
      { mode: "check" },
    );
  }
  for (const event of ["push", "workflow_dispatch", undefined])
    assert.deepEqual(parseInvocation([], event), { mode: "check" });
});
test("events: closed audit preserves event metadata; unmerged/missing payload fails", () => {
  assert.deepEqual(
    parseInvocation(["audit"], "pull_request", {
      action: "closed",
      pull_request: pr,
    }),
    { mode: "audit", pr },
  );
  for (const event of [
    {},
    { action: "closed", pull_request: { ...pr, merged: false } },
    { action: "edited", pull_request: pr },
  ]) {
    assert.throws(
      () => parseInvocation(["audit"], "pull_request", event),
      /Usage/,
    );
  }
  assert.deepEqual(
    parseInvocation(["audit", "--pr", "123"], "workflow_dispatch"),
    { mode: "audit", number: "123" },
  );
  for (const args of [
    ["audit", "--pr", "0"],
    ["audit", "--pr", "1;echo"],
    ["--merged-pr", "1"],
  ])
    assert.throws(() => parseInvocation(args));
  for (const args of [[], ["audit", "--pr", "1"]])
    assert.throws(() => parseInvocation(args, "merge_group"), /unsupported/);
});
test("workflow declarations: retargeting triggers required gate; retrospective audit is separate", async () => {
  const gate = await readFile(
    new URL("../../.github/workflows/merge-policy.yml", import.meta.url),
    "utf8",
  );
  const audit = await readFile(
    new URL("../../.github/workflows/merge-result-audit.yml", import.meta.url),
    "utf8",
  );
  assert.match(
    gate,
    /pull_request:\s+branches: \[main\]\s+types: \[opened, reopened, synchronize, ready_for_review, edited\]/,
  );
  assert.match(gate, /push:\s+branches: \[main\]/);
  assert.match(gate, /workflow_dispatch:/);
  assert.doesNotMatch(gate, /closed|merge_group|mise-action/);
  assert.match(gate, /name: merge-policy/);
  assert.match(audit, /pull_request:\s+branches: \[main\]\s+types: \[closed\]/);
  assert.match(audit, /github\.event\.pull_request\.merged == true/);
  assert.match(audit, /check-merge-policy\.mjs audit --pr "\$PR_NUMBER"/);
  assert.match(audit, /check-merge-policy\.mjs audit\r?\n/);
  assert.doesNotMatch(audit, /merge_group|mise-action/);
  for (const workflow of [gate, audit]) {
    assert.match(workflow, /actions\/setup-node@[a-f\d]{40}/);
    assert.match(workflow, /timeout-minutes: 5/);
  }
});

async function withServer(t, handler, options = {}) {
  const server = createServer(handler);
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  t.after(() => {
    server.closeAllConnections();
    return new Promise((resolve) => server.close(resolve));
  });
  return createGitHubClient({
    token: "local-test",
    baseUrl: `http://127.0.0.1:${server.address().port}`,
    ...options,
  });
}
function json(response, status, value, headers = {}) {
  response.writeHead(status, {
    "content-type": "application/json",
    ...headers,
  });
  response.end(JSON.stringify(value));
}
for (const status of [408, 429, 500, 502, 503, 504]) {
  test(`HTTP: transient ${status} recovers after a bounded retry`, async (t) => {
    let calls = 0;
    const request = await withServer(t, (_req, res) => {
      if (++calls === 1)
        json(res, status, { message: "temporary" }, { "retry-after": "0" });
      else json(res, 200, { ok: true });
    });
    assert.deepEqual(await request("/test"), { ok: true });
    assert.equal(calls, 2);
  });
}
test("HTTP: secondary 403 and GraphQL rate limits recover, not permission failures", async (t) => {
  let calls = 0;
  const request = await withServer(t, (_req, res) => {
    calls++;
    if (calls === 1)
      json(
        res,
        403,
        { message: "secondary rate limit" },
        { "retry-after": "0" },
      );
    else if (calls === 2)
      json(
        res,
        200,
        { errors: [{ type: "RATE_LIMITED" }] },
        { "retry-after": "0" },
      );
    else json(res, 200, { data: { repository: settings } });
  });
  await checkSettings(request, "owner/repo");
  assert.equal(calls, 3);
});
for (const headers of [
  { "retry-after": "120" },
  {
    "x-ratelimit-remaining": "0",
    "x-ratelimit-reset": String(Math.ceil(Date.now() / 1000) + 300),
  },
  {},
]) {
  test(`HTTP: long/unspecified rate-limit waits fail without retrying early (${JSON.stringify(headers)})`, async (t) => {
    let calls = 0;
    const request = await withServer(
      t,
      (_req, res) => {
        calls++;
        json(res, 429, {}, headers);
      },
      { maxRetryDelayMs: 1000 },
    );
    await assert.rejects(request("/test"), /wait budget/);
    assert.equal(calls, 1);
  });
}
test("HTTP: Retry-After is honored before retrying", async (t) => {
  const times = [];
  const request = await withServer(t, (_req, res) => {
    times.push(Date.now());
    json(res, times.length === 1 ? 429 : 200, {}, { "retry-after": "1" });
  });
  await request("/test");
  assert.equal(times.length, 2);
  assert.ok(times[1] - times[0] >= 1000);
});
test("HTTP: repeated server failures exhaust exactly three attempts", async (t) => {
  let calls = 0;
  const request = await withServer(t, (_req, res) => {
    calls++;
    json(res, 503, {});
  });
  await assert.rejects(request("/test"), /Retry limit exhausted/);
  assert.equal(calls, 3);
});
test("HTTP: dropped socket recovers", async (t) => {
  let calls = 0;
  const request = await withServer(t, (req, res) => {
    if (++calls === 1) req.socket.destroy();
    else json(res, 200, {});
  });
  await request("/test");
  assert.equal(calls, 2);
});
for (const phase of ["headers", "body"]) {
  test(`HTTP: ${phase} timeout exhausts finite attempts`, async (t) => {
    let calls = 0;
    const request = await withServer(
      t,
      (_req, res) => {
        calls++;
        if (phase === "body") {
          res.writeHead(200);
          res.write('{"unfinished":');
        }
      },
      { timeoutMs: 50, maxAttempts: 2 },
    );
    await assert.rejects(request("/test"), /timed out.*Retry limit exhausted/);
    assert.equal(calls, 2);
  });
}
for (const status of [400, 401, 403, 404, 422]) {
  test(`HTTP: terminal ${status} is not retried`, async (t) => {
    let calls = 0;
    const request = await withServer(t, (_req, res) => {
      calls++;
      json(res, status, { message: "terminal" });
    });
    await assert.rejects(request("/test"), new RegExp(`HTTP ${status}`));
    assert.equal(calls, 1);
  });
}
test("HTTP: invalid or unavailable GraphQL/settings data never passes or retries", async (t) => {
  const results = [
    null,
    {},
    { data: { repository: null } },
    { errors: [{ type: "FORBIDDEN" }] },
    { errors: [{ type: "RATE_LIMITED" }, { type: "FORBIDDEN" }] },
    { data: { repository: { ...settings, rebaseMergeAllowed: true } } },
  ];
  let calls = 0;
  const request = await withServer(t, (_req, res) =>
    json(res, 200, results[calls++]),
  );
  for (let i = 0; i < results.length; i++)
    await assert.rejects(checkSettings(request, "owner/repo"));
  assert.equal(calls, results.length);
});
test("HTTP: malformed success and missing/invalid audit data fail without retry", async (t) => {
  let calls = 0;
  const request = await withServer(t, (_req, res) => {
    if (++calls === 1) {
      res.end("not json");
    } else json(res, 200, { ...commit, sha: base });
  });
  await assert.rejects(request("/test"), /invalid JSON/);
  await assert.rejects(auditMerge(request, "owner/repo", pr), /audit failed/);
  assert.equal(calls, 2);
});

test("local Git: concurrent A/B ordinary merges retain all commits after main advances; actual squash/rebase results fail", async (t) => {
  const cwd = await mkdtemp(join(tmpdir(), "moment-merge-policy-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  const config = join(cwd, "empty-config");
  await writeFile(config, "");
  const env = {
    ...process.env,
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: config,
    GIT_AUTHOR_NAME: "Policy Test",
    GIT_AUTHOR_EMAIL: "test@example.invalid",
    GIT_COMMITTER_NAME: "Policy Test",
    GIT_COMMITTER_EMAIL: "test@example.invalid",
  };
  // Strip inherited worktree/config overrides so all mutation stays in this disposable repo.
  for (const key of Object.keys(env))
    if (
      /^GIT_(DIR|WORK_TREE|INDEX_FILE|COMMON_DIR|CONFIG_COUNT|CONFIG_KEY_\d+|CONFIG_VALUE_\d+)$/.test(
        key,
      )
    )
      delete env[key];
  const git = (...args) =>
    execFileSync("git", args, {
      cwd,
      env,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  const addCommit = async (name) => {
    await writeFile(join(cwd, name), name);
    git("add", name);
    git("commit", "-m", name);
    return git("rev-parse", "HEAD");
  };
  const lookup = (sha) => ({
    sha,
    parents: git("show", "-s", "--format=%P", sha)
      .split(" ")
      .map((sha) => ({ sha })),
  });
  git("init", "-b", "main");
  const originalBase = await addCommit("base");
  const heads = {},
    commits = {};
  for (const branch of ["A", "B"]) {
    git("checkout", "-b", branch, originalBase);
    commits[branch] = [
      await addCommit(`${branch}1`),
      await addCommit(`${branch}2`),
    ];
    heads[branch] = git("rev-parse", "HEAD");
  }
  git("checkout", "main");
  const merges = {};
  for (const branch of ["A", "B"]) {
    git("merge", "--no-ff", branch, "-m", `Merge ${branch}`);
    merges[branch] = git("rev-parse", "HEAD");
    assertMergeCommit(lookup(merges[branch]), heads[branch], merges[branch]);
    for (const sha of commits[branch])
      git("merge-base", "--is-ancestor", sha, merges[branch]);
  }
  assert.equal(lookup(merges.B).parents[0].sha, merges.A);
  await addCommit("later-main");
  for (const branch of ["A", "B"]) {
    await auditMerge(
      async (path) => {
        assert.equal(path, `/repos/owner/repo/git/commits/${merges[branch]}`);
        return lookup(merges[branch]);
      },
      "owner/repo",
      {
        merged: true,
        merge_commit_sha: merges[branch],
        head: { sha: heads[branch] },
      },
    );
  }
  git("checkout", "-b", "squash-result", originalBase);
  git("merge", "--squash", "A");
  git("commit", "-m", "Squashed A");
  const squash = git("rev-parse", "HEAD");
  assert.throws(
    () => assertMergeCommit(lookup(squash), heads.A, squash),
    /forbidden/,
  );
  assert.equal(
    spawnSync("git", ["merge-base", "--is-ancestor", heads.A, squash], {
      cwd,
      env,
    }).status,
    1,
  );
  git("checkout", "-b", "rebase-base", originalBase);
  await addCommit("rebase-base-change");
  git("checkout", "-b", "rebased-A", "A");
  git("rebase", "rebase-base");
  git("checkout", "rebase-base");
  git("merge", "--ff-only", "rebased-A");
  const rebased = git("rev-parse", "HEAD");
  assert.notEqual(rebased, heads.A);
  assert.throws(
    () => assertMergeCommit(lookup(rebased), heads.A, rebased),
    /forbidden/,
  );
  assert.equal(
    spawnSync("git", ["merge-base", "--is-ancestor", heads.A, rebased], {
      cwd,
      env,
    }).status,
    1,
  );
});
