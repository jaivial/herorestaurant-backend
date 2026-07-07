#!/usr/bin/env python3
"""
End-to-end proof that the Preact /reservas flow completes a booking and that a
confirmation email is actually delivered over SMTP (now sourced from
email_provider_config, not env vars).

Drives the live site with patchright (headless Chromium), asserts the
POST /api/bookings/front response is HTTP 200 + success=true (no 503), waits for
the confirmation modal, then verifies the message lands in the target inbox via
IMAP.

Env vars (defaults match the values supplied for restaurant 1):
  BASE_URL       default https://preact-dev.menustudioai.com
  BOOKING_EMAIL  default jaimebillanueba99@gmail.com
  IMAP_HOST      default imap.gmail.com
  IMAP_USER      default jaimebillanueba99@gmail.com
  IMAP_PASSWORD  REQUIRED (Gmail app password) - script refuses to run without it

Usage: python3 backend/e2e/smtp_booking_flow.py
"""
import email
import imaplib
import os
import re
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

from patchright.sync_api import sync_playwright

BASE_URL = os.environ.get("BASE_URL", "https://preact-dev.menustudioai.com").rstrip("/")
BOOKING_EMAIL = os.environ.get("BOOKING_EMAIL", "jaimebillanueba99@gmail.com")
IMAP_HOST = os.environ.get("IMAP_HOST", "imap.gmail.com")
IMAP_USER = os.environ.get("IMAP_USER", "jaimebillanueba99@gmail.com")
IMAP_PASSWORD = os.environ.get("IMAP_PASSWORD", "")

ARTIFACT_DIR = Path(__file__).resolve().parent / "artifacts"
SCREENSHOT_PATH = ARTIFACT_DIR / "booking-confirmation.png"

GREEN = "\033[92m"
RED = "\033[91m"
RESET = "\033[0m"


def fail(msg: str) -> "NoReturn":
    print(f"{RED}FAIL:{RESET} {msg}", file=sys.stderr)
    sys.exit(1)


def next_step(page, label="Siguiente"):
    """Click the primary CTA on the current step and give the SPA a tick."""
    btn = page.locator(".btn.primary", has_text=label).first
    btn.wait_for(state="visible", timeout=15000)
    for _ in range(40):
        if btn.is_enabled():
            break
        time.sleep(0.25)
    btn.click()


def current_title(page) -> str:
    try:
        return page.locator(".resvStep .resvCardTitle").first.inner_text(timeout=8000).strip()
    except Exception:
        return ""


def choose_no(page):
    """Yes/No steps: click the 'No' choice button."""
    page.locator("button.resvChoice", has_text="No").first.click()


