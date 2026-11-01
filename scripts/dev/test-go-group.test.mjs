import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import {
  chmodSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

test("archive shards run every discovered test exactly once with race detection", () => {
  const dir = mkdtempSync(join(tmpdir(), "moment-go-shards-"));
  const calls = join(dir, "calls.jsonl");
  const names = [
    "TestZulu",
    "TestAlpha",
    "TestDelta",
    "TestBeta",
    "Example",
    "FuzzArchive",
  ];
  try {
    const fakeGo = join(dir, "go");
    writeFileSync(
      fakeGo,
      `#!${process.execPath}
const fs = require("node:fs");
const args = process.argv.slice(2);
if (args[0] === "list") console.log("github.com/moment/moment/internal/archive");
else if (args.includes("-list")) console.log(${JSON.stringify(names.join("\n"))});
else fs.appendFileSync(process.env.SHARD_CALLS, JSON.stringify(args) + "\\n");
`,
    );
    chmodSync(fakeGo, 0o700);
    const env = {
      ...process.env,
      PATH: `${dir}:${process.env.PATH}`,
      SHARD_CALLS: calls,
    };
    const script = resolve("scripts/dev/test-go-group.mjs");
    for (let index = 1; index <= 4; index++) {
      execFileSync(
        process.execPath,
        [script, "archive", `--shard=${index}/4`],
        { env },
      );
    }
    const runs = readFileSync(calls, "utf8").trim().split("\n").map(JSON.parse);
    const covered = [];
    for (const args of runs) {
      assert.equal(args[0], "test");
      assert.ok(args.includes("-race"));
      const pattern = new RegExp(args[args.indexOf("-run") + 1]);
      const selected = names.filter((name) => pattern.test(name));
      assert.ok(selected.length > 0);
      assert.equal(pattern.test("TestAlphaExtra"), false);
      covered.push(...selected);
    }
    assert.deepEqual(covered.sort(), [...names].sort());
    for (const args of [
      ["archive", "--shard=5/4"],
      ["core", "--shard=1/4"],
      ["archive", "--shard=0/4"],
    ]) {
      assert.notEqual(
        spawnSync(process.execPath, [script, ...args], { env }).status,
        0,
      );
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
