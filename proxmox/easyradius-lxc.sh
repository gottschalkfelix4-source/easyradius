#!/usr/bin/env bash
# EasyRadius als LXC-Container auf Proxmox VE anlegen.
# Auf dem Proxmox-Host (Shell) als root ausführen:
#
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/gottschalkfelix4-source/easyradius/main/proxmox/easyradius-lxc.sh)"
#
# Ohne Rückfragen mit eigenen Werten (alle optional):
#   CTID=150 CT_HOSTNAME=radius CT_IP=192.168.1.20/24 CT_GW=192.168.1.1 CT_STORAGE=local-lvm \
#   EASYRADIUS_YES=1 bash -c "$(curl -fsSL …/easyradius-lxc.sh)"
#
# Variablen: CTID, CT_HOSTNAME, CT_CORES, CT_RAM, CT_SWAP, CT_DISK (GB), CT_STORAGE, CT_TEMPLATE_STORAGE,
#            CT_BRIDGE, CT_VLAN, CT_IP (dhcp oder CIDR), CT_GW, CT_DNS, CT_PASSWORD, EASYRADIUS_VERSION
set -euo pipefail

REPO="gottschalkfelix4-source/easyradius"
RAW="https://raw.githubusercontent.com/$REPO/main"

if [ -t 1 ]; then B=$'\e[1m'; G=$'\e[32m'; Y=$'\e[33m'; R=$'\e[31m'; N=$'\e[0m'; else B=""; G=""; Y=""; R=""; N=""; fi
step() { printf '%s==>%s %s\n' "$B" "$N" "$*"; }
ok()   { printf ' %s✓%s %s\n' "$G" "$N" "$*"; }
die()  { printf '%sFehler:%s %s\n' "$R" "$N" "$*" >&2; exit 1; }

# ---------------------------------------------------------------- Prüfungen
[ "$(id -u)" -eq 0 ] || die "Bitte als root auf dem Proxmox-Host ausführen."
command -v pct >/dev/null 2>&1 && command -v pveam >/dev/null 2>&1 || die "Dieses Skript läuft nur auf einem Proxmox-VE-Host."
command -v pvesm >/dev/null 2>&1 || die "pvesm nicht gefunden – ist das ein Proxmox-VE-Host?"

# Speicher wählen: bevorzugt local-lvm bzw. local, sonst den ersten passenden.
pick_storage() { # $1 = Inhaltstyp (rootdir/vztmpl), $2 = bevorzugter Name
  local list
  list="$(pvesm status -content "$1" 2>/dev/null | awk 'NR>1 && $3=="active" {print $1}')"
  [ -n "$list" ] || return 1
  if grep -qx "$2" <<<"$list"; then echo "$2"; else head -n1 <<<"$list"; fi
}

CTID="${CTID:-$(pvesh get /cluster/nextid)}"
CT_HOSTNAME="${CT_HOSTNAME:-easyradius}"
CT_CORES="${CT_CORES:-1}"
CT_RAM="${CT_RAM:-512}"
CT_SWAP="${CT_SWAP:-512}"
CT_DISK="${CT_DISK:-4}"
CT_STORAGE="${CT_STORAGE:-$(pick_storage rootdir local-lvm || true)}"
CT_TEMPLATE_STORAGE="${CT_TEMPLATE_STORAGE:-$(pick_storage vztmpl local || true)}"
CT_BRIDGE="${CT_BRIDGE:-vmbr0}"
CT_VLAN="${CT_VLAN:-}"
CT_IP="${CT_IP:-dhcp}"
CT_GW="${CT_GW:-}"
CT_DNS="${CT_DNS:-}"
CT_PASSWORD="${CT_PASSWORD:-}"
EASYRADIUS_VERSION="${EASYRADIUS_VERSION:-}"

[ -n "$CT_STORAGE" ] || die "Kein Speicher für Container gefunden (Inhalt „rootdir“)."
[ -n "$CT_TEMPLATE_STORAGE" ] || die "Kein Speicher für Vorlagen gefunden (Inhalt „vztmpl“)."