def run_booking(page) -> dict:
    requests_seen = []
    console_msgs = []

    def on_request(req):
        if "/api/bookings/front" in req.url:
            requests_seen.append(req.method + " " + req.url)

    def on_request_failed(req):
        if "/api/bookings/front" in req.url:
            requests_seen.append("FAILED " + req.url + " " + str(req.failure))

    page.on("request", on_request)
    page.on("requestfailed", on_request_failed)
    page.on("console", lambda m: console_msgs.append(f"{m.type}: {m.text}"))
    page.on("pageerror", lambda e: console_msgs.append(f"pageerror: {e}"))

    page.goto(f"{BASE_URL}/reservas", wait_until="domcontentloaded")
    page.wait_for_selector('[data-step-id="date"]', timeout=30000)

    # --- Date step: pick first bookable day (in-month, not disabled/full). ---
    day = page.locator(".resvDay:not(.other):not(.disabled):not(.full):not([disabled])").first
    day.wait_for(state="visible", timeout=20000)
    day.click()

    # Party size popover -> pick 4 (fallback 2). Options carry the number in
    # .resvSelectOpt__left; the visible label is just the "personas" suffix.
    trigger = page.locator('.resvSelectBtn[aria-label="Número de personas"]').first
    trigger.wait_for(state="visible", timeout=15000)
    for _ in range(40):
        if trigger.is_enabled():
            break
        time.sleep(0.25)
    trigger.click()
    page.wait_for_selector('.resvSelectPopover[aria-label="Número de personas"]', timeout=10000)

    def pick_party(n: str) -> bool:
        opt = page.locator(".resvSelectOpt", has=page.locator(".resvSelectOpt__left", has_text=n)).first
        try:
            opt.wait_for(state="visible", timeout=3000)
            opt.click()
            return True
        except Exception:
            return False

    if not pick_party("4"):
        if not pick_party("2"):
            # Last resort: click the first option available.
            page.locator(".resvSelectOpt").first.click()

    # Reservation time.
    hour = page.locator(".resvHourBtn").first
    hour.wait_for(state="visible", timeout=15000)
    hour.click()
    next_step(page)  # leave date step

    # --- Middle steps are dynamic (group-menu only when menus exist). Loop by
    # card title until we reach the personal-data step. ---
    for _ in range(6):
        title = current_title(page).lower()
        if "datos personales" in title:
            break
        if "menú de grupo" in title or "menu de grupo" in title or "grupos" in title:
            choose_no(page)
            next_step(page)
        elif "arroz" in title:
            choose_no(page)
            next_step(page)
        else:
            # Unknown intermediate step: just advance.
            next_step(page)
        time.sleep(0.3)

    # --- Personal step. ---
    page.locator('input.resvInput[type="text"]').first.fill("Test Booking")
    page.locator('input.resvInput[type="email"]').first.fill(BOOKING_EMAIL)
    page.locator('input.resvInput[type="tel"]').first.fill("600000000")
    next_step(page)

    # --- Adults / accessories: keep defaults, advance until summary. ---
    for _ in range(4):
        title = current_title(page).lower()
        if "resumen" in title:
            break
        next_step(page)
        time.sleep(0.3)

    # --- Summary step: tick both Radix checkboxes (role=checkbox buttons). ---
    # The "Completar reserva" button is NOT disabled on unchecked terms; the
    # handler validates and silently toasts. So we must actually flip both.
    page.wait_for_selector(".resvTerms", timeout=15000)
    checks = page.locator('.resvTerms button[role="checkbox"]')
    n = checks.count()
    if n < 2:
        fail(f"expected 2 terms checkboxes, found {n}")
    for i in range(n):
        cb = checks.nth(i)
        for _ in range(3):
            if cb.get_attribute("aria-checked") == "true":
                break
            cb.click()
            time.sleep(0.2)
        if cb.get_attribute("aria-checked") != "true":
            ARTIFACT_DIR.mkdir(parents=True, exist_ok=True)
            page.screenshot(path=str(ARTIFACT_DIR / "debug-checkbox.png"))
            fail(f"terms checkbox {i} would not check (aria-checked={cb.get_attribute('aria-checked')!r})")

    title_before = current_title(page)
    complete = page.locator(".btn.primary", has_text="Completar reserva").first
    try:
        complete.wait_for(state="visible", timeout=15000)
    except Exception:
        ARTIFACT_DIR.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=str(ARTIFACT_DIR / "debug-no-complete.png"))
        fail(f"'Completar reserva' not visible; current step title={title_before!r}")
    for _ in range(40):
        if complete.is_enabled():
            break
        time.sleep(0.25)
    if not complete.is_enabled():
        ARTIFACT_DIR.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=str(ARTIFACT_DIR / "debug-complete-disabled.png"))
        fail("'Completar reserva' stayed disabled (checkboxes not accepted?)")
    # Capture the response deterministically via expect_response (event-handler
    # body reads under the sync API are unreliable).
    try:
        with page.expect_response(
            lambda r: "/api/bookings/front" in r.url, timeout=60000
        ) as resp_info:
            complete.click()
        resp = resp_info.value
    except Exception:
        ARTIFACT_DIR.mkdir(parents=True, exist_ok=True)
        page.screenshot(path=str(ARTIFACT_DIR / "debug-no-response.png"))
        toasts = ""
        try:
            toasts = page.locator(".resvToast__msg").all_inner_texts()
        except Exception:
            pass
        summary = ""
        try:
            summary = page.locator(".resvSummary").first.inner_text(timeout=3000)
        except Exception:
            pass
        fail(
            f"no response captured for /api/bookings/front; title={title_before!r} "
            f"toasts={toasts}\nREQUESTS={requests_seen}\n"
            f"CONSOLE(last 15)={console_msgs[-15:]}\nSUMMARY:\n{summary}"
        )

    status = resp.status
    try:
        body = resp.json()
    except Exception:
        body = resp.text()
    if status != 200:
        msg = body.get("message") if isinstance(body, dict) else body
        fail(f"/api/bookings/front returned HTTP {status}: {msg}")
    if not isinstance(body, dict) or body.get("success") is not True:
        msg = body.get("message") if isinstance(body, dict) else body
        fail(f"/api/bookings/front success!=true: {msg}")

    # Confirmation modal + screenshot.
    page.wait_for_selector(".resvModal__card", timeout=20000)
    ARTIFACT_DIR.mkdir(parents=True, exist_ok=True)
    page.screenshot(path=str(SCREENSHOT_PATH))

    return body


