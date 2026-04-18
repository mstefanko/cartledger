#!/usr/bin/env bash
#
# scripts/smoke.sh — end-to-end self-hosting smoke test.
#
# Walks the happy path against a freshly built binary:
#   1. Build → launch with mock LLM → /livez
#   2. Extract bootstrap token from stderr
#   3. /setup (with X-Bootstrap-Token) → /auth/login → /auth/profile
#   4. Upload a receipt (mock LLM), poll until the worker finishes so the DB
#      holds a receipt + line items + matched-product state to round-trip
#   5. Stop server, back up DATA_DIR via the CLI
#   6. Restore into a fresh DATA_DIR, re-launch, re-login
#   7. GET /api/v1/receipts on the restored server — assert the uploaded
#      receipt + its line items survived the backup/restore round-trip
#
# Designed to catch ~70% of deploy regressions: config loading, migrations,
# bootstrap token flow, cookie-based auth, backup/restore round-trip,
# receipt+line-item persistence through a backup cycle.
#
# Usage:
#   make smoke
#   VERBOSE=1 ./scripts/smoke.sh
#
# Requirements: bash, go, curl, python3 (jq NOT required).

set -euo pipefail

# -------- config --------
PORT="${SMOKE_PORT:-8089}"
BASE_URL="http://127.0.0.1:${PORT}"
BIN="${PWD}/bin/cartledger"
VERBOSE="${VERBOSE:-0}"

# Two separate DATA_DIRs — one for the original run, one for the restored run.
DATA_DIR_ORIG="$(mktemp -d -t cartledger-smoke-orig.XXXXXX)"
DATA_DIR_RESTORED="$(mktemp -d -t cartledger-smoke-restored.XXXXXX)"
BACKUP_FILE="$(mktemp -u -t cartledger-smoke-backup.XXXXXX).tgz"
STDERR_LOG_ORIG="$(mktemp -t cartledger-smoke-orig-stderr.XXXXXX)"
STDERR_LOG_RESTORED="$(mktemp -t cartledger-smoke-restored-stderr.XXXXXX)"
COOKIE_JAR="$(mktemp -t cartledger-smoke-cookies.XXXXXX)"

SERVER_PID=""

# -------- helpers --------
log()  { printf '[smoke] %s\n' "$*"; }
vlog() { [[ "${VERBOSE}" == "1" ]] && printf '[smoke:v] %s\n' "$*" || true; }
die()  { printf '[smoke] FAIL: %s\n' "$*" >&2; exit 1; }

cleanup() {
    local ec=$?
    if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
        vlog "killing server pid=${SERVER_PID}"
        kill "${SERVER_PID}" 2>/dev/null || true
        wait "${SERVER_PID}" 2>/dev/null || true
    fi
    # Also nuke anything still listening on our port (orphaned child).
    if command -v lsof >/dev/null 2>&1; then
        local pids
        pids="$(lsof -t -i ":${PORT}" 2>/dev/null || true)"
        [[ -n "${pids}" ]] && kill -9 ${pids} 2>/dev/null || true
    fi
    if [[ "${VERBOSE}" == "1" && $ec -ne 0 ]]; then
        log "---- stderr (orig) ----"
        cat "${STDERR_LOG_ORIG}" 2>/dev/null || true
        log "---- stderr (restored) ----"
        cat "${STDERR_LOG_RESTORED}" 2>/dev/null || true
    fi
    rm -rf "${DATA_DIR_ORIG}" "${DATA_DIR_RESTORED}" "${STDERR_LOG_ORIG}" "${STDERR_LOG_RESTORED}" "${COOKIE_JAR}" "${BACKUP_FILE}" 2>/dev/null || true
    if [[ $ec -eq 0 ]]; then log "smoke OK"; else log "smoke FAILED (exit ${ec})"; fi
}
trap cleanup EXIT

