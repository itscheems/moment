import assert from "node:assert/strict";
import { test } from "node:test";
import {
  assertMergeCommit,
  assertMergeSettings,
} from "./check-merge-policy.mjs";

const settings = {
  mergeCommitAllowed: true,
  squashMergeAllowed: false,
  rebaseMergeAllowed: false,
};
test("permits only ordinary merge commits", () =>
  assertMergeSettings(settings));
for (const [name, value] of Object.entries(settings)) {
  test(`rejects a changed ${name} policy`, () => {
    assert.throws(
      () => assertMergeSettings({ ...settings, [name]: !value }),
      /Merge policy requires/,
    );
  });
}
test("fails closed when settings are missing", () =>
  assert.throws(() => assertMergeSettings(null)));
test("retains the original pull request head", () => {
  assertMergeCommit({ parents: [{ sha: "base" }, { sha: "head" }] }, "head");
});
for (const method of ["squash", "rebase"]) {
  test(`rejects a ${method} result with one parent`, () => {
    assert.throws(
      () => assertMergeCommit({ parents: [{ sha: "base" }] }, "head"),
      /forbidden/,
    );
  });
}
test("rejects an unrelated second parent", () => {
  assert.throws(() =>
    assertMergeCommit({ parents: [{ sha: "base" }, { sha: "other" }] }, "head"),
  );
});
