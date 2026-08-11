#!/usr/bin/env node

import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, resolve } from "node:path";
import process from "node:process";

const repository = resolve(import.meta.dirname, "../..");
const commit = execFileSync("git", ["rev-parse", "HEAD"], { cwd: repository, encoding: "utf8" }).trim();
const epoch = Number(execFileSync("git", ["show", "-s", "--format=%ct", "HEAD"], { cwd: repository, encoding: "utf8" }).trim());
const releaseTag = process.env.MOMENT_RELEASE_TAG;
const expectedVersion = process.env.MOMENT_EXPECTED_VERSION ??
  (releaseTag?.startsWith("v") ? releaseTag.slice(1) : undefined);
if (!expectedVersion) throw new Error("MOMENT_EXPECTED_VERSION is required");
const goos = execFileSync("go", ["env", "GOOS"], { encoding: "utf8" }).trim();
const goarch = execFileSync("go", ["env", "GOARCH"], { encoding: "utf8" }).trim();
const target = `_${expectedVersion}_${goos}_${goarch}`;
const manifest = readFileSync(resolve(repository, "dist/checksums.txt"), "utf8")
  .trim()
  .split("\n")
  .map((line) => line.trim().split(/\s+/));
const candidate = manifest.find(([, name]) => name.includes(target) && (name.endsWith(".tar.gz") || name.endsWith(".zip")));
if (!candidate) throw new Error(`native release archive not found for ${goos}/${goarch}`);
const [expectedSHA256, archiveName] = candidate;
const archivePath = resolve(repository, "dist", archiveName);
const actualSHA256 = createHash("sha256").update(readFileSync(archivePath)).digest("hex");
if (actualSHA256 !== expectedSHA256) throw new Error(`checksum mismatch for ${archiveName}`);

const scratch = mkdtempSync(resolve(tmpdir(), "moment-native-artifact-"));
try {
  execFileSync("tar", ["-xf", archivePath, "-C", scratch], { stdio: "inherit" });
  const binary = resolve(scratch, goos === "windows" ? "moment.exe" : "moment");
  const version = JSON.parse(execFileSync(binary, ["version", "--json"], { encoding: "utf8" }));
  const buildDate = new Date(epoch * 1000).toISOString().replace(".000Z", "Z");
  if (
    version.ok !== true ||
    version.data?.version !== expectedVersion ||
    version.data?.commit !== commit ||
    version.data?.build_date !== buildDate
  ) {
    throw new Error(`native build identity mismatch for ${basename(archivePath)}`);
  }
  execFileSync(
    "go",
    [
      "test",
      "-count=1",
      "./cmd/moment",
      "-run",
      "^(TestServeBinaryStructuredLogsAndGracefulSignal|TestInitAliasAndBrokenPipeBinaryContracts)$",
    ],
    {
      cwd: repository,
      env: { ...process.env, MOMENT_SMOKE_BINARY: binary },
      stdio: "inherit",
    },
  );
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
