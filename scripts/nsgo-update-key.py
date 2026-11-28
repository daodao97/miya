"""Translate Miya's existing Ed25519 private key to NSGo's base64 seed."""
import base64
import binascii
import os

try:
    key = base64.b64decode(os.environ.get("MIYA_UPDATE_PRIVATE_KEY", "").strip(), validate=True)
except binascii.Error:
    raise SystemExit("Invalid desktop update signing key")
if len(key) != 64:
    raise SystemExit("Expected the existing 64-byte desktop update signing key")
print(base64.b64encode(key[:32]).decode("ascii"))
