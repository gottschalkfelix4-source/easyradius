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
# Vor jedem Update wird das Datenverzeichnis gesichert (/var/lib/easyradius.bak-*.tar.gz, die 3 neuesten bleiben).
#
# Das ganze Skript steckt in main(): Bricht der Download mittendrin ab, wird nichts halb ausgeführt.
set -euo pipefail

main() {
  REPO="gottschalkfelix4-source/easyradius"
  BIN="/usr/local/bin/easyradius"
  DATA="/var/lib/easyradius"
  UNIT="/etc/systemd/system/easyradius.service"
  DROPIN_DIR="/etc/systemd/system/easyradius.service.d"
  DROPIN="$DROPIN_DIR/10-no-sandbox.conf"
  WEB_PORT=8088
  WEB_TLS_PORT=8443

  if [ -t 1 ]; then B=$'\e[1m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; N=$'\e[0m'; else B=""; G=""; Y=""; R=""; N=""; fi
  step() { printf '%s==>%s %s\n' "$B" "$N" "$*"; }
  ok()   { printf ' %s✓%s %s\n' "$G" "$N" "$*"; }
  warn() { printf ' %s!%s %s\n' "$Y" "$N" "$*"; }
  die()  { printf '%sFehler:%s %s\n' "$R" "$N" "$*" >&2; exit 1; }

  usage() {
    cat <<'EOF'
EasyRadius unter Debian/Ubuntu installieren oder aktualisieren – auch im Proxmox-LXC.

Aufruf:
  curl -fsSL https://raw.githubusercontent.com/gottschalkfelix4-source/easyradius/main/linux/install.sh | bash
  curl -fsSL …/linux/install.sh | bash -s -- [Optionen]

Optionen:
  --version 1.6.0      bestimmte Version statt der neuesten
  --file paket.tar.gz  lokales Paket statt Download (z. B. ohne Internet)
  --uninstall          Dienst und Programm entfernen (Daten bleiben erhalten)
  --purge              zusammen mit --uninstall: auch alle Daten löschen
  -h, --help           diese Hilfe anzeigen

Ein erneuter Aufruf aktualisiert eine bestehende Installation; Daten bleiben erhalten.
Vor jedem Update wird das Datenverzeichnis nach /var/lib/easyradius.bak-<Zeit>.tar.gz gesichert.
EOF
  }

  # ---------------------------------------------------------------- Optionen
  VERSION=""; FILE=""; UNINSTALL=0; PURGE=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --version|--file)
        if [ $# -lt 2 ] || [ -z "$2" ] || [[ "$2" == -* ]]; then die "$1 braucht einen Wert (siehe --help)."; fi
        if [ "$1" = "--version" ]; then VERSION="$2"; else FILE="$2"; fi
        shift 2 ;;
      --version=*) VERSION="${1#*=}"; [ -n "$VERSION" ] || die "--version braucht einen Wert (siehe --help)."; shift ;;
      --file=*) FILE="${1#*=}"; [ -n "$FILE" ] || die "--file braucht einen Wert (siehe --help)."; shift ;;
      --uninstall) UNINSTALL=1; shift ;;
      --purge) PURGE=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) die "Unbekannte Option: $1 (siehe --help)" ;;
    esac
  done
  if [ "$PURGE" -eq 1 ] && [ "$UNINSTALL" -eq 0 ]; then die "--purge geht nur zusammen mit --uninstall."; fi
  if [ -n "$VERSION" ]; then
    [[ "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "Ungültige Version: $VERSION (erwartet z. B. 1.6.0)"
    VERSION="${VERSION#v}"
  fi

  [ "$(id -u)" -eq 0 ] || die "Bitte als root ausführen (z. B. mit sudo)."
  [ -d /run/systemd/system ] || die "systemd wird benötigt."

  # ---------------------------------------------------------------- Deinstallation
  if [ "$UNINSTALL" -eq 1 ]; then
    step "EasyRadius wird entfernt"
    systemctl disable --now easyradius.service 2>/dev/null || true
    rm -f "$UNIT" "$BIN" "$BIN.old" "$BIN.new" /usr/local/bin/easyradius-radtest /usr/local/sbin/easyradius-update
    rm -rf "$DROPIN_DIR"
    systemctl daemon-reload
    systemctl reset-failed easyradius.service 2>/dev/null || true
    if [ "$PURGE" -eq 1 ]; then
      rm -rf "$DATA"
      rm -f "$DATA".bak-*.tar.gz
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
    apt-get update -qq || die "apt-get update fehlgeschlagen (Internetzugang?)."
    apt-get install -y -qq curl ca-certificates tar coreutils >/dev/null || die "Pakete konnten nicht installiert werden."
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
  tar -xzf "$TMP/paket.tar.gz" -C "$TMP/x" || die "Paket lässt sich nicht entpacken – beschädigt?"
  [ -f "$TMP/x/easyradius" ] || die "Paket enthält kein Programm"

  # ---------------------------------------------------------------- Hilfsfunktionen Dienst
  health() { curl -fs --max-time 3 "http://127.0.0.1:$WEB_PORT/api/state" >/dev/null 2>&1; }

  main_status() { # Exit-Status des letzten Starts (226 = NAMESPACE)
    local s
    s="$(systemctl show -p ExecMainStatus easyradius.service 2>/dev/null || true)"
    echo "${s#*=}"
  }

  wait_healthy() { # bis zu ~30 s auf die Weboberfläche warten
    local i
    for i in $(seq 1 30); do
      if health; then return 0; fi
      # Scheitert schon das Einrichten der Sandbox, braucht man nicht weiter zu warten.
      if [ "$i" -ge 3 ] && [ "$(main_status)" = "226" ]; then return 1; fi
      sleep 1
    done
    health
  }

  in_container() { command -v systemd-detect-virt >/dev/null 2>&1 && systemd-detect-virt -c -q 2>/dev/null; }

  namespace_error() { # $1 = Zeitpunkt des Starts (für journalctl --since)
    local log
    [ "$(main_status)" = "226" ] && return 0
    log="$(journalctl -u easyradius.service --since "$1" --no-pager 2>/dev/null || true)"
    grep -qE '226/NAMESPACE|status=226|Failed to set up mount namespacing|namespace' <<<"$log"
  }

  service_up() { # aktiviert und (neu) startet den Dienst; 0 = Weboberfläche antwortet
    local since out
    out="$(systemctl enable easyradius.service 2>&1)" || die "Dienst konnte nicht aktiviert werden (systemctl enable easyradius): $out"
    since="$(date '+%Y-%m-%d %H:%M:%S')"
    systemctl reset-failed easyradius.service >/dev/null 2>&1 || true
    if ! out="$(systemctl restart easyradius.service 2>&1)"; then
      warn "systemctl restart easyradius meldet einen Fehler: ${out:-ohne Text}"
    fi
    if wait_healthy; then return 0; fi

    # Container ohne „nesting“: systemd kann die Schutzfunktionen (eigene Namespaces) nicht einrichten.
    if in_container && [ ! -f "$DROPIN" ] && namespace_error "$since"; then
      warn "Die systemd-Sandbox wird in diesem Container nicht unterstützt – starte den Dienst ohne Sandbox."
      mkdir -p "$DROPIN_DIR"
      cat > "$DROPIN" <<'EOF'
# Von install.sh angelegt: Der Container erlaubt keine eigenen Namespaces (Proxmox: nesting=0).
# Nach dem Aktivieren von nesting=1 kann diese Datei gelöscht werden
# (danach: systemctl daemon-reload && systemctl restart easyradius).
[Service]
ProtectSystem=no
ProtectHome=no
PrivateTmp=no
EOF
      systemctl daemon-reload
      systemctl reset-failed easyradius.service >/dev/null 2>&1 || true
      if ! out="$(systemctl restart easyradius.service 2>&1)"; then
        warn "systemctl restart easyradius meldet einen Fehler: ${out:-ohne Text}"
      fi
      if wait_healthy; then
        warn "Tipp: In Proxmox beim Container unter Optionen → Features „Nesting“ aktivieren (nesting=1),"
        warn "dann $DROPIN löschen und den Dienst neu starten – so läuft EasyRadius wieder abgeschottet."
        return 0
      fi
    fi
    return 1
  }

  prune_backups() { # nur die 3 neuesten Sicherungen behalten
    local f n=0
    while IFS= read -r f; do
      n=$((n + 1))
      if [ "$n" -gt 3 ]; then rm -f -- "$f"; fi
    done < <(ls -1 "$DATA".bak-*.tar.gz 2>/dev/null | sort -r)
  }

  abort_update() { # vor dem Austausch des Programms: alte Version weiterlaufen lassen
    systemctl start easyradius.service >/dev/null 2>&1 || true
    die "$1"
  }

  # ---------------------------------------------------------------- Installieren
  UPDATE=0; BAK=""
  if [ -f "$BIN" ]; then UPDATE=1; fi

  step "Installiere Programm"
  if ! id easyradius >/dev/null 2>&1; then
    if getent group easyradius >/dev/null 2>&1; then
      useradd --system -g easyradius --home-dir "$DATA" --no-create-home --shell /usr/sbin/nologin easyradius \
        || die "Benutzer easyradius konnte nicht angelegt werden."
    else
      useradd --system --user-group --home-dir "$DATA" --no-create-home --shell /usr/sbin/nologin easyradius \
        || die "Benutzer easyradius konnte nicht angelegt werden."
    fi
  fi

  if [ "$UPDATE" -eq 1 ]; then
    # Dienst anhalten, damit die Sicherung einen stimmigen Stand enthält.
    systemctl stop easyradius.service >/dev/null 2>&1 || true
    if [ -d "$DATA" ]; then
      BAK="$DATA.bak-$(date +%Y%m%d-%H%M%S).tar.gz"
      if ! (umask 077 && tar -czf "$BAK" -C "$(dirname "$DATA")" "$(basename "$DATA")"); then
        rm -f "$BAK"
        abort_update "Datensicherung nach $BAK fehlgeschlagen (Speicherplatz?). Das Update wurde nicht eingespielt."
      fi
      prune_backups
      ok "Daten gesichert: $BAK"
    fi
    cp -p "$BIN" "$BIN.old" || abort_update "Konnte die bisherige Version nicht nach $BIN.old sichern."
  fi
  if ! { install -m 0755 "$TMP/x/easyradius" "$BIN.new" && mv -f "$BIN.new" "$BIN"; }; then
    rm -f "$BIN.new"
    if [ "$UPDATE" -eq 1 ]; then abort_update "Programm konnte nicht nach $BIN kopiert werden."; fi
    die "Programm konnte nicht nach $BIN kopiert werden."
  fi
  if [ -f "$TMP/x/radtest" ]; then install -m 0755 "$TMP/x/radtest" /usr/local/bin/easyradius-radtest; fi

  cat > /usr/local/sbin/easyradius-update <<EOF
#!/usr/bin/env bash
# EasyRadius auf die neueste Version aktualisieren (Optionen wie install.sh, z. B. --version 1.6.0).
set -euo pipefail
tmp="\$(mktemp)"
trap 'rm -f "\$tmp"' EXIT
if ! curl -fsSL -o "\$tmp" "https://raw.githubusercontent.com/$REPO/main/linux/install.sh"; then
  echo "Fehler: Installationsskript konnte nicht geladen werden (Internetzugang?)." >&2
  exit 1
fi
rc=0
bash "\$tmp" "\$@" || rc=\$?
exit "\$rc"
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
  systemctl daemon-reload || die "systemctl daemon-reload fehlgeschlagen."

  step "Starte Dienst"
  if ! service_up; then
    systemctl --no-pager status easyradius.service 2>&1 | tail -n 15 || true
    if [ "$UPDATE" -eq 1 ] && [ -f "$BIN.old" ]; then
      warn "Die neue Version startet nicht – stelle die vorherige Version wieder her."
      mv -f "$BIN.old" "$BIN"
      systemctl reset-failed easyradius.service >/dev/null 2>&1 || true
      systemctl restart easyradius.service >/dev/null 2>&1 || true
      if wait_healthy; then
        die "Update fehlgeschlagen – die vorherige Version läuft wieder.${BAK:+ Datensicherung: $BAK.} Details: journalctl -u easyradius"
      fi
      die "Update fehlgeschlagen und auch die vorherige Version startet nicht.${BAK:+ Datensicherung: $BAK.} Details: journalctl -u easyradius"
    fi
    die "Der Dienst antwortet nicht. Details: journalctl -u easyradius"
  fi
  ok "Dienst läuft"

  # Nur IPv4-Adressen anzeigen.
  IPS="$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]{1,3}(\.[0-9]{1,3}){3}$' || true)"
  if [ -z "$IPS" ]; then
    IPS="$(ip -4 -o addr show scope global 2>/dev/null | awk '{split($4, a, "/"); print a[1]}' || true)"
  fi
  echo
  if [ "$UPDATE" -eq 1 ]; then
    printf '%sEasyRadius %s ist aktualisiert.%s\n\n' "$G" "$("$BIN" -version 2>/dev/null || echo "$VERSION")" "$N"
  else
    printf '%sEasyRadius ist installiert.%s\n\n' "$G" "$N"
  fi
  echo "  Weboberfläche:"
  for ip in $IPS; do
    echo "    https://$ip:$WEB_TLS_PORT   (empfohlen)"
    echo "    http://$ip:$WEB_PORT"
  done
  if [ -z "$IPS" ]; then
    echo "    https://<IP dieses Systems>:$WEB_TLS_PORT   (empfohlen)"
    echo "    http://<IP dieses Systems>:$WEB_PORT"
  fi
  echo "    Der Browser warnt bei HTTPS, solange er der Zertifizierungsstelle nicht vertraut –"
  echo "    Zertifikat einmal bestätigen. Port $WEB_PORT (HTTP) funktioniert weiterhin."
  echo
  echo "  RADIUS:        UDP 1812 (Anmeldung), 1813 (Accounting)"
  echo "  Windows-App:   als Server einfach die IP-Adresse eintragen (verbindet per HTTPS)"
  echo "  Daten:         $DATA"
  echo "  Protokoll:     journalctl -u easyradius -f"
  echo "  Aktualisieren: easyradius-update"
  echo
}

main "$@"
