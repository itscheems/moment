#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import process from "node:process";

const [group, flag] = process.argv.slice(2);
if (
  !["archive", "lifecycle", "core"].includes(group) ||
  (flag && flag !== "--list")
) {
  throw new Error("usage: test-go-group.mjs <archive|lifecycle|core> [--list]");
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
else execFileSync("go", ["test", "-race", ...selected], { stdio: "inherit" });