summary() {
  cat <<EOF
Container-ID:   $CTID
Name:           $CT_HOSTNAME
CPU / RAM:      $CT_CORES Kern(e) / $CT_RAM MB (+ $CT_SWAP MB Swap)
Festplatte:     $CT_DISK GB auf $CT_STORAGE
Netzwerk:       $CT_BRIDGE${CT_VLAN:+ (VLAN $CT_VLAN)}, IP: $CT_IP${CT_GW:+, Gateway $CT_GW}
System:         Debian (unprivilegierter Container)
EOF
}

# ---------------------------------------------------------------- Rückfragen
ask() { # $1 Titel, $2 Text, $3 Vorgabe
  whiptail --title "$1" --inputbox "$2" 10 64 "$3" 3>&1 1>&2 2>&3
}

if [ -z "${EASYRADIUS_YES:-}" ] && [ -t 0 ] && command -v whiptail >/dev/null 2>&1; then
  if ! whiptail --title "EasyRadius LXC" --yesno "Container mit diesen Einstellungen anlegen?\n\n$(summary)\n\n„Nein“ öffnet die erweiterten Einstellungen." 18 72; then
    CTID="$(ask "Container-ID" "Freie ID für den Container:" "$CTID")" || exit 1
    CT_HOSTNAME="$(ask "Name" "Hostname des Containers:" "$CT_HOSTNAME")" || exit 1
    CT_CORES="$(ask "CPU" "Anzahl CPU-Kerne:" "$CT_CORES")" || exit 1
    CT_RAM="$(ask "Arbeitsspeicher" "RAM in MB:" "$CT_RAM")" || exit 1
    CT_DISK="$(ask "Festplatte" "Größe in GB:" "$CT_DISK")" || exit 1
    CT_STORAGE="$(ask "Speicher" "Speicher für den Container ($(pvesm status -content rootdir | awk 'NR>1{printf "%s ", $1}')):" "$CT_STORAGE")" || exit 1
    CT_BRIDGE="$(ask "Netzwerk" "Bridge:" "$CT_BRIDGE")" || exit 1
    CT_VLAN="$(ask "VLAN" "VLAN-Tag (leer = keins):" "$CT_VLAN")" || exit 1
    CT_IP="$(ask "IP-Adresse" "dhcp oder feste Adresse mit Präfix, z. B. 192.168.1.20/24:" "$CT_IP")" || exit 1
    if [ "$CT_IP" != "dhcp" ]; then
      CT_GW="$(ask "Gateway" "Gateway, z. B. 192.168.1.1:" "$CT_GW")" || exit 1
      CT_DNS="$(ask "DNS" "DNS-Server (leer = wie der Host):" "$CT_DNS")" || exit 1
    fi
    CT_PASSWORD="$(whiptail --title "root-Passwort" --passwordbox "root-Passwort für den Container (leer = keins, Zugang dann über „pct enter $CTID“):" 10 64 3>&1 1>&2 2>&3)" || exit 1
    whiptail --title "EasyRadius LXC" --yesno "Container jetzt anlegen?\n\n$(summary)" 16 72 || exit 1
  fi
else
  echo; summary; echo
fi

[[ "$CTID" =~ ^[0-9]+$ ]] || die "Container-ID muss eine Zahl sein."
if pct status "$CTID" >/dev/null 2>&1; then die "Container $CTID existiert bereits."; fi
if [ "$CT_IP" != "dhcp" ] && [[ ! "$CT_IP" =~ ^[0-9.]+/[0-9]+$ ]]; then die "IP bitte als CIDR angeben, z. B. 192.168.1.20/24"; fi

# ---------------------------------------------------------------- Vorlage
step "Debian-Vorlage vorbereiten"
pveam update >/dev/null 2>&1 || true
TEMPLATE=""
for v in 13 12; do
  TEMPLATE="$(pveam available --section system 2>/dev/null | awk '{print $2}' | grep -E "^debian-$v-standard_.*_amd64" | sort -V | tail -n1 || true)"
  [ -n "$TEMPLATE" ] && break
