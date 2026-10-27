import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const script = fileURLToPath(new URL("./check-go-format.mjs", import.meta.url));

for (const [name, source, expected] of [
  [
    "accepts formatted source without depending on Git cleanliness",
    "package example\n\nconst Value = 1\n",
    0,
  ],
  [
    "rejects unformatted source without rewriting it",
    "package example\nconst Value=1\n",
    1,
  ],
  ["rejects a gofmt parse failure", "package example\nfunc broken(\n", 1],
]) {
  test(name, () => {
    const dir = mkdtempSync(join(tmpdir(), "moment-go-format-"));
    try {
      const file = join(dir, "sample.go");
      writeFileSync(file, source);
      const result = spawnSync(process.execPath, [script, file], {
        encoding: "utf8",
      });
      assert.equal(result.status, expected, result.stderr);
      assert.equal(readFileSync(file, "utf8"), source);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
}
