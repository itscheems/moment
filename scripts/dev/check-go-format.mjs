#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import process from "node:process";

const supplied = process.argv.slice(2);
const files = supplied.length
  ? supplied
  : execFileSync("git", ["ls-files", "-z", "--", "*.go"], {
      encoding: "utf8",
    })
      .split("\0")
      .filter(Boolean);

if (files.length) {
  const unformatted = execFileSync("gofmt", ["-l", ...files], {
    encoding: "utf8",
  });
  if (unformatted.trim()) {
    process.stderr.write(`Go files need formatting:\n${unformatted}`);
    process.exitCode = 1;
  }
}
