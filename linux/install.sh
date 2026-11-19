#!/usr/bin/env bash
# EasyRadius unter Debian/Ubuntu installieren oder aktualisieren – auch im Proxmox-LXC.
#
#   curl -fsSL https://raw.githubusercontent.com/gottschalkfelix4-source/easyradius/main/linux/install.sh | bash
#
# Optionen:
#   --version 1.6.0      bestimmte Version statt der neuesten
#   --file paket.tar.gz  lokales Paket statt Download (z. B. ohne Internet)
#   --uninstall          Dienst und Programm entfernen (Daten bleiben erhalten)
#   --purge              zusammen mit --uninstall: auch alle Daten löschen
#
# Ein erneuter Aufruf aktualisiert eine bestehende Installation; Daten bleiben erhalten.
set -euo pipefail

REPO="gottschalkfelix4-source/easyradius"
BIN="/usr/local/bin/easyradius"
DATA="/var/lib/easyradius"
UNIT="/etc/systemd/system/easyradius.service"
WEB_PORT=8088

if [ -t 1 ]; then B=$'\e[1m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; N=$'\e[0m'; else B=""; G=""; Y=""; R=""; N=""; fi
step() { printf '%s==>%s %s\n' "$B" "$N" "$*"; }
ok()   { printf ' %s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf ' %s!%s %s\n' "$Y" "$N" "$*"; }
die()  { printf '%sFehler:%s %s\n' "$R" "$N" "$*" >&2; exit 1; }

VERSION=""; FILE=""; UNINSTALL=0; PURGE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2#v}"; shift 2 ;;
    --file) FILE="$2"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    --purge) PURGE=1; shift ;;
    -h|--help) sed -n '2,15p' "$0" 2>/dev/null || true; exit 0 ;;
    *) die "Unbekannte Option: $1" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "Bitte als root ausführen (z. B. mit sudo)."
[ -d /run/systemd/system ] || die "systemd wird benötigt."

# ---------------------------------------------------------------- Deinstallation
if [ "$UNINSTALL" -eq 1 ]; then
  step "EasyRadius wird entfernt"
  systemctl disable --now easyradius.service 2>/dev/null || true
  rm -f "$UNIT" "$BIN" /usr/local/bin/easyradius-radtest /usr/local/sbin/easyradius-update
  systemctl daemon-reload
  if [ "$PURGE" -eq 1 ]; then
    rm -rf "$DATA"
    userdel easyradius 2>/dev/null || true
    ok "Programm und Daten entfernt"
  else
    ok "Programm entfernt – Daten liegen weiterhin in $DATA (löschen mit --uninstall --purge)"
  fi
  exit 0
fi

# ---------------------------------------------------------------- Voraussetzungen
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "Nicht unterstützte Architektur: $(uname -m)" ;;
esac

