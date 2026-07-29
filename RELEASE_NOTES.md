# Release notes

## 0.14.1 release candidate

### Compatibility and migration

- cmgr now uses SQLite schema version 2. Existing unversioned, version 0, and
  version 1 databases are migrated transactionally at startup. The migration
  adds SHA-256 challenge digests, explicit schema ownership, persisted network
  policy, and deferred Docker cleanup records. Before the first upgrade, stop
  every process sharing `CMGR_DB` and make your own verified, timestamped
  backup; keep it until the upgraded deployment has been validated.

- As a second safety layer, cmgr creates a transactionally consistent backup
  immediately before migrating an existing older database. It is retained as
  `<CMGR_DB>.pre-migration-v<old>-to-v<new>-<UTC timestamp>.bak` whether the
  migration succeeds or fails, and migration is aborted if the backup cannot
  be created. Before attempting that backup, cmgr creates
  `<CMGR_DB>.cmgr-migration-latch` and removes it only after migration and
  schema validation succeed. A failed or interrupted migration leaves the
  latch in place, and later startups stop before creating another backup or
  changing the database until an operator repairs or restores the database and
  moves the latch aside. Automatic backup publication also refuses to
  overwrite an existing destination. This does not replace the
  operator-managed backup. Recovery and SQLite sidecar-file handling are
  documented in the README. Databases from newer cmgr versions or databases
  missing required schema invariants are rejected instead of being modified
  opportunistically.

- Flag formats are now limited to 128 bytes and must contain exactly one
  literal `%s`. Other `fmt.Sprintf` directives, including width or precision
  forms such as `%08s`, and escaped percent sequences such as `%%`, are
  rejected. The substituted value remains the same eight-character lowercase
  hexadecimal value, so formats such as `flag{%s}` retain their prior output.

- Modern JSON challenge files and YAML challenge-options blocks now reject
  unknown fields. Correct misspelled or obsolete fields before upgrading.
  Defined legacy catalog fields remain accepted as compatibility no-ops on
  modern challenge types and continue to support Hacksport challenges.

- Challenge source identity now uses a framed SHA-256 digest. Legacy CRC32
  fields remain in the API and retain their historical calculation for
  compatibility. Migrated rows have an empty digest until the next
  `cmgr update`, which intentionally re-evaluates the challenge and rebuilds
  its existing builds once under the new identity.

### Network and runtime policy

- New challenge networks deny Internet egress by default in the standard
  Docker bridge/NAT topology by disabling IP masquerading while preserving
  published ingress ports. Set `allow_egress: true` in Challenge Options when
  outbound access is required. Existing Docker networks are not mutated in
  place; destroy and recreate existing instances to apply the new default.
  Deployments that deliberately route Docker container subnets must enforce a
  hard egress deny in the host firewall as well.

- Runtime containers now default to 1 CPU, 512 MiB memory with additional swap
  disabled, 256 PIDs, and a `nofile` limit of 4096. Challenge options may set
  higher or lower values. All defaults are configurable through the
  `CMGR_DEFAULT_*` environment variables documented in the README.

- cmgr does not cap the number of active instances. The default limit of
  10,000 seeds applies only to a single challenge in one request and can be
  raised with `CMGR_MAX_SEEDS_PER_REQUEST`. Concurrent Docker builds default
  to four and are independently configurable.

- Artifact archives default to at most 10,000 entries, 5 GiB total
  uncompressed data, and 1 GiB per file. Build contexts, daemon request
  bodies, solver duration, solver logs, and solver flag output also have
  configurable safety bounds.

- Writable-layer quotas remain opt-in through
  `CMGR_ENABLE_DISK_QUOTAS`. cmgr applies them only when Docker reports ZFS or
  `overlay2` backed by XFS. If an XFS filesystem lacks the required `pquota`
  mount option, cmgr retries container creation without the quota, disables
  further quota attempts for that manager process, and emits a warning.
  `overlay2` on ext4 and Docker installations using the newer containerd image
  store are treated as unsupported unless Docker reports one of the verified
  storage-driver configurations.

