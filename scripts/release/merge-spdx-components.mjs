#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { basename, resolve } from "node:path";
import process from "node:process";

const repository = resolve(import.meta.dirname, "../..");
const index = JSON.parse(readFileSync(resolve(repository, "release/third-party-components.json"), "utf8"));
if (index.schema_version !== 1 || !Array.isArray(index.components)) {
  throw new Error("invalid third-party component index");
}
const epoch = Number(process.env.SOURCE_DATE_EPOCH);
if (!Number.isSafeInteger(epoch) || epoch <= 0) {
  throw new Error("SOURCE_DATE_EPOCH is required to finalize deterministic SBOMs");
}

function identifier(component) {
  return `SPDXRef-Moment-${createHash("sha256")
    .update(`${component.ecosystem}:${component.name}@${component.version}`)
    .digest("hex")
    .slice(0, 24)}`;
}

const additions = index.components.filter(
  (component) => component.ecosystem === "Web" || component.name === "Go standard library",
);
for (const argument of process.argv.slice(2)) {
  const path = resolve(argument);
  const document = JSON.parse(readFileSync(path, "utf8"));
  if (document.spdxVersion !== "SPDX-2.3" || !Array.isArray(document.packages) || !Array.isArray(document.relationships)) {
    throw new Error(`${argument} is not a complete SPDX 2.3 JSON document`);
  }
  const existing = new Set(document.packages.map((item) => `${item.name}@${item.versionInfo ?? ""}`));
  for (const component of additions) {
    if (existing.has(`${component.name}@${component.version}`)) continue;
    const SPDXID = identifier(component);
    document.packages.push({
      name: component.name,
      SPDXID,
      versionInfo: component.version,
      downloadLocation: "NOASSERTION",
      filesAnalyzed: false,
      licenseConcluded: component.license,
      licenseDeclared: component.license,
      copyrightText: "NOASSERTION",
      primaryPackagePurpose: "LIBRARY",
      externalRefs: [
        {
          referenceCategory: "PACKAGE-MANAGER",
          referenceType: "purl",
          referenceLocator: component.purl,
        },
      ],
    });
    document.relationships.push({
      spdxElementId: "SPDXRef-DOCUMENT",
      relationshipType: "DESCRIBES",
      relatedSpdxElement: SPDXID,
    });
  }
  document.packages.sort((left, right) => left.SPDXID.localeCompare(right.SPDXID));
  document.relationships.sort((left, right) =>
    `${left.spdxElementId}:${left.relationshipType}:${left.relatedSpdxElement}`.localeCompare(
      `${right.spdxElementId}:${right.relationshipType}:${right.relatedSpdxElement}`,
    ),
  );
  document.creationInfo.created = new Date(epoch * 1000).toISOString();
  const namespaceHash = createHash("sha256").update(`${basename(path)}:${epoch}`).digest("hex").slice(0, 32);
  document.documentNamespace = `urn:moment:spdx:${namespaceHash}`;
  writeFileSync(path, `${JSON.stringify(document, null, 2)}\n`, { mode: 0o644 });
}
