#!/usr/bin/env bash
# End-to-end test: builds the real binary, runs it against a fake API and
# checks every feature from the outside, like a user would.
#
#   ./scripts/e2e/run.sh
#
# Needs: go, curl, python3. Takes about 30 seconds.
# Exit code is 0 when every check passes.
set -u

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
WORK=$(mktemp -d)
BIN=$WORK/local-scheduler

PORT=${E2E_PORT:-18090}
FAKE_PORT=${E2E_FAKE_PORT:-15001}
API=http://localhost:$PORT
FAKE=http://127.0.0.1:$FAKE_PORT
export DB_PATH=$WORK/scheduler.db PORT ALLOW_SHELL_JOBS=true

PASS=0
FAIL=0
SRV=
FAKE_PID=

# ---------------------------------------------------------------- helpers

check() { # check "description" command args...  (passes if the command succeeds)
	local name=$1
	shift
	if "$@" >/dev/null 2>&1; then
		echo "PASS  $name"
		PASS=$((PASS + 1))
	else
		echo "FAIL  $name"
		FAIL=$((FAIL + 1))
	fi
}

eq() { [ "$1" = "$2" ]; }
status() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

# json '<python expression on d>' reads JSON from stdin, e.g. json 'd["id"]'
json() { python3 -c "import json, sys; d = json.load(sys.stdin); print($1)"; }

# hits /path: how many times the fake API received this path
hits() { grep -c "\"path\": \"$1\"" "$WORK/fake.log" 2>/dev/null || echo 0; }

create() { curl -s -X POST "$API/jobs" -d "$1" | json 'd["id"]'; }

start_server() {
	"$BIN" >>"$WORK/server.log" 2>&1 &
	SRV=$!
	for _ in $(seq 50); do
		curl -s "$API/health" >/dev/null && return
		sleep 0.1
	done
	echo "server did not start, log:"
	cat "$WORK/server.log"
	exit 1
}

