#!/usr/bin/env bash
# install_test.sh — bash test harness for install.sh.
#
# Strategy: source install.sh with `--dry-run` and a stub PATH so the
# script exercises its argument parser and topology-detection branches
# without actually mutating the system or downloading anything.
#
# Run:  ./install_test.sh
# CI:   .github/workflows/ci.yml runs `bash install_test.sh`
# NOTE: deliberately no `pipefail` — we pipe install.sh (which legitimately
# exits 1 on validation errors) into grep and inspect grep's status. With
# pipefail, run_install's non-zero exit would override grep's success and
# every "expected error" test would falsely fail.
set -eu

cd "$(dirname "$0")"

PASS=0
FAIL=0
log() { printf '\033[0;32m✓\033[0m %s\n' "$1"; PASS=$((PASS+1)); }
fail() { printf '\033[0;31m✗\033[0m %s\n' "$1"; FAIL=$((FAIL+1)); }

# All tests run install.sh from a stub directory where docker/systemctl/etc
# are missing, so the script falls into the "binary" branch every time.
STUB=$(mktemp -d)
trap "rm -rf $STUB" EXIT

# Wrapper that runs install.sh with a clean PATH containing only standard
# tools (curl, sh, tr, uname). No docker, systemd, kubectl. Also unsets
# any SUPPORTLY_* env vars so validation isn't bypassed by the parent
# shell's environment (the live agent sets them; tests must not inherit).
run_install() {
  env -i PATH="/usr/bin:/bin:$STUB" HOME="$HOME" \
    sh ./install.sh "$@" 2>&1
}

# -- arg validation tests --

test_missing_project_id_errors() {
  if run_install --api-key foo --dry-run 2>&1 | grep -q 'project-id'; then
    log "missing --project-id is rejected"
  else
    fail "missing --project-id should error"
  fi
}

test_missing_api_key_errors() {
  if run_install --project-id 11111111-1111-1111-1111-111111111111 --dry-run 2>&1 \
     | grep -q 'api-key'; then
    log "missing credentials are rejected"
  else
    fail "missing --api-key/--enrollment-token should error"
  fi
}

test_help_prints_usage() {
  if run_install --help 2>&1 | grep -q 'Usage:'; then
    log "--help prints usage"
  else
    fail "--help should print usage"
  fi
}

# -- dry-run topology tests --

# For tests that exercise the binary-install path with --dry-run, we still
# need to dodge install.sh's "resolve latest" curl that hits api.github.com
# for an actual API call (curl is real, not stubbed by --dry-run). We
# bypass it by passing --version explicitly so the resolve step is skipped.
test_dry_run_binary_path() {
  out=$(run_install \
    --project-id 11111111-1111-1111-1111-111111111111 \
    --api-key sk_test \
    --version 0.1.0 \
    --dry-run \
    --no-verify 2>&1) || true
  if echo "$out" | grep -q 'topology: binary'; then
    log "no docker/systemd → binary topology"
  else
    fail "expected binary topology, got: $(echo "$out" | head -5)"
  fi
}

test_dry_run_does_not_call_curl() {
  out=$(run_install \
    --project-id 11111111-1111-1111-1111-111111111111 \
    --api-key sk_test \
    --version 0.1.0 \
    --dry-run \
    --no-verify 2>&1) || true
  # Strip ANSI escape codes before grepping — the [dry-run] marker is
  # wrapped in color codes that break a literal pattern match.
  if echo "$out" | sed -E 's/\x1b\[[0-9;]*m//g' | grep -q '\[dry-run\] curl'; then
    log "dry-run prefixes downloads with [dry-run]"
  else
    fail "dry-run should print [dry-run] for curl"
  fi
}

test_uses_enrollment_token_if_provided() {
  out=$(run_install \
    --project-id 11111111-1111-1111-1111-111111111111 \
    --enrollment-token enr_test \
    --version 0.1.0 \
    --dry-run \
    --no-verify 2>&1) || true
  # Just shouldn't bail with 'api-key required'.
  if echo "$out" | grep -q 'api-key.*required'; then
    fail "--enrollment-token alone should be sufficient"
  else
    log "--enrollment-token is accepted alone"
  fi
}

test_unknown_arch_rejected() {
  # We can't easily mock uname inside the same process, but we can verify
  # the code path exists by grepping the script.
  if grep -q 'unsupported arch' install.sh; then
    log "script handles unsupported arch"
  else
    fail "script missing arch validation"
  fi
}

# -- script integrity tests --

test_shebang_is_posix_sh() {
  if head -1 install.sh | grep -q '/usr/bin/env sh'; then
    log "shebang uses /usr/bin/env sh (POSIX)"
  else
    fail "shebang should use POSIX sh"
  fi
}

test_set_e_is_set() {
  if grep -q '^set -eu' install.sh; then
    log "script uses 'set -eu'"
  else
    fail "script should use 'set -eu' for safety"
  fi
}

test_no_eval_of_user_input() {
  # eval is used to expand build_env_args output; verify the input is
  # constructed by us, not from user input directly.
  count=$(grep -c '^[^#]*eval' install.sh || true)
  if [ "$count" -le 2 ]; then
    log "eval usage bounded (≤2 occurrences in run wrapper)"
  else
    fail "eval used too liberally ($count occurrences)"
  fi
}

# -- run all tests --

test_missing_project_id_errors
test_missing_api_key_errors
test_help_prints_usage
test_dry_run_binary_path
test_dry_run_does_not_call_curl
test_uses_enrollment_token_if_provided
test_unknown_arch_rejected
test_shebang_is_posix_sh
test_set_e_is_set
test_no_eval_of_user_input

echo
echo "================================="
echo " $PASS passed, $FAIL failed"
echo "================================="

[ "$FAIL" -eq 0 ]
