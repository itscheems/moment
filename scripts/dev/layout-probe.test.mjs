import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { builtStylesheets, resolveModuleClasses } from "./layout-probe.mjs";

test("reads CSS split across entry, shared, and lazy bundles in build order", (t) => {
  const root = mkdtempSync(join(tmpdir(), "moment-layout-assets-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const assets = join(root, "assets");
  mkdirSync(assets);
  writeFileSync(join(root, "index.html"), '<link rel="preload" as="style" href="/assets/ArticleEditor.css"><link href="/assets/shared.css" rel="stylesheet"><link rel="stylesheet" href="/assets/index.css">');
  writeFileSync(join(assets, "ArticleEditor.css"), "._editor_abc12_1 { color: red; }");
  writeFileSync(join(assets, "shared.css"), "._shared_def34_2 { color: blue; }");
  writeFileSync(join(assets, "index.css"), "._settingsNav_ghi56_3 { color: black; }");
  const result = builtStylesheets(assets);
  assert.match(result.css, /_settingsNav_ghi56_3/);
  assert.match(result.css, /_shared_def34_2/);
  assert.match(result.css, /_editor_abc12_1/);
  assert.deepEqual(result.paths, ["shared.css", "index.css", "ArticleEditor.css"].map((name) => join(assets, name)));
});

test("resolves a class in a later CSS chunk and deduplicates repeated selectors", () => {
  const result = resolveModuleClasses(".$settingsNav", "._editor_abc12_1{} ._settingsNav_ghi56_3{} ._settingsNav_ghi56_3:hover{}");
  assert.equal(result.resolved, "._settingsNav_ghi56_3");
  assert.deepEqual(result.missing, []);
  assert.deepEqual(result.ambiguous, []);
});

test("reports ambiguous source classes instead of choosing a different component", () => {
  const result = resolveModuleClasses(".$primaryButton", "._primaryButton_abc12_1{} ._primaryButton_def34_2{}");
  assert.deepEqual(result.ambiguous, [{ name: "primaryButton", classes: ["_primaryButton_abc12_1", "_primaryButton_def34_2"] }]);
});

test("keeps literal selectors and explains unknown source class names", () => {
  const result = resolveModuleClasses('[aria-label="settings"] .$missing', ".plain{}");
  assert.equal(result.resolved, '[aria-label="settings"] .$missing');
  assert.deepEqual(result.missing, ["missing"]);
});

test("runs help and argument failures without requiring a browser or build", () => {
  const script = fileURLToPath(new URL("./layout-probe.mjs", import.meta.url));
  const help = spawnSync(process.execPath, [script, "--help"], { encoding: "utf8" });
  assert.equal(help.status, 0, help.stderr);
  assert.match(help.stdout, /--fragment/);
  assert.equal(help.stderr, "");
  const invalid = spawnSync(process.execPath, [script, "--invalid-layout-option"], { encoding: "utf8" });
  assert.equal(invalid.status, 1);
  assert.equal(invalid.stdout, "");
  assert.match(invalid.stderr, /unknown option --invalid-layout-option/);
});
