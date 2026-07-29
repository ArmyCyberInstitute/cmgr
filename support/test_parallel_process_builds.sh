#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 ]]; then
    echo "usage: $0 <cmgr-binary> <challenge-directory>" >&2
    exit 2
fi

cmgr_bin="$(realpath "$1")"
challenge_dir="$(realpath "$2")"
test_root="${CMGR_PARALLEL_TEST_ROOT:-$(mktemp -d)}"
created_test_root=
if [[ -z "${CMGR_PARALLEL_TEST_ROOT:-}" ]]; then
    created_test_root=1
fi

mkdir -p "$test_root/artifacts"
export CMGR_DB="$test_root/cmgr.db"
export CMGR_DIR="$challenge_dir"
export CMGR_ARTIFACT_DIR="$test_root/artifacts"

lock_holder=
cleanup() {
    if [[ -n "$lock_holder" ]]; then
        kill "$lock_holder" >/dev/null 2>&1 || true
        wait "$lock_holder" >/dev/null 2>&1 || true
    fi
    if [[ -n "$created_test_root" ]]; then
        find "$test_root" -depth -delete
    fi
}
trap cleanup EXIT

"$cmgr_bin" update "$challenge_dir"

lock_ready="$test_root/shared-lock-ready"
(
    exec 9>"${CMGR_DB}.cmgr.lock"
    flock --shared 9
    touch "$lock_ready"
    exec sleep 900
) &
lock_holder=$!

for _ in $(seq 1 100); do
    if [[ -e "$lock_ready" ]]; then
        break
    fi
    sleep 0.05
done
if [[ ! -e "$lock_ready" ]]; then
    echo "shared operation-lock holder did not start" >&2
    exit 1
fi

# A current database must initialize under the existing shared lock. The
# exclusive-only startup behavior would time out here before any build began.
timeout 10 "$cmgr_bin" list >/dev/null

challenges=(
    "cmgr/examples/custom-socat"
    "cmgr/examples/node-server"
    "cmgr/examples/php-sqlite"
    "cmgr/examples/flask--sqlite"
)
pids=()
for index in "${!challenges[@]}"; do
    log="$test_root/build-$index.log"
    timeout 600 "$cmgr_bin" build \
        "${challenges[$index]}" \
        "$((5900 + index))" >"$log" 2>&1 &
    pids+=("$!")
done

failed=
for index in "${!pids[@]}"; do
    if ! wait "${pids[$index]}"; then
        echo "parallel build failed for ${challenges[$index]}" >&2
        sed -n '1,240p' "$test_root/build-$index.log" >&2
        failed=1
    fi
done
if [[ -n "$failed" ]]; then
    exit 1
fi
if ! kill -0 "$lock_holder" >/dev/null 2>&1; then
    echo "builds waited for the shared lock holder instead of running concurrently" >&2
    exit 1
fi

python3 - "$CMGR_DB" "${challenges[@]}" <<'PY'
import sqlite3
import sys

database, *expected = sys.argv[1:]
connection = sqlite3.connect(database)
try:
    integrity = connection.execute("PRAGMA quick_check").fetchall()
    if integrity != [("ok",)]:
        raise SystemExit(f"database quick_check failed: {integrity!r}")
    foreign_keys = connection.execute("PRAGMA foreign_key_check").fetchall()
    if foreign_keys:
        raise SystemExit(f"database foreign-key violations: {foreign_keys!r}")

    rows = connection.execute(
        """
        SELECT challenge, COUNT(*)
        FROM builds
        WHERE schema LIKE 'manual-%' AND flag != ''
        GROUP BY challenge
        """
    ).fetchall()
    completed = {challenge: count for challenge, count in rows}
    wanted = {challenge: 1 for challenge in expected}
    if completed != wanted:
        raise SystemExit(
            f"unexpected completed parallel builds: got {completed!r}, want {wanted!r}"
        )

    incomplete = connection.execute(
        """
        SELECT id, challenge
        FROM builds
        WHERE schema LIKE 'manual-%' AND flag = ''
        ORDER BY id
        """
    ).fetchall()
    if incomplete:
        raise SystemExit(f"incomplete parallel builds remain: {incomplete!r}")
finally:
    connection.close()
PY

mapfile -t build_ids < <(
    python3 - "$CMGR_DB" "${challenges[@]}" <<'PY'
import sqlite3
import sys

database, *challenges = sys.argv[1:]
connection = sqlite3.connect(database)
try:
    for challenge in challenges:
        rows = connection.execute(
            """
            SELECT id
            FROM builds
            WHERE challenge = ? AND schema LIKE 'manual-%' AND flag != ''
            ORDER BY id
            """,
            (challenge,),
        ).fetchall()
        if len(rows) != 1:
            raise SystemExit(
                f"challenge {challenge!r} has {len(rows)} completed manual builds"
            )
        print(rows[0][0])
finally:
    connection.close()
PY
)

# Use exactly as many host ports as concurrent instances. Without a
# process-shared allocation lock, two processes can select the same uncommitted
# port and either Docker creation or the unique database constraint will fail.
export CMGR_PORTS=25300-25311
instance_pids=()
instance_logs=()
for build_id in "${build_ids[@]}"; do
    for copy in 1 2 3; do
        log="$test_root/start-${build_id}-${copy}.log"
        timeout 120 "$cmgr_bin" start "$build_id" >"$log" 2>&1 &
        instance_pids+=("$!")
        instance_logs+=("$log")
    done
done

failed=
for index in "${!instance_pids[@]}"; do
    if ! wait "${instance_pids[$index]}"; then
        echo "parallel instance start failed" >&2
        sed -n '1,240p' "${instance_logs[$index]}" >&2
        failed=1
    fi
done
if [[ -n "$failed" ]]; then
    exit 1
fi

python3 - "$CMGR_DB" <<'PY'
import sqlite3
import sys

database = sys.argv[1]
connection = sqlite3.connect(database)
try:
    integrity = connection.execute("PRAGMA quick_check").fetchall()
    if integrity != [("ok",)]:
        raise SystemExit(f"database quick_check failed after starts: {integrity!r}")
    foreign_keys = connection.execute("PRAGMA foreign_key_check").fetchall()
    if foreign_keys:
        raise SystemExit(
            f"database foreign-key violations after starts: {foreign_keys!r}"
        )

    instance_count = connection.execute(
        "SELECT COUNT(*) FROM instances"
    ).fetchone()[0]
    if instance_count != 12:
        raise SystemExit(f"got {instance_count} instances, want 12")

    assignment_count, distinct_ports = connection.execute(
        "SELECT COUNT(*), COUNT(DISTINCT port) FROM portAssignments"
    ).fetchone()
    if assignment_count != 12 or distinct_ports != 12:
        raise SystemExit(
            "host-port assignments are not unique and complete: "
            f"rows={assignment_count}, distinct={distinct_ports}"
        )

    incomplete = connection.execute(
        """
        SELECT instances.id
        FROM instances
        LEFT JOIN containers ON containers.instance = instances.id
        GROUP BY instances.id
        HAVING COUNT(containers.id) = 0
        ORDER BY instances.id
        """
    ).fetchall()
    if incomplete:
        raise SystemExit(f"incomplete parallel instances remain: {incomplete!r}")
finally:
    connection.close()
PY

echo "four builds and twelve port-bearing starts completed across separate cmgr processes"
