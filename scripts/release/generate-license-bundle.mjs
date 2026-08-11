#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import process from "node:process";

const repository = resolve(import.meta.dirname, "../..");
const destination = resolve(repository, "THIRD_PARTY_LICENSES.txt");
const indexDestination = resolve(repository, "release/third-party-components.json");
const allowed = new Set([
  "Apache-2.0",
  "BSD-2-Clause",
  "BSD-3-Clause",
  "ISC",
  "MIT",
  "Unicode-3.0",
]);

function parseJSONStream(input) {
  const values = [];
  let start = -1;
  let depth = 0;
  let quoted = false;
  let escaped = false;
  for (let index = 0; index < input.length; index += 1) {
    const character = input[index];
    if (quoted) {
      if (escaped) escaped = false;
      else if (character === "\\") escaped = true;
      else if (character === '"') quoted = false;
      continue;
    }
    if (character === '"') {
      quoted = true;
      continue;
    }
    if (character === "{") {
      if (depth === 0) start = index;
      depth += 1;
    } else if (character === "}") {
      depth -= 1;
      if (depth === 0 && start >= 0) {
        values.push(JSON.parse(input.slice(start, index + 1)));
        start = -1;
      }
    }
  }
  if (depth !== 0 || quoted) throw new Error("incomplete JSON stream from go list");
  return values;
}

function licenseFiles(directory) {
  if (!directory || !existsSync(directory)) return [];
  return readdirSync(directory)
    .filter((name) => /^(licen[cs]e|copying|notice)(\.|$)/i.test(name))
    .sort()
    .map((name) => ({ name, text: readFileSync(resolve(directory, name), "utf8").trim() }));
}

function classify(text) {
  if (/Apache License\s+Version 2\.0/i.test(text)) return "Apache-2.0";
  if (/Unicode License V3/i.test(text)) return "Unicode-3.0";
  if (/Permission to use, copy, modify, and\/?or distribute this software for any purpose with or without fee/i.test(text)) return "ISC";
  if (/Permission is hereby granted, free of charge, to any person obtaining\s+a copy/i.test(text)) return "MIT";
  if (/Redistribution and use in source and binary forms/i.test(text)) {
    return /Neither the name|names of its contributors/i.test(text) ? "BSD-3-Clause" : "BSD-2-Clause";
  }
  throw new Error("unrecognized third-party license text");
}

const components = [];
const goModules = new Map();
for (const target of [
  ["linux", "amd64"],
  ["linux", "arm64"],
  ["darwin", "amd64"],
  ["darwin", "arm64"],
  ["windows", "amd64"],
  ["windows", "arm64"],
]) {
  const goPackages = parseJSONStream(
    execFileSync("go", ["list", "-deps", "-json", "./cmd/moment"], {
      cwd: repository,
      encoding: "utf8",
      maxBuffer: 32 << 20,
      env: { ...process.env, CGO_ENABLED: "0", GOOS: target[0], GOARCH: target[1] },
    }),
  );
  for (const item of goPackages) {
    const module = item.Module;
    if (item.Standard || !module || module.Main) continue;
    goModules.set(`${module.Path}@${module.Version}`, module);
  }
}
for (const module of goModules.values()) {
  const files = licenseFiles(module.Dir);
  if (files.length === 0) throw new Error(`missing license files for Go module ${module.Path}@${module.Version}`);
  const license = classify(files.map((file) => file.text).join("\n"));
  if (!allowed.has(license)) throw new Error(`disallowed Go license ${license} for ${module.Path}`);
  components.push({
    ecosystem: "Go",
    name: module.Path,
    version: module.Version,
    license,
    source: `https://pkg.go.dev/${module.Path}@${module.Version}`,
    files,
  });
}

const goRoot = execFileSync("go", ["env", "GOROOT"], { cwd: repository, encoding: "utf8" }).trim();
const goVersion = execFileSync("go", ["env", "GOVERSION"], { cwd: repository, encoding: "utf8" }).trim();
const standardFiles = licenseFiles(goRoot);
if (standardFiles.length === 0) throw new Error("Go standard library license is missing");
const standardLicense = classify(standardFiles.map((file) => file.text).join("\n"));
if (!allowed.has(standardLicense)) throw new Error(`disallowed Go standard library license ${standardLicense}`);
components.push({
  ecosystem: "Go",
  name: "Go standard library",
  version: goVersion,
  license: standardLicense,
  source: "https://go.dev/LICENSE",
  files: standardFiles,
});

const webLicenses = JSON.parse(
  execFileSync("pnpm", ["--dir", "web", "licenses", "list", "--prod", "--json"], {
    cwd: repository,
    encoding: "utf8",
  }),
);
for (const [declaredLicense, packages] of Object.entries(webLicenses)) {
  if (!allowed.has(declaredLicense)) throw new Error(`disallowed Web license ${declaredLicense}`);
  for (const item of packages) {
    const files = licenseFiles(item.paths[0]);
    if (files.length === 0) throw new Error(`missing license files for Web package ${item.name}`);
    let detected;
    try {
      detected = classify(files.map((file) => file.text).join("\n"));
    } catch (error) {
      throw new Error(`unrecognized license text for Web package ${item.name}`, { cause: error });
    }
    if (detected !== declaredLicense) {
      throw new Error(`Web license mismatch for ${item.name}: declared ${declaredLicense}, detected ${detected}`);
    }
    for (const version of item.versions) {
      components.push({
        ecosystem: "Web",
        name: item.name,
        version,
        license: detected,
        source: item.homepage ?? "package metadata",
        files,
      });
    }
  }
}

components.sort((left, right) =>
  `${left.ecosystem}:${left.name}:${left.version}`.localeCompare(`${right.ecosystem}:${right.name}:${right.version}`),
);

const sections = [
  "Moment third-party license bundle",
  "",
  "This file is generated from go.sum and web/pnpm-lock.yaml by the pinned release toolchain.",
  `It contains ${components.length} component records and their offline copyright, notice, and license texts.`,
];
for (const component of components) {
  sections.push(
    "",
    "================================================================================",
    `Ecosystem: ${component.ecosystem}`,
    `Component: ${component.name}`,
    `Version: ${component.version}`,
    `Detected license: ${component.license}`,
    `Source: ${component.source}`,
  );
  for (const file of component.files) {
    sections.push("", `----- BEGIN ${file.name} -----`, file.text, `----- END ${file.name} -----`);
  }
}
const output = `${sections.join("\n")}\n`;
function componentPurl(component) {
  if (component.ecosystem === "Web") {
    const name = component.name.startsWith("@") ? `%40${component.name.slice(1)}` : component.name;
    return `pkg:npm/${name}@${component.version}`;
  }
  if (component.name === "Go standard library") return `pkg:golang/stdlib@${component.version}`;
  return `pkg:golang/${component.name}@${component.version}`;
}
const componentIndex = `${JSON.stringify(
  {
    schema_version: 1,
    components: components.map(({ ecosystem, name, version, license, source }) => ({
      ecosystem,
      name,
      version,
      license,
      source,
      purl: componentPurl({ ecosystem, name, version }),
    })),
  },
  null,
  2,
)}\n`;

if (process.argv.includes("--check")) {
  if (!existsSync(destination) || readFileSync(destination, "utf8") !== output ||
      !existsSync(indexDestination) || readFileSync(indexDestination, "utf8") !== componentIndex) {
    throw new Error("third-party license artifacts are stale; run the license bundle generator");
  }
} else {
  writeFileSync(destination, output, { mode: 0o644 });
  writeFileSync(indexDestination, componentIndex, { mode: 0o644 });
}