done
[ -n "$TEMPLATE" ] || die "Keine Debian-Vorlage gefunden (Internetzugang des Hosts?)."
if ! grep -q "$TEMPLATE" <<<"$(pveam list "$CT_TEMPLATE_STORAGE" 2>/dev/null)"; then
  pveam download "$CT_TEMPLATE_STORAGE" "$TEMPLATE" >/dev/null || die "Download der Vorlage fehlgeschlagen."
fi
ok "$TEMPLATE"

# ---------------------------------------------------------------- Container
step "Container $CTID anlegen"
NET="name=eth0,bridge=$CT_BRIDGE,ip=$CT_IP"
[ -n "$CT_GW" ] && NET="$NET,gw=$CT_GW"
[ -n "$CT_VLAN" ] && NET="$NET,tag=$CT_VLAN"
ARGS=(
  --hostname "$CT_HOSTNAME" --cores "$CT_CORES" --memory "$CT_RAM" --swap "$CT_SWAP"
  --rootfs "$CT_STORAGE:$CT_DISK" --net0 "$NET" --ostype debian
  --unprivileged 1 --features nesting=1 --onboot 1 --tags easyradius
  --description "EasyRadius – RADIUS-Server mit Weboberfläche (Port 8088). https://github.com/$REPO"
)
[ -n "$CT_DNS" ] && ARGS+=(--nameserver "$CT_DNS")
[ -n "$CT_PASSWORD" ] && ARGS+=(--password "$CT_PASSWORD")
pct create "$CTID" "$CT_TEMPLATE_STORAGE:vztmpl/$TEMPLATE" "${ARGS[@]}" >/dev/null || die "pct create fehlgeschlagen."
ok "Container angelegt"

step "Container starten"
pct start "$CTID"
for _ in $(seq 1 60); do
  if pct exec "$CTID" -- getent hosts github.com >/dev/null 2>&1; then break; fi
  sleep 1
done
pct exec "$CTID" -- getent hosts github.com >/dev/null 2>&1 || die "Der Container hat keine Internetverbindung (IP/Gateway/DNS prüfen)."
ok "Netzwerk bereit"

# Zeitzone des Hosts übernehmen (für die Zeiten im Protokoll).
TZ_HOST="$(timedatectl show -p Timezone --value 2>/dev/null || cat /etc/timezone 2>/dev/null || echo Etc/UTC)"
pct exec "$CTID" -- ln -sf "/usr/share/zoneinfo/$TZ_HOST" /etc/localtime 2>/dev/null || true

step "System aktualisieren"
pct exec "$CTID" -- bash -c "export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get -y -qq dist-upgrade >/dev/null && apt-get install -y -qq curl ca-certificates >/dev/null"
ok "Debian aktuell"

step "EasyRadius installieren"
pct exec "$CTID" -- bash -c "curl -fsSL $RAW/linux/install.sh | bash -s -- ${EASYRADIUS_VERSION:+--version $EASYRADIUS_VERSION}"

IP="$(pct exec "$CTID" -- hostname -I 2>/dev/null | awk '{print $1}')"
echo
printf '%sFertig! EasyRadius läuft im Container %s (%s).%s\n\n' "$G" "$CTID" "$CT_HOSTNAME" "$N"
echo "  Weboberfläche:   http://${IP:-<IP>}:8088"
echo "  Windows-App:     als Server ${IP:-<IP>}:8088 eintragen"
echo "  RADIUS-Server:   ${IP:-<IP>}, UDP 1812/1813"
echo "  Konsole:         pct enter $CTID"
echo "  Aktualisieren:   pct exec $CTID -- easyradius-update"
echo
[ "$CT_IP" = "dhcp" ] && printf '%sTipp:%s Für einen RADIUS-Server empfiehlt sich eine feste IP (oder eine DHCP-Reservierung),\n      damit Access Points und Switches ihn immer unter derselben Adresse finden.\n\n' "$Y" "$N"
exit 0
