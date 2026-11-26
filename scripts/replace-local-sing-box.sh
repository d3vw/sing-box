#!/usr/bin/env bash
# Replace the system sing-box binary with the freshly built (CI, with_musl, static
# naive/cronet) one and restart the service. Must be run as root (uses systemctl +
# writes to /usr/bin).
set -euo pipefail

NEW_BINARY="/home/grey/d3vw/sing-box/sing-box"
TARGET_BINARY="/usr/bin/sing-box"
SERVICE="sing-box.service"
CONFIG_DIR="/etc/sing-box"
BACKUP="${TARGET_BINARY}.bak.$(date +%Y%m%d%H%M%S)"

# A previous attempt used a with_purego local build and installed a prebuilt
# libcronet.so from a slightly different cronet-go release here; it crashed on
# real naive traffic (ABI mismatch) and is no longer needed now that the CI-built
# binary statically links cronet via with_musl. Remove it so it can't be picked up
# by anything else by mistake.
STALE_CRONET_LIB="/usr/local/lib/libcronet.so"

if [[ $EUID -ne 0 ]]; then
	echo "This script must be run as root (it replaces ${TARGET_BINARY} and restarts ${SERVICE})." >&2
	exit 1
fi

if [[ ! -x "$NEW_BINARY" ]]; then
	echo "Built binary not found or not executable: $NEW_BINARY" >&2
	exit 1
fi

echo "==> New binary version:"
"$NEW_BINARY" version

if [[ -f "$STALE_CRONET_LIB" ]]; then
	echo "==> Removing stale $STALE_CRONET_LIB from the earlier with_purego attempt"
	rm -f "$STALE_CRONET_LIB"
	ldconfig || true
fi

echo "==> Validating config with the new binary before touching anything live"
"$NEW_BINARY" check -D /var/lib/sing-box -C "$CONFIG_DIR"

echo "==> Backing up current binary to $BACKUP"
cp -a "$TARGET_BINARY" "$BACKUP"

echo "==> Installing new binary to $TARGET_BINARY"
install -o root -g root -m 755 "$NEW_BINARY" "$TARGET_BINARY"

echo "==> Restarting $SERVICE"
systemctl restart "$SERVICE"

sleep 2
echo "==> Service status"
systemctl --no-pager status "$SERVICE" || true

if systemctl is-active --quiet "$SERVICE"; then
	echo "==> OK: $SERVICE is active. Rollback if needed:"
	echo "    sudo install -o root -g root -m 755 $BACKUP $TARGET_BINARY && sudo systemctl restart $SERVICE"
else
	echo "==> FAILED: $SERVICE is not active, rolling back automatically" >&2
	install -o root -g root -m 755 "$BACKUP" "$TARGET_BINARY"
	systemctl restart "$SERVICE" || true
	exit 1
fi