wait_for_livez() {
    local deadline=$(( $(date +%s) + 15 ))
    while (( $(date +%s) < deadline )); do
        if curl -fsS -o /dev/null "${BASE_URL}/livez"; then
            return 0
        fi
        sleep 0.2
    done
    return 1
}

# Idempotency: free the port before we start in case a previous run left junk.
if command -v lsof >/dev/null 2>&1; then
    pids="$(lsof -t -i ":${PORT}" 2>/dev/null || true)"
    [[ -n "${pids}" ]] && { vlog "freeing port ${PORT} from pids: ${pids}"; kill -9 ${pids} 2>/dev/null || true; }
fi

# -------- 1. build --------
log "building ${BIN}"
mkdir -p "${PWD}/bin"
go build -o "${BIN}" ./cmd/server

# -------- env shared by all server invocations --------
# 32-char JWT secret (deterministic, not a production secret — this is smoke-only).
export JWT_SECRET="smoke-test-jwt-secret-0123456789a"
export LLM_PROVIDER="mock"
export ALLOWED_ORIGINS="${BASE_URL}"
export PORT
export CARTLEDGER_ENV="development"
export RATE_LIMIT_ENABLED="false"

launch_server() {
    local data_dir="$1" stderr_log="$2"
    DATA_DIR="${data_dir}" "${BIN}" 2> "${stderr_log}" &
    SERVER_PID=$!
    vlog "server launched pid=${SERVER_PID} data_dir=${data_dir}"
}

stop_server() {
    if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
        kill "${SERVER_PID}" 2>/dev/null || true
        wait "${SERVER_PID}" 2>/dev/null || true
    fi
    SERVER_PID=""
}

# -------- 2. launch original server --------
log "launching server against fresh DATA_DIR"
launch_server "${DATA_DIR_ORIG}" "${STDERR_LOG_ORIG}"

log "waiting for /livez"
wait_for_livez || {
    log "---- server stderr ----"
    cat "${STDERR_LOG_ORIG}"
    die "/livez did not respond within 15s"
}

# -------- 3. extract bootstrap token --------
# Banner line form: http://localhost:<port>/setup?bootstrap=<token>
# Try a few times in case the banner hasn't been flushed yet.
TOKEN=""
for _ in 1 2 3 4 5 6 7 8 9 10; do
    TOKEN="$(grep -oE 'bootstrap=[A-Za-z0-9_-]+' "${STDERR_LOG_ORIG}" | head -n1 | cut -d= -f2 || true)"
    [[ -n "${TOKEN}" ]] && break
    sleep 0.2
done
[[ -n "${TOKEN}" ]] || { cat "${STDERR_LOG_ORIG}"; die "could not find bootstrap token in stderr"; }
vlog "bootstrap token: ${TOKEN}"

# -------- 4. POST /api/v1/setup --------
export EMAIL="smoke+$(date +%s)@example.com"
export PASSWORD="smoke-password-123"

