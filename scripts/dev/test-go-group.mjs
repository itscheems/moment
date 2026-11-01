#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import process from "node:process";

const [group, flag] = process.argv.slice(2);
const shard = flag?.match(/^--shard=([1-9]\d*)\/([1-9]\d*)$/);
if (
  !["archive", "lifecycle", "core"].includes(group) ||
  (flag && flag !== "--list" && !shard) ||
  (shard && (group !== "archive" || Number(shard[1]) > Number(shard[2])))
) {
  throw new Error(
    "usage: test-go-group.mjs <archive|lifecycle|core> [--list|--shard=INDEX/TOTAL (archive only)]",
  );
}
const packages = execFileSync("go", ["list", "./..."], { encoding: "utf8" })
  .trim()
  .split(/\s+/);
const selected = packages.filter((name) => {
  const owner = name.endsWith("/internal/archive")
    ? "archive"
    : name.endsWith("/internal/devtools") ||
        name.endsWith("/internal/transport/cli")
      ? "lifecycle"
      : "core";
  return owner === group;
});
if (!selected.length) throw new Error(`no Go packages selected for ${group}`);
if (flag === "--list") process.stdout.write(selected.join("\n") + "\n");
else if (shard) {
  const names = execFileSync("go", ["test", "-list", ".", ...selected], {
    encoding: "utf8",
  })
    .split(/\r?\n/)
    .filter((name) => /^(Test|Example|Fuzz)/.test(name))
    .sort();
  const slice = names.filter(
    (_, index) => index % Number(shard[2]) === Number(shard[1]) - 1,
  );
  if (!slice.length) throw new Error("Archive test shard is empty");
  process.stdout.write(
    `Archive shard ${shard[1]}/${shard[2]}: ${slice.length}/${names.length} tests\n`,
  );
  execFileSync(
    "go",
    ["test", "-race", "-run", `^(${slice.join("|")})$`, ...selected],
    {
      stdio: "inherit",
    },
  );
} else execFileSync("go", ["test", "-race", ...selected], { stdio: "inherit" });
