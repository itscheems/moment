#!/usr/bin/env node
//
// Sweep a set of surfaces across widths and themes in a real browser.
//
// layout-probe answers one question about one page. A regression is the same
// question asked about every surface at every width in every theme, and doing
// that by hand is how a surface gets skipped and how the pile of screenshots
// left behind stops meaning anything a week later.
//
// This drives layout-probe over the matrix, writes the shots into one
// directory with names that say what they are, and leaves an index.md that can
// be read top to bottom.
//
// It also refuses a theme that did not take. The document root just receives
// whatever string --theme is given, so a name this project does not define
// renders light and reports success -- which looks exactly like a broken dark
// mode. Comparing the measured background against light's catches it here
// rather than in someone's reading of a screenshot.

import { spawnSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import process from "node:process";

const repository = resolve(import.meta.dirname, "../..");
const probeScript = join(repository, "scripts/dev/layout-probe.mjs");
const lightBackground = "rgb(255, 255, 255)";

const usage = `Capture one surface matrix in a real browser.

  --url <origin>       the running instance, e.g. http://127.0.0.1:19090
  --out <directory>    where the shots and index.md are written
  --surface <n=path>   a named route to capture; repeatable
  --widths <list>      comma separated, default 390,768,1440
  --themes <list>      comma separated, default light,dim
  --height <px>        viewport height, default 900
  --settle <ms>        how long to wait for the page, default 2500

Themes are this project's own names. The dark ones are "dim" and "lights-out";
there is no theme called "dark", and asking for one renders light.

  node scripts/dev/ui-regression.mjs --url http://127.0.0.1:19090 \\
    --out /tmp/regression \\
    --surface 'timeline=/' --surface 'article=/p/01ARZ3NDEKTSV4RRFFQ69G5FAV'
`;

function fail(message) {
  process.stderr.write(`ui-regression: ${message}\n`);
  process.exit(1);
}

function parseArguments(argv) {
  const options = {
    surfaces: [],
    widths: [390, 768, 1440],
    themes: ["light", "dim"],
    height: 900,
    settle: 2500,
  };
  for (let index = 0; index < argv.length; index += 1) {
    const flag = argv[index];
    const value = argv[index + 1];
    switch (flag) {
      case "--help":
      case "-h":
        process.stdout.write(usage);
        process.exit(0);
        break;
      case "--url":
        options.url = value;
        index += 1;
        break;
      case "--out":
        options.out = value;
        index += 1;
        break;
      case "--surface": {
        const separator = (value ?? "").indexOf("=");
        if (separator < 1) fail(`--surface wants name=path, got ${value}`);
        options.surfaces.push({
          name: value.slice(0, separator),
          path: value.slice(separator + 1),
        });
        index += 1;
        break;
      }
      case "--widths":
        options.widths = value.split(",").map((entry) => Number.parseInt(entry, 10));
        index += 1;
        break;
      case "--themes":
        options.themes = value.split(",").map((entry) => entry.trim()).filter(Boolean);
        index += 1;
        break;
      case "--height":
        options.height = Number.parseInt(value, 10);
        index += 1;
        break;
      case "--settle":
        options.settle = Number.parseInt(value, 10);
        index += 1;
        break;
      default:
        fail(`unknown option ${flag}`);
    }
  }
  if (!options.url) fail("--url is required");
  if (!options.out) fail("--out is required");
  if (options.surfaces.length === 0) fail("at least one --surface is required");
  if (options.widths.some((width) => !Number.isFinite(width) || width <= 0)) {
    fail("--widths wants positive numbers");
  }
  return options;
}

function probe(options, path, width, theme, shot) {
  const args = [
    probeScript,
    "--url", new URL(path, options.url).toString(),
    "--width", String(width),
    "--height", String(options.height),
    "--theme", theme,
    "--settle", String(options.settle),
    "--measure", "body",
    "--computed", "background-color",
    "--json",
  ];
  if (shot) args.push("--shot", join(options.out, shot));
  const result = spawnSync(process.execPath, args, {
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
  });
  if (result.status !== 0) return { failure: (result.stderr || "probe failed").trim() };
  try {
    return { report: JSON.parse(result.stdout) };
  } catch {
    return { failure: "probe did not print JSON" };
  }
}

// What the not-found page calls itself, learned rather than written down, so a
// change to the wording does not quietly turn this check off.
//
// Without it a mistyped route is photographed and reported as a pass: the shot
// is of a real page that rendered correctly, just not the one anybody asked
// for, and nothing in a screenshot pile says so.
function learnNotFoundTitle(options) {
  const { report, failure } = probe(options, "/__ui-regression-asks-for-a-page-that-is-not-there", 1280, "light");
  if (failure) fail(`could not reach ${options.url}: ${failure}`);
  return report.title ?? "";
}

function capture(options, surface, width, theme) {
  const shot = `${surface.name}-${width}-${theme}.png`;
  const { report: measured, failure } = probe(options, surface.path, width, theme, shot);
  if (failure) return { shot, failure };
  if (measured.title && measured.title === options.notFoundTitle) {
    return { shot, failure: `${surface.path} rendered the not-found page` };
  }
  // A shot taken before the pictures decode shows a grey box where a cover is,
  // which reads as a broken image. That is false evidence, so it fails here.
  if (measured.painted === false) {
    return { shot, failure: "the shot was taken before everything on screen had decoded; raise --settle" };
  }
  const background = measured.measured?.body?.["background-color"] ?? "";
  // A theme name this project does not define is applied without complaint and
  // renders light. Only "light" may look like light.
  if (theme !== "light" && background === lightBackground) {
    return {
      shot,
      background,
      failure: `theme "${theme}" rendered as light, so it is not a theme this build defines`,
    };
  }
  return { shot, background };
}

function main() {
  const options = parseArguments(process.argv.slice(2));
  mkdirSync(options.out, { recursive: true });
  options.notFoundTitle = learnNotFoundTitle(options);

  const rows = [];
  const failures = [];
  for (const surface of options.surfaces) {
    for (const width of options.widths) {
      for (const theme of options.themes) {
        process.stderr.write(`${surface.name} ${width} ${theme} ... `);
        const captured = capture(options, surface, width, theme);
        process.stderr.write(captured.failure ? `FAILED: ${captured.failure}\n` : "ok\n");
        rows.push({ surface, width, theme, ...captured });
        if (captured.failure) failures.push(captured);
      }
    }
  }

  const lines = [
    `# 界面回归 ${new Date().toISOString().slice(0, 16).replace("T", " ")}`,
    "",
    `实例：${options.url}`,
    `共 ${rows.length} 张，失败 ${failures.length} 张。`,
    "",
  ];
  for (const surface of options.surfaces) {
    lines.push(`## ${surface.name} — \`${surface.path}\``, "");
    for (const row of rows.filter((entry) => entry.surface === surface)) {
      const label = `${row.width} / ${row.theme}`;
      lines.push(
        row.failure
          ? `- **${label} 失败**：${row.failure}`
          : `- ${label} — 背景 \`${row.background}\`\n\n  ![${label}](${row.shot})`,
      );
    }
    lines.push("");
  }
  writeFileSync(join(options.out, "index.md"), `${lines.join("\n")}\n`);
  process.stderr.write(`\n${rows.length - failures.length}/${rows.length} captured -> ${join(options.out, "index.md")}\n`);
  process.exit(failures.length === 0 ? 0 : 1);
}

main();