log "POST /api/v1/setup"
setup_body=$(python3 -c '
import json, os
print(json.dumps({
    "household_name": "Smoke House",
    "user_name": "Smoke Tester",
    "email": os.environ["EMAIL"],
    "password": os.environ["PASSWORD"],
}))
')
setup_status=$(curl -sS -o /tmp/smoke-setup.json -w '%{http_code}' \
    -X POST "${BASE_URL}/api/v1/setup" \
    -H "Content-Type: application/json" \
    -H "X-Bootstrap-Token: ${TOKEN}" \
    --data "${setup_body}")

if [[ "${setup_status}" != "201" && "${setup_status}" != "200" ]]; then
    cat /tmp/smoke-setup.json
    die "setup returned ${setup_status} (expected 201)"
fi
vlog "setup status=${setup_status}"

# -------- 5. POST /api/v1/login --------
log "POST /api/v1/login"
login_body=$(python3 -c '
import json, os
print(json.dumps({"email": os.environ["EMAIL"], "password": os.environ["PASSWORD"]}))
')
login_status=$(curl -sS -o /tmp/smoke-login.json -w '%{http_code}' \
    -c "${COOKIE_JAR}" \
    -X POST "${BASE_URL}/api/v1/login" \
    -H "Content-Type: application/json" \
    --data "${login_body}")

[[ "${login_status}" == "200" ]] || { cat /tmp/smoke-login.json; die "login returned ${login_status}"; }

# Confirm the cookie jar has a session cookie.
grep -qE '(^|\s)cartledger_session(\s|$)|auth_token|cartledger' "${COOKIE_JAR}" \
    || { cat "${COOKIE_JAR}"; die "no session cookie set by login"; }
vlog "login status=${login_status}, cookie jar populated"

# -------- 6. GET /api/v1/profile --------
log "GET /api/v1/profile"
profile_status=$(curl -sS -o /tmp/smoke-profile.json -w '%{http_code}' \
    -b "${COOKIE_JAR}" \
    "${BASE_URL}/api/v1/profile")
[[ "${profile_status}" == "200" ]] || { cat /tmp/smoke-profile.json; die "profile returned ${profile_status}"; }
vlog "profile status=${profile_status}"

# -------- 7. upload a receipt (mock LLM fills line items) --------
# Mint a minimal valid 1x1 PNG so we exercise the upload → scan → worker →
# line-items pipeline. The mock LLM client ignores pixel data and returns a
# canned extraction (see internal/llm/testdata/sample-receipt.json) — we just
# need *some* valid image bytes to clear the content-type + decode checks.
SAMPLE_PNG="$(mktemp -t cartledger-smoke-sample.XXXXXX).png"
python3 - "${SAMPLE_PNG}" <<'PY'
import base64, sys
# 1x1 transparent PNG — smallest legal encoding.
data = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
)
with open(sys.argv[1], "wb") as f:
    f.write(data)
PY

log "POST /api/v1/receipts/scan"
scan_status=$(curl -sS -o /tmp/smoke-scan.json -w '%{http_code}' \
    -b "${COOKIE_JAR}" \
    -X POST "${BASE_URL}/api/v1/receipts/scan" \
    -F "images=@${SAMPLE_PNG};type=image/png")
[[ "${scan_status}" == "202" || "${scan_status}" == "200" ]] \
    || { cat /tmp/smoke-scan.json; die "receipt scan returned ${scan_status}"; }
RECEIPT_ID=$(python3 -c '
import json, sys
with open("/tmp/smoke-scan.json") as f:
    print(json.load(f).get("id",""))
')
[[ -n "${RECEIPT_ID}" ]] || { cat /tmp/smoke-scan.json; die "scan response missing id"; }
vlog "receipt id=${RECEIPT_ID}"

# Poll until the worker finishes. Mock LLM is fast (~ms) but the worker still
# goes through preprocess + matcher, so give it a generous deadline for
# loaded CI runners.
log "polling receipt status"
final_status=""
final_items=0
deadline=$(( $(date +%s) + 30 ))
while (( $(date +%s) < deadline )); do
    curl -fsS -o /tmp/smoke-receipt.json \
        -b "${COOKIE_JAR}" \
        "${BASE_URL}/api/v1/receipts/${RECEIPT_ID}" || true
    final_status=$(python3 -c '
import json
try:
    print(json.load(open("/tmp/smoke-receipt.json")).get("status",""))
except Exception:
    print("")
')
    final_items=$(python3 -c '
import json
try:
    li = json.load(open("/tmp/smoke-receipt.json")).get("line_items") or []
    print(len(li))
except Exception:
    print("0")
')
    vlog "poll: status=${final_status} items=${final_items}"
    if [[ "${final_status}" != "pending" && "${final_status}" != "processing" && -n "${final_status}" ]]; then
        break
    fi
    sleep 0.5
done
[[ "${final_status}" != "pending" && "${final_status}" != "processing" && -n "${final_status}" ]] \
    || { cat /tmp/smoke-receipt.json; die "worker did not finish within 30s (status=${final_status})"; }
(( final_items > 0 )) || { cat /tmp/smoke-receipt.json; die "worker finished but no line items extracted"; }
vlog "worker done: status=${final_status} line_items=${final_items}"
EXPECTED_ITEMS="${final_items}"

# -------- 8. stop server for backup --------
log "stopping server"
stop_server

# -------- 9. backup via CLI --------
# The `backup` subcommand takes `--out <path>`, not a positional arg. The
# archive is written outside both DATA_DIRs so the restore target stays empty.
log "backup → ${BACKUP_FILE}"
DATA_DIR="${DATA_DIR_ORIG}" "${BIN}" backup --out "${BACKUP_FILE}" >/dev/null

[[ -f "${BACKUP_FILE}" ]] || die "backup file not created"
backup_size=$(wc -c < "${BACKUP_FILE}" | tr -d ' ')
(( backup_size > 1024 )) || die "backup file too small: ${backup_size} bytes"
vlog "backup size=${backup_size} bytes"

# -------- 10. restore to fresh DATA_DIR --------
log "restore → ${DATA_DIR_RESTORED}"
DATA_DIR="${DATA_DIR_RESTORED}" "${BIN}" restore "${BACKUP_FILE}" >/dev/null

# -------- 11. relaunch against restored DATA_DIR, re-login --------
log "relaunching server against restored DATA_DIR"
launch_server "${DATA_DIR_RESTORED}" "${STDERR_LOG_RESTORED}"

wait_for_livez || {
    cat "${STDERR_LOG_RESTORED}"
    die "/livez did not respond after restore"
}

log "re-login with original credentials"
rm -f "${COOKIE_JAR}" && touch "${COOKIE_JAR}"
relogin_status=$(curl -sS -o /tmp/smoke-relogin.json -w '%{http_code}' \
    -c "${COOKIE_JAR}" \
    -X POST "${BASE_URL}/api/v1/login" \
    -H "Content-Type: application/json" \
    --data "${login_body}")
[[ "${relogin_status}" == "200" ]] || { cat /tmp/smoke-relogin.json; die "re-login after restore returned ${relogin_status}"; }
vlog "relogin status=${relogin_status}"

# -------- 12. verify receipt + line items round-tripped --------
log "GET /api/v1/receipts (post-restore round-trip check)"
receipts_status=$(curl -sS -o /tmp/smoke-receipts.json -w '%{http_code}' \
    -b "${COOKIE_JAR}" \
    "${BASE_URL}/api/v1/receipts")
[[ "${receipts_status}" == "200" ]] || { cat /tmp/smoke-receipts.json; die "receipts list returned ${receipts_status}"; }

# Assert: (a) the receipt we uploaded is present by ID, and (b) its
# persisted item_count matches what the worker extracted pre-backup.
python3 - "${RECEIPT_ID}" "${EXPECTED_ITEMS}" <<'PY'
import json, sys
wanted_id, wanted_items = sys.argv[1], int(sys.argv[2])
with open("/tmp/smoke-receipts.json") as f:
    rows = json.load(f)
if not isinstance(rows, list) or not rows:
    print(f"FAIL: receipts list empty or not an array: {rows!r}", file=sys.stderr)
    sys.exit(1)
match = next((r for r in rows if r.get("id") == wanted_id), None)
if match is None:
    ids = [r.get("id") for r in rows]
    print(f"FAIL: receipt {wanted_id!r} missing after restore; got ids={ids}", file=sys.stderr)
    sys.exit(1)
got_items = int(match.get("item_count") or 0)
if got_items != wanted_items:
    print(f"FAIL: item_count={got_items} after restore, expected {wanted_items}", file=sys.stderr)
    sys.exit(1)
print(f"OK: receipt {wanted_id[:8]}... round-tripped with {got_items} line items")
PY

log "stopping server"
stop_server

# Scrub the sample PNG too.
rm -f "${SAMPLE_PNG}" 2>/dev/null || true

log "all checks passed"
