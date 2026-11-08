#!/usr/bin/env python3
"""Offline native-protocol fixture. Never contacts an authorization service."""
import json
import os
from pathlib import Path
import sys

agent = "codex" if "app-server" in sys.argv else "claude"
home = Path(os.environ["CODEX_HOME" if agent == "codex" else "CLAUDE_CONFIG_DIR"])
signed = home / "fixture-signed-in"
mode = os.environ.get("LOGIN_FIXTURE_MODE", "success")
email = home.name + "@example.invalid"


def emit(value):
    print(json.dumps(value), flush=True)


if "status" in sys.argv:
    emit({"loggedIn": signed.exists(), "authMethod": "claude.ai", "email": email,
          "subscriptionType": "pro"})
    sys.exit(0 if signed.exists() else 1)

wait_id = None
for line in sys.stdin:
    r = json.loads(line)
    if agent == "codex":
        method = r["method"]
        result = {}
        if method == "initialized":
            continue
        if method == "account/read":
            result = {"account": {"type": "chatgpt", "email": email, "planType": "plus"}
                      if signed.exists() else None}
        elif method == "account/login/start":
            assert r["params"]["type"] == "chatgptDeviceCode"
            if mode == "failed-start":
                emit({"id": r["id"], "error": {"code": -1, "message": "secret-token-not-for-ui"}})
                continue
            result = {"type": "chatgptDeviceCode", "loginId": "fixture-login",
                      "verificationUrl": "https://auth.openai.com/codex/device", "userCode": "ABCD-1234"}
            emit({"method": "account/login/completed", "params": {"loginId": "other-login", "success": True}})
        elif method == "test/complete":
            signed.write_text("fixture", encoding="utf-8")
            emit({"method": "account/login/completed", "params": {"loginId": "fixture-login", "success": True}})
        elif method == "account/login/cancel":
            (home / "fixture-cancelled").write_text("cancelled", encoding="utf-8")
        emit({"id": r["id"], "result": result})
    else:
        req = r["request"]
        method = req["subtype"]
        result = {}
        if method == "claude_authenticate":
            result = {"manualUrl": "https://claude.ai/oauth/authorize?code=true&state=fixture-state&code_challenge=native-pkce"}
        elif method == "claude_oauth_wait_for_completion":
            wait_id = r["request_id"]
            continue
        elif method == "claude_oauth_callback":
            assert req["state"] == "fixture-state"
            assert req["authorizationCode"] == "fixture-code"
            signed.write_text("fixture", encoding="utf-8")
            if wait_id:
                emit({"type": "control_response", "response": {"request_id": wait_id,
                      "subtype": "success" if mode != "failed-code" else "error",
                      "error": "secret-token-not-for-ui", "response": {}}})
        emit({"type": "control_response", "response": {"request_id": r["request_id"], "subtype": "success", "response": result}})