need=()
for c in curl tar sha256sum; do command -v "$c" >/dev/null 2>&1 || need+=("$c"); done
if [ ${#need[@]} -gt 0 ]; then
  step "Installiere benötigte Pakete"
  command -v apt-get >/dev/null 2>&1 || die "Bitte curl, tar und coreutils installieren."
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq curl ca-certificates tar coreutils >/dev/null
  ok "Pakete installiert"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# ---------------------------------------------------------------- Paket holen
if [ -n "$FILE" ]; then
  [ -f "$FILE" ] || die "Datei nicht gefunden: $FILE"
  cp "$FILE" "$TMP/paket.tar.gz"
  ok "Lokales Paket: $FILE"
else
  if [ -z "$VERSION" ]; then
    step "Suche die neueste Version"
    # Die Weiterleitung von …/releases/latest zeigt auf …/tag/vX.Y.Z (ohne API-Limit, ohne Pipe).
    LATEST="$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")" || LATEST=""
    VERSION="${LATEST##*/}"
    VERSION="${VERSION#v}"
    [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || VERSION=""
    [ -n "$VERSION" ] || die "Konnte die neueste Version nicht ermitteln (Internetzugang?)."
  fi
  NAME="easyradius-$VERSION-linux-$ARCH.tar.gz"
  URL="https://github.com/$REPO/releases/download/v$VERSION"
  step "Lade EasyRadius $VERSION ($ARCH)"
  curl -fsSL -o "$TMP/$NAME" "$URL/$NAME" || die "Download fehlgeschlagen: $URL/$NAME"
  curl -fsSL -o "$TMP/SHA256SUMS.txt" "$URL/SHA256SUMS.txt" || die "Prüfsummen nicht gefunden"
  (cd "$TMP" && grep -E "[ *]$NAME\$" SHA256SUMS.txt | sed 's/ [ *]/  /' | sha256sum -c --quiet -) || die "Prüfsumme stimmt nicht – Download beschädigt?"
  mv "$TMP/$NAME" "$TMP/paket.tar.gz"
  ok "Paket geladen und geprüft"
fi

mkdir -p "$TMP/x"
tar -xzf "$TMP/paket.tar.gz" -C "$TMP/x"
[ -f "$TMP/x/easyradius" ] || die "Paket enthält kein Programm"

# ---------------------------------------------------------------- Installieren
UPDATE=0
[ -f "$BIN" ] && UPDATE=1

step "Installiere Programm"
id easyradius >/dev/null 2>&1 || useradd --system --home-dir "$DATA" --no-create-home --shell /usr/sbin/nologin easyradius
install -m 0755 "$TMP/x/easyradius" "$BIN"
[ -f "$TMP/x/radtest" ] && install -m 0755 "$TMP/x/radtest" /usr/local/bin/easyradius-radtest
cat > /usr/local/sbin/easyradius-update <<EOF
#!/usr/bin/env bash
# EasyRadius auf die neueste Version aktualisieren.
curl -fsSL https://raw.githubusercontent.com/$REPO/main/linux/install.sh | bash -s -- "\$@"
EOF
chmod 0755 /usr/local/sbin/easyradius-update
ok "$BIN"

cat > "$UNIT" <<EOF
[Unit]
Description=EasyRadius RADIUS-Server
Documentation=https://github.com/$REPO
After=network-online.target
Wants=network-online.target

[Service]
User=easyradius
Group=easyradius
ExecStart=$BIN -data $DATA
StateDirectory=easyradius
StateDirectoryMode=0700
# RADIUS (1812/1813) braucht keine Sonderrechte; Port 80 nur für die Let's-Encrypt-Prüfung.
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
if [ "$UPDATE" -eq 1 ]; then
  systemctl restart easyradius.service
else
  systemctl enable --now easyradius.service >/dev/null 2>&1
fi

step "Starte Dienst"
for _ in $(seq 1 30); do
  if curl -fs "http://127.0.0.1:$WEB_PORT/api/state" >/dev/null 2>&1; then break; fi
  sleep 1
done
if ! curl -fs "http://127.0.0.1:$WEB_PORT/api/state" >/dev/null 2>&1; then
  systemctl --no-pager status easyradius.service | tail -n 15 || true
  die "Der Dienst antwortet nicht. Details: journalctl -u easyradius"
fi
ok "Dienst läuft"

IPS="$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+\.' || true)"
echo
if [ "$UPDATE" -eq 1 ]; then
  printf '%sEasyRadius %s ist aktualisiert.%s\n\n' "$G" "$("$BIN" -version 2>/dev/null || echo "$VERSION")" "$N"
else
  printf '%sEasyRadius ist installiert.%s\n\n' "$G" "$N"
fi
echo "  Weboberfläche:"
for ip in $IPS; do echo "    http://$ip:$WEB_PORT"; done
[ -n "$IPS" ] || echo "    http://<IP dieses Systems>:$WEB_PORT"
echo
echo "  RADIUS:        UDP 1812 (Anmeldung), 1813 (Accounting)"
echo "  Windows-App:   als Server <IP>:$WEB_PORT eintragen"
echo "  Daten:         $DATA"
echo "  Protokoll:     journalctl -u easyradius -f"
echo "  Aktualisieren: easyradius-update"
echo
