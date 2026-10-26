#!/bin/bash
# build-tailscaled.sh — build Tailscale's daemon, tailscaled, for the images that carry it: the Shows,
# the Echo Show 8 2nd gen, the Skylight and the Spot (docs/tailscale.md). Linux or WSL, with Go:
#
#   tools/linux/build-tailscaled.sh [-o FILE]
#
# writes bin/tailscaled-arm64 (deploy-rootfs.sh puts it in the image as /usr/local/bin/tailscaled).
#
# arm64: their kernels are 64-bit, also under the Show's 32-bit userspace, and tailscaled is static
# (CGO off), so the one build runs on every one of them (not on an athena still on Amazon's 32-bit
# kernel, where the daemon doesn't offer it). It is built from Tailscale's own release
# source, checked against the SHA-256 below, with what TECHO5 doesn't use left out by Tailscale's own
# ts_omit build tags: no SSH server, no Taildrop, no exit node or subnet routes, no DNS or route
# changes on the device, no web client, no logging to Tailscale. What is left joins the tailnet with an
# auth key and carries Home Assistant's connection; about 13 MB rather than about 35.
#
# Go: the go.mod's version or later (GOTOOLCHAIN=auto fetches it, checked by Go's checksum database).
#
# Tailscale is BSD-3-Clause (its LICENSE, copied beside the binary as tailscaled-arm64.LICENSE).
#
# Environment:
#   WORK  scratch (default ~/tailscaled-build)
set -euo pipefail
ROOT=${TECHO5_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}
WORK=${WORK:-$HOME/tailscaled-build}
OUT=$ROOT/bin/tailscaled-arm64
TS_VERSION=1.102.4
TS_SHA256=784b023e825e1cca7b146ac6a7aff08b179d60d10839b51019f315dab426c871
TAGS=ts_omit_ace,ts_omit_acme,ts_omit_advertiseexitnode,ts_omit_advertiseroutes,ts_omit_appconnectors,ts_omit_aws,ts_omit_bakedroots,ts_omit_bird,ts_omit_c2n,ts_omit_cachenetmap,ts_omit_captiveportal,ts_omit_capture,ts_omit_cliconndiag,ts_omit_clientmetrics,ts_omit_clientupdate,ts_omit_cloud,ts_omit_colorable,ts_omit_completion,ts_omit_completion_scripts,ts_omit_conn25,ts_omit_dbus,ts_omit_debug,ts_omit_debugeventbus,ts_omit_debugportmapper,ts_omit_desktop_sessions,ts_omit_dns,ts_omit_doctor,ts_omit_drive,ts_omit_flashappliance,ts_omit_gro,ts_omit_health,ts_omit_hujsonconf,ts_omit_identityfederation,ts_omit_iptables,ts_omit_kube,ts_omit_linkspeed,ts_omit_linuxdnsfight,ts_omit_listenrawdisco,ts_omit_logtail,ts_omit_netlog,ts_omit_netstack,ts_omit_networkmanager,ts_omit_oauthkey,ts_omit_outboundproxy,ts_omit_peerapiclient,ts_omit_peerapiserver,ts_omit_portlist,ts_omit_posture,ts_omit_qrcodes,ts_omit_relayserver,ts_omit_remoteconfig,ts_omit_resolved,ts_omit_routecheck,ts_omit_runtimemetrics,ts_omit_sdnotify,ts_omit_serve,ts_omit_serviceclientprefs,ts_omit_ssh,ts_omit_synology,ts_omit_syslog,ts_omit_syspolicy,ts_omit_systray,ts_omit_taildrop,ts_omit_tailnetlock,ts_omit_tap,ts_omit_tpm,ts_omit_tundevstats,ts_omit_useexitnode,ts_omit_useproxy,ts_omit_usermetrics,ts_omit_useroutes,ts_omit_wakeonlan,ts_omit_webbrowser,ts_omit_webclient
while [ $# -gt 0 ]; do
	case "$1" in
	-o) OUT=$2; shift 2;;
	*) echo "unknown argument: $1" >&2; exit 1;;
	esac
done
command -v go >/dev/null || { echo "go is not on PATH" >&2; exit 1; }

mkdir -p "$WORK"
TGZ=$WORK/tailscale-$TS_VERSION.tar.gz
[ -f "$TGZ" ] || { curl -fsSL -A "techo5 build-tailscaled.sh" -o "$TGZ.part" \
	"https://github.com/tailscale/tailscale/archive/refs/tags/v$TS_VERSION.tar.gz" && mv "$TGZ.part" "$TGZ"; }
echo "$TS_SHA256  $TGZ" | sha256sum -c - || { echo "the Tailscale source is not the one this script was written for" >&2; exit 1; }
SRC=$WORK/tailscale-$TS_VERSION
rm -rf "$SRC"; mkdir -p "$SRC"
tar -xzf "$TGZ" -C "$SRC" --strip-components=1

echo "== tailscaled $TS_VERSION, arm64, small"
mkdir -p "$(dirname "$OUT")"
# The version is stamped as Tailscale's own release builds stamp it, so the tailnet's admin page shows
# the version the device runs.
(cd "$SRC" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOFLAGS=-mod=mod go build -trimpath -buildvcs=false \
	-tags "$TAGS" \
	-ldflags "-s -w -X tailscale.com/version.longStamp=$TS_VERSION-techo5 -X tailscale.com/version.shortStamp=$TS_VERSION" \
	-o "$OUT" ./cmd/tailscaled)
cp "$SRC/LICENSE" "$OUT.LICENSE"
echo "built: $OUT $(sha256sum "$OUT" | cut -d' ' -f1)"
