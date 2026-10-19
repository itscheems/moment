import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

export function assertMergeSettings(settings) {
  for (const [name, expected] of Object.entries({
    mergeCommitAllowed: true,
    squashMergeAllowed: false,
    rebaseMergeAllowed: false,
  })) {
    if (settings?.[name] !== expected) {
      throw new Error(`Merge policy requires ${name}=${expected}.`);
    }
  }
}

export function assertMergeCommit(commit, head) {
  if (
    !head ||
    commit?.parents?.length !== 2 ||
    commit.parents[1].sha !== head
  ) {
    throw new Error(
      "A normal merge must retain the PR head as its second parent; squash and rebase merges are forbidden.",
    );
  }
}

async function main() {
  const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN;
  if (!token)
    throw new Error(
      "Set GH_TOKEN or GITHUB_TOKEN with repository read access.",
    );
  const repository = process.env.GITHUB_REPOSITORY || "itscheems/moment";
  if (!/^[\w.-]+\/[\w.-]+$/.test(repository))
    throw new Error("Invalid repository.");
  const args = process.argv.slice(2);
  if (
    args.length &&
    (args.length !== 2 || args[0] !== "--merged-pr" || !/^\d+$/.test(args[1]))
  ) {
    throw new Error("Usage: check-merge-policy.mjs [--merged-pr NUMBER]");
  }
  async function request(path, body) {
    const response = await fetch(`https://api.github.com${path}`, {
      method: body ? "POST" : "GET",
      headers: {
        authorization: `Bearer ${token}`,
        "content-type": "application/json",
        "user-agent": "Moment merge policy",
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!response.ok)
      throw new Error(`GitHub policy lookup failed: HTTP ${response.status}.`);
    return response.json();
  }
  const [owner, name] = repository.split("/");
  const result = await request("/graphql", {
    query:
      "query($owner:String!,$name:String!){repository(owner:$owner,name:$name){mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed}}",
    variables: { owner, name },
  });
  if (result.errors?.length)
    throw new Error(result.errors.map((error) => error.message).join("; "));
  assertMergeSettings(result.data?.repository);
  let pr;
  if (args.length) {
    pr = await request(`/repos/${repository}/pulls/${args[1]}`);
    if (!pr.merged)
      throw new Error("The selected pull request has not merged.");
  } else if (process.env.GITHUB_EVENT_PATH) {
    const event = JSON.parse(
      await readFile(process.env.GITHUB_EVENT_PATH, "utf8"),
    );
    if (event.pull_request?.merged) pr = event.pull_request;
  }
  if (pr) {
    const sha = pr.merge_commit_sha;
    if (!/^[a-f\d]{40}$/.test(sha))
      throw new Error("Missing merge commit SHA.");
    assertMergeCommit(
      await request(`/repos/${repository}/git/commits/${sha}`),
      pr.head?.sha,
    );
  }
  console.log("Merge policy passed: normal merge commits only.");
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
