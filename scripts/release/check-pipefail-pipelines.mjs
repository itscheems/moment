#!/usr/bin/env node

import { readdir, readFile } from "node:fs/promises";
import { join, relative } from "node:path";

const root = new URL("../../", import.meta.url);
const scriptsRoot = new URL("./scripts/", root);
const unsafeConsumer = /(?<!\|)\|(?!\|).*\b(?:head(?:\s|$)|grep\s+(?:-[A-Za-z]*q[A-Za-z]*\b|--quiet\b))/;
const failures = [];

for (const example of [
  "docker logs service | grep -q stopped",
  "docker port service | sed -n '1p' | head -n 1",
  "producer | grep --quiet value",
]) {
  if (!unsafeConsumer.test(example)) {
    throw new Error(`pipefail checker did not reject fixture: ${example}`);
  }
}
for (const example of [
  "grep -q stopped captured.log",
  "producer | jq -e '.ok'",
  "command >/dev/null || true",
]) {
  if (unsafeConsumer.test(example)) {
    throw new Error(`pipefail checker rejected safe fixture: ${example}`);
  }
}

async function visit(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      await visit(path);
      continue;
    }
    if (!entry.isFile() || !entry.name.endsWith(".sh")) {
      continue;
    }

    const lines = (await readFile(path, "utf8")).split("\n");
    lines.forEach((line, index) => {
      if (unsafeConsumer.test(line)) {
        failures.push(`${relative(root.pathname, path)}:${index + 1}: ${line.trim()}`);
      }
    });
  }
}

await visit(scriptsRoot.pathname);
if (failures.length > 0) {
  console.error("pipefail-unsafe early-exit pipeline(s) found:");
  failures.forEach((failure) => console.error(`  ${failure}`));
  process.exit(1);
}

console.log("pipefail early-exit pipeline gate passed");
