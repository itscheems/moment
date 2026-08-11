# Releasing Moment

Moment v0 supports CGo-free native archives for Linux, macOS, and Windows on
amd64 and arm64. The container target is Linux and runs as fixed UID/GID 65532
with `/data` as its only writable authority volume. Go 1.25.12, Node 24.19.0,
pnpm 10.34.5, and every release/security tool are pinned in `.mise.toml`.

## Qualification

Install the locked tools and dependencies, then run:

```sh
mise install
mise exec -- task setup
mise exec -- task release:preflight
```

The preflight gate runs formatting, static analysis, race tests, Web tests,
six target cross-builds, vulnerability and secret scans, dependency-license
policy, a GoReleaser snapshot, checksum/SBOM/archive smoke checks, and a real
non-root container lifecycle. The container check initializes anonymous and
named volumes, starts through the image's default command, waits for
`/readyz`, persists a post across restart, and requires graceful SIGTERM.

The final qualification runs on the declared Linux x64 release runner and
includes a full scale gate that writes real bounded
image payloads rather than sparse placeholders and requires at least 110 GiB
free on the qualification volume:

```sh
mise exec -- task release:qualification
```

It uses the frozen fixture of 10,000 entries, 50,000 comments, 20,000 media
records, and 20 GiB of original bytes. After warm-up, the gate requires at
least 30 samples and enforces timeline p95 below 100 ms, search p95 below
200 ms, and year/month aggregate p95 below 250 ms. It records host OS/arch,
toolchain from the tested binary, commit, binary SHA-256, fixture and archive
hashes, elapsed time, thresholds, 31 samples, and peak subprocess RSS sampled
every 25 ms in `dist/performance-evidence.json`. A skipped or incomplete
full-scale run is not release qualification. The tagged workflow publishes the
same checksummed archives consumed by this gate; it does not rebuild them.
The streaming sequence exports the complete archive, reads and stages all of it
with a dry-run against an initialized empty target, and proves that target's
members, modes, link targets, and regular-file bytes are unchanged. A separate
initialized empty target then receives the actual import and must pass the
exact deep-doctor and real-socket API checks. Keeping validation before the
actual import avoids manufacturing a second rollback copy of an already
restored 20 GiB authority while preserving both measured archive passes.

On another native host, maintainers can exercise the identical real fixture,
socket latency, export, import, validation, and deep-doctor flow without
creating release qualification:

```sh
mise exec -- task release:performance:validation
```

This explicit mode keeps the same 110 GiB preflight and writes
`dist/performance-validation-evidence.json` with `validation_only: true` and
`qualified: false`. It records local measurements for diagnosis only. The
publish gate never consumes this file and requires a separate Linux/amd64
`performance-evidence.json` with `validation_only: false`.

## Release artifacts and evidence

Set `SOURCE_DATE_EPOCH` to the source commit timestamp before a tagged build.
GoReleaser writes six archives, SHA-256 `checksums.txt`, per-archive SBOMs, and
`release-evidence.json` under `dist/`. Every archive contains exactly one
`moment` executable plus `README.md`, `THIRD_PARTY_NOTICES.md`, and the complete
`THIRD_PARTY_LICENSES.txt` bundle. Smoke tests
initialize a clean instance from the native archive; that proves the same
binary contains both production Web assets and all SQLite migrations.

Release builds disable Go's automatic VCS stamping because checkout topology
can make that field identify a different worktree. Moment's version, commit,
and commit-time build date are injected from the clean source HEAD instead;
the verifier requires those fields, the artifact manifest, and qualification
evidence to agree exactly and rejects any automatic `vcs.*` build setting.

The release workflow blocks on every high or critical vulnerability, including
findings for which no fixed version is available.
Exceptions are not implicit: an exemption must name the advisory, affected
component, compensating control, owner, and expiry date in
`release/vulnerability-exemptions.json`. The committed file is empty for v0.
Scanner database identity and any active exemptions are captured in release
evidence. Hashes are generated artifacts and must never be copied from a
different commit.

The policy also records one non-exemption observation: `GO-2026-5932` marks
the unmaintained `golang.org/x/crypto/openpgp` package. The module is present
transitively, but `govulncheck` reports zero imported packages and zero
reachable symbols for Moment. Any future package or symbol reachability fails
the dynamic gate.

The release workflow also records an SPDX JSON SBOM and fixed UID, volume,
health, persistence, shutdown, image ID, and pinned base-image evidence for the
container. The committed vulnerability policy currently requires an empty
exemption list; an unexplained or expired waiver is never accepted.

## Install and smoke test

Download the archive for the target OS and architecture together with
`checksums.txt`, then verify it before extraction:

```sh
archive=moment_0.1.0_linux_amd64.tar.gz
grep "  ${archive}$" checksums.txt >checksums.target.txt
shasum -a 256 -c checksums.target.txt
tar -xzf "$archive"
./moment version --json
./moment init --data-dir ./moment-data --json
./moment serve --data-dir ./moment-data --listen 127.0.0.1:8080
```

Use the corresponding macOS archive. On Windows, compare the expected and
actual hash before expanding the ZIP:

```powershell
$archive = "moment_0.1.0_windows_amd64.zip"
$expected = ((Select-String -Path checksums.txt -Pattern "  $([regex]::Escape($archive))$").Line -split "\s+")[0]
$actual = (Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw "checksum mismatch" }
Expand-Archive $archive
```

Run the native commands with `moment.exe`. Linux, macOS, and Windows amd64
runners execute the complete host test suite, race detector, and real
cross-process lock tests. Arm64 archives for all three systems are CGo-free
cross-builds; v0 does not claim native Windows arm64 runtime qualification.

Build and exercise the container with an explicit authority volume. The owner
token is printed only by the one-off initialization command, so capture it in a
password manager rather than logs or shell history:

```sh
docker build -f deploy/Dockerfile -t moment:local .
docker volume create moment-data
docker run --rm --mount type=volume,src=moment-data,dst=/data \
  moment:local owner init --data-dir /data --json
docker run --name moment --mount type=volume,src=moment-data,dst=/data \
  -p 127.0.0.1:8080:8080 moment:local
docker inspect --format '{{.State.Health.Status}}' moment
docker stop --time 15 moment
docker rm moment
docker run --name moment --mount type=volume,src=moment-data,dst=/data \
  -p 127.0.0.1:8080:8080 moment:local
```

Every overridden container command, including `doctor`, backup, and restore,
must pass `--data-dir /data` explicitly. Reusing the volume across an image
upgrade preserves the native data layout. Readiness is served by `/readyz`, and
SIGTERM must complete within the 15-second shutdown window.

The final browser and accessibility qualification is a separate release gate;
native/container qualification does not claim visual acceptance.
