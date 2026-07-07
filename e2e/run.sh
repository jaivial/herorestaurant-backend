#!/usr/bin/env bash
# Runs the SMTP booking E2E. Loads IMAP_PASSWORD from backend/.env if present;
# everything else has safe defaults inside the Python script.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${HERE}/../.env"

if [[ -z "${IMAP_PASSWORD:-}" && -f "${ENV_FILE}" ]]; then
  # Extract only IMAP_PASSWORD; do not source the whole file.
  line="$(grep -E '^IMAP_PASSWORD=' "${ENV_FILE}" || true)"
  if [[ -n "${line}" ]]; then
    export IMAP_PASSWORD="${line#IMAP_PASSWORD=}"
  fi
fi

exec python3 "${HERE}/smtp_booking_flow.py"