stop_server() { kill "$1" "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null; }

cleanup() {
	kill "$SRV" "$FAKE_PID" 2>/dev/null
	wait 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

# ---------------------------------------------------------------- setup

echo "building..."
(cd "$ROOT" && go build -o "$BIN" ./cmd/local-scheduler) || exit 1
python3 "$HERE/fake_api.py" "$WORK/fake.log" "$FAKE_PORT" &
FAKE_PID=$!
start_server

# ---------------------------------------------------------------- checks

echo "--- basics"
check "GET /health → 200" eq "$(status "$API/health")" 200
check "GET /openapi.yaml → 200" eq "$(status "$API/openapi.yaml")" 200
check "GET /jobs on an empty database → []" eq "$(curl -s "$API/jobs")" "[]"

echo "--- bad requests"
check "invalid JSON → 400" eq "$(status -X POST "$API/jobs" -d 'nope')" 400
check "unknown field → 400" eq "$(status -X POST "$API/jobs" -d '{"name":"x","shedule":"* * * * *"}')" 400
check "no schedule and no runAt → 400" eq "$(status -X POST "$API/jobs" -d '{"name":"x","type":"http","http":{"url":"http://a.b"}}')" 400
check "bad cron → 400" eq "$(status -X POST "$API/jobs" -d '{"name":"x","schedule":"every day","type":"http","http":{"url":"http://a.b"}}')" 400
check "bad timezone → 400" eq "$(status -X POST "$API/jobs" -d '{"name":"x","schedule":"@daily","timezone":"Mars/X","type":"http","http":{"url":"http://a.b"}}')" 400
check "relative URL → 400" eq "$(status -X POST "$API/jobs" -d '{"name":"x","schedule":"@daily","type":"http","http":{"url":"/x"}}')" 400
check "missing job → 404" eq "$(status "$API/jobs/nope")" 404
check "runs of a missing job → 404" eq "$(status "$API/jobs/nope/runs")" 404

echo "--- create jobs"
RUN_AT=$(python3 -c 'import datetime as d; print((d.datetime.now(d.timezone.utc) + d.timedelta(seconds=3)).strftime("%Y-%m-%dT%H:%M:%SZ"))')
ONCE=$(create "{\"name\":\"once\",\"runAt\":\"$RUN_AT\",\"type\":\"http\",\"http\":{\"url\":\"$FAKE/once\",\"method\":\"POST\",\"headers\":{\"X-Token\":\"secret\"},\"body\":\"{\\\"db\\\":\\\"main\\\"}\"}}")
EVERY=$(create "{\"name\":\"every-2s\",\"schedule\":\"@every 2s\",\"type\":\"http\",\"http\":{\"url\":\"$FAKE/every\"}}")
SHELL_JOB=$(create '{"name":"shell","schedule":"@every 2s","type":"shell","shell":{"command":"echo hello from shell"}}')
FAILS=$(create "{\"name\":\"fails\",\"schedule\":\"@every 2s\",\"type\":\"http\",\"http\":{\"url\":\"$FAKE/fail\"}}")
SLOW=$(create "{\"name\":\"slow\",\"schedule\":\"@every 1h\",\"timeoutSeconds\":1,\"type\":\"http\",\"http\":{\"url\":\"$FAKE/slow\"}}")
DHAKA=$(curl -s -X POST "$API/jobs" -d '{"name":"dhaka","schedule":"0 2 * * *","timezone":"Asia/Dhaka","type":"http","http":{"url":"http://a.b"}}')
check "6 jobs created" eq "$(curl -s "$API/jobs" | json 'len(d)')" 6
check "02:00 in Dhaka is stored as 20:00 UTC" eq "$(echo "$DHAKA" | json 'd["nextRunAt"][11:16]')" "20:00"
check "defaults filled in (GET, 30s, UTC)" eq "$(curl -s "$API/jobs/$EVERY" | json 'd["http"]["method"], d["timeoutSeconds"], d["timezone"]')" "GET 30 UTC"

echo "--- run now"
check "run now → 202" eq "$(status -X POST "$API/jobs/$SLOW/run")" 202
check "run now while running → 409" eq "$(status -X POST "$API/jobs/$SLOW/run")" 409

echo "--- let the jobs run (7 s)"
sleep 7
check "one-time job called exactly once" eq "$(hits /once)" 1
check "one-time job sent method, header and body" eq "$(grep '/once' "$WORK/fake.log" | json 'd["method"], d["token"], d["body"]')" 'POST secret {"db":"main"}'
check "one-time job has no next run" eq "$(curl -s "$API/jobs/$ONCE" | json 'd.get("nextRunAt")')" None
check "one-time job succeeded" eq "$(curl -s "$API/jobs/$ONCE" | json 'd["lastRun"]["status"]')" success
check "repeating job ran 3+ times" test "$(hits /every)" -ge 3
check "shell output saved" eq "$(curl -s "$API/jobs/$SHELL_JOB" | json 'd["lastRun"]["output"].strip()')" "hello from shell"
check "500 response → failed" eq "$(curl -s "$API/jobs/$FAILS" | json 'd["lastRun"]["status"]')" failed
check "slow API → timed out after 1s" eq "$(curl -s "$API/jobs/$SLOW" | json 'd["lastRun"]["error"]')" "timed out after 1s"
check "run history is newest first" eq "$(curl -s "$API/jobs/$EVERY/runs?limit=2" | json 'd[0]["id"] > d[1]["id"]')" True
check "runs ?limit=0 → 400" eq "$(status "$API/jobs/$EVERY/runs?limit=0")" 400

echo "--- pause and resume"
check "pause → 200" eq "$(status -X POST "$API/jobs/$EVERY/pause")" 200
sleep 0.5
BEFORE=$(hits /every)
sleep 3
check "paused job does not run" eq "$(hits /every)" "$BEFORE"
check "resume → 200" eq "$(status -X POST "$API/jobs/$EVERY/resume")" 200
sleep 3
check "resumed job runs again" test "$(hits /every)" -gt "$BEFORE"

echo "--- delete"
check "delete → 204" eq "$(status -X DELETE "$API/jobs/$FAILS")" 204
check "deleted job → 404" eq "$(status "$API/jobs/$FAILS")" 404

echo "--- restart keeps data"
COUNT=$(curl -s "$API/jobs" | json 'len(d)')
stop_server -TERM
start_server
check "same $COUNT jobs after restart" eq "$(curl -s "$API/jobs" | json 'len(d)')" "$COUNT"
check "run history kept after restart" test "$(curl -s "$API/jobs/$EVERY/runs?limit=100" | json 'len(d)')" -ge 3

echo "--- graceful shutdown waits for a running job"
LONG=$(create '{"name":"long","schedule":"@every 1h","type":"shell","shell":{"command":"sleep 2 && echo finished"}}')
curl -s -X POST "$API/jobs/$LONG/run" >/dev/null
sleep 0.3
T0=$(python3 -c 'import time; print(time.time())')
stop_server -TERM
T1=$(python3 -c 'import time; print(time.time())')
check "shutdown waited for the job" python3 -c "import sys; sys.exit($T1 - $T0 < 1.5)"
start_server
check "job finished during shutdown" eq "$(curl -s "$API/jobs/$LONG" | json 'd["lastRun"]["output"].strip()')" finished

echo "--- crash (kill -9 during a run)"
curl -s -X POST "$API/jobs/$LONG/run" >/dev/null
sleep 0.3
stop_server -KILL
start_server
check "interrupted run marked failed" eq "$(curl -s "$API/jobs/$LONG/runs?limit=1" | json 'd[0]["status"]')" failed
check "job can run again" eq "$(curl -s "$API/jobs/$LONG" | json 'd["running"]')" False
check "recovery logged a warning" grep -q "interrupted by the last shutdown" "$WORK/server.log"

echo "--- big output does not use much memory"
BIG=$(create '{"name":"big","schedule":"@every 1h","type":"shell","shell":{"command":"head -c 200000000 /dev/zero | tr \"\\0\" a"}}')
curl -s -X POST "$API/jobs/$BIG/run" >/dev/null
PEAK=0
for _ in $(seq 40); do
	R=$(ps -o rss= -p "$SRV" | tr -d ' ')
	[ "${R:-0}" -gt "$PEAK" ] && PEAK=$R
	sleep 0.1
done
check "200 MB of output: server stays under 100 MB ($((PEAK / 1024)) MB)" test "$PEAK" -lt 102400
check "only the start of the output is kept" eq "$(curl -s "$API/jobs/$BIG" | json 'd["lastRun"]["output"].endswith("(truncated)")')" True

stop_server -TERM
echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