### Correctness and hardening

- Process-shared operation locking now uses a writer-preference gate so a
  pending challenge update, schema convergence, migration, or recovery pass
  cannot be starved by a stream of later builds. Independent ordinary builds
  retain shared access and can still run concurrently. Schema creation,
  updates, and deletion are exclusive for the complete convergence interval.

- Host-port selection is protected by a database-scoped process lock from
  selection through Docker creation and database persistence. Parallel
  `cmgr`/`cmgrd` processes using one database can no longer select the same
  not-yet-persisted port.

- Startup recovery now reconciles incomplete instances and instances whose
  tracked Docker containers no longer exist. Broken dynamic instances are
  removed; fixed-count schema builds are restored to their configured
  capacity. Multi-container cleanup retains the complete ownership record
  until every sibling is gone, and transient Docker failures preserve state
  for a later retry.

- Schema convergence stages and validates replacement builds before activating
  a new definition, scales up before destructive cleanup, and persists
  explicit ownership for empty and manual schemas.

- Challenge, build, instance, and solve metadata updates now use checked
  transactions and preserve the original operation error if rollback also
  fails. Failed container cutovers are retained for cleanup and retried on the
  next startup.

- Challenge updates now hold a process-shared exclusive lock associated with
  `CMGR_DB`. Startup cleanup and other mutating `cmgr`/`cmgrd` operations wait
  for that update to finish, preventing a concurrent process from deleting
  rollback-reserve containers or starting an instance from a candidate image.
  This creates a sibling `<CMGR_DB>.cmgr.lock` file; container deployments must
  share the database directory, not only the database file. Migrations and
  recovery cleanup remain exclusive, while a process opening an already-current
  database may use shared startup access when another ordinary operation is
  active. This permits separate processes to build distinct challenges
  concurrently instead of serializing behind startup cleanup.

- Rebuilding a challenge now persists an incomplete-update marker until every
  build and running instance has cut over successfully. If `cmgr` is killed
  between builds, the next update retries the whole challenge instead of
  treating a mixed old/new generation as unmodified, and removes orphaned
  staging images and artifacts after convergence.

- SQLite connections now wait for short-lived writer locks instead of
  immediately returning `SQLITE_BUSY` during concurrent API requests. An
  instance is finalized only after Docker reports its containers running and
  every declared port assigned; immediate crash loops and incomplete port
  metadata now roll back atomically.

- Challenge metadata must be a regular file. Build-context, solver-context,
  and artifact handling reject traversal, symlinks, unsupported file types,
  duplicate archive paths, and oversized content. Artifact installation is
  atomic, and `convert-to-custom` stages both outputs and rolls back ordinary
  installation failures.

- Registry authentication uses Docker's canonical encoding, build cache tags
  use SHA-256 source identity, build generation is protected against duplicate
  concurrent work, and malformed or duplicate Docker build metadata is
  rejected.

- Failed challenge, frozen-base, and solver image builds now force-remove
  Docker intermediate containers instead of leaking stopped build containers
  on the daemon.

- `cmgrd` now enforces bounded, single-value JSON bodies with unknown-field
  rejection; preserves database errors; and returns 400, 404, or 409 for typed
  client errors. Its unauthenticated deployment model is unchanged.

- `cmgr version` now prints version information without requiring a usable
  Docker connection or database.

- Solver execution has bounded time and output, playtest HTML is sanitized,
  and playtest shutdown now cleans up its instance and build. Playtest flag
  submission intentionally remains a GET request because it is an internal
  testing interface.

- Deterministic flag generation from the challenge, format, and seed is
  unchanged. The hacksport compatibility runner receives and uses the seed,
  may generate its own flag, and cmgr persists the flag returned in hacksport
  build metadata.