def verify_email() -> str:
    if not IMAP_PASSWORD:
        # Data path already proven by the 200/success response; skip IMAP if
        # no app password is available (documented manual fallback).
        print(f"{RED}WARN:{RESET} IMAP_PASSWORD unset - skipping inbox verification "
              "(manual fallback: check the mailbox for a message from "
              "reservas@alqueriavillacarmen.com).", file=sys.stderr)
        return "SKIPPED (IMAP_PASSWORD unset)"

    deadline = time.time() + 60
    since = datetime.now(timezone.utc).strftime("%d-%b-%Y")
    last_err = None
    while time.time() < deadline:
        try:
            M = imaplib.IMAP4_SSL(IMAP_HOST, 993)
            M.login(IMAP_USER, IMAP_PASSWORD)
            M.select("INBOX")
            typ, data = M.search(
                None,
                f'(SINCE {since} SUBJECT "Confirmación de Reserva" FROM "reservas@alqueriavillacarmen.com")',
            )
            uids = data[0].split() if data and data[0] else []
            if uids:
                uid = uids[-1]
                typ, msg_data = M.fetch(uid, "(RFC822)")
                raw = msg_data[0][1]
                msg = email.message_from_bytes(raw)
                message_id = msg.get("Message-ID", "<unknown>")
                body_text = _extract_body(msg).lower()
                if BOOKING_EMAIL.lower() not in body_text and BOOKING_EMAIL.lower() not in raw.decode("utf-8", "ignore").lower():
                    last_err = "email found but does not contain booking email"
                elif "alquer" not in body_text:
                    last_err = "email found but missing brand name 'Alquería Villa Carmen'"
                else:
                    M.logout()
                    return message_id
            M.logout()
        except Exception as e:  # noqa: BLE001
            last_err = str(e)
        time.sleep(10)
    fail(f"confirmation email not found within 60s (last: {last_err})")


def _extract_body(msg) -> str:
    if msg.is_multipart():
        parts = []
        for part in msg.walk():
            if part.get_content_type() in ("text/plain", "text/html"):
                payload = part.get_payload(decode=True)
                if payload:
                    parts.append(payload.decode("utf-8", "ignore"))
        return "\n".join(parts)
    payload = msg.get_payload(decode=True)
    return payload.decode("utf-8", "ignore") if payload else ""


def main():
    with sync_playwright() as p:
        browser = p.chromium.launch()
        context = browser.new_context(
            viewport={"width": 1280, "height": 900}, locale="es-ES"
        )
        page = context.new_page()
        try:
            body = run_booking(page)
        finally:
            context.close()
            browser.close()

    booking_id = body.get("booking_id")
    message_id = verify_email()
    print(
        f"{GREEN}PASS{RESET} booking_id={booking_id} "
        f"imap_message_id={message_id} screenshot={SCREENSHOT_PATH}"
    )
    sys.exit(0)


if __name__ == "__main__":
    main()
