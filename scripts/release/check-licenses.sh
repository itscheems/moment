#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

node scripts/release/generate-license-bundle.mjs --check

jq -e '
  .schema_version == 1 and .blocking_severities == ["Critical", "High"] and
  (.exemptions | type == "array") and (.exemptions | length == 0) and
  .observations == [{
    advisory:"GO-2026-5932", module:"golang.org/x/crypto", version:"v0.53.0", fixed_in:"N/A",
    disposition:"not_affected", basis:{scanner:"govulncheck",symbol_results:0,package_results:0,module_results:1,
    package:"golang.org/x/crypto/openpgp",package_imported_or_called:false}
  }]
' release/vulnerability-exemptions.json >/dev/null
