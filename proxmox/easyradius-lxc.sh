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
#            CT_BRIDGE, CT_VLAN, CT_IP (dhcp oder CIDR), CT_GW, CT_DNS, CT_PASSWORD, CT_UNPRIVILEGED (1/0),
#            CT_ONBOOT (1/0), EASYRADIUS_VERSION
set -Eeuo pipefail

REPO="gottschalkfelix4-source/easyradius"
RAW="https://raw.githubusercontent.com/$REPO/main"
APP="EasyRadius"
LOG="/tmp/easyradius-lxc-$(date +%Y%m%d-%H%M%S).log"
: >"$LOG"

# ================================================================ Darstellung
if [ -t 1 ]; then
  BL=$'\e[36m'; GN=$'\e[1;92m'; RD=$'\e[01;31m'; YW=$'\e[33m'; DGN=$'\e[32m'; BD=$'\e[1m'; DIM=$'\e[2m'; CL=$'\e[m'
  TAB="  "; CM="${GN}✓${CL}"; CROSS="${RD}✗${CL}"; INFO="${YW}➜${CL}"
else
  BL=""; GN=""; RD=""; YW=""; DGN=""; BD=""; DIM=""; CL=""; TAB="  "; CM="[ok]"; CROSS="[x]"; INFO="->"
fi

# Dunkles Farbschema für die Dialoge, passend zum Logo (eigene Farben per NEWT_COLORS möglich).
export NEWT_COLORS="${NEWT_COLORS:-
root=brightcyan,black
roottext=brightcyan,black
border=cyan,black
window=white,black
shadow=black,black
title=brightcyan,black
textbox=white,black
button=black,cyan
actbutton=black,brightcyan
compactbutton=white,black
listbox=white,black
actlistbox=black,cyan
sellistbox=brightcyan,black
actsellistbox=black,brightcyan
checkbox=white,black
actcheckbox=black,cyan
entry=white,black
disentry=gray,black
label=white,black
helpline=cyan,black
}"

header() {
  [ -t 1 ] && clear
  cat <<EOF
${BL}${BD} _____        _  _                ____  _                    _
|  ___|  ___ | |(_)__  __        / ___|| |  ___   _   _   __| |
| |_    / _ \\| || |\\ \\/ / _____ | |    | | / _ \\ | | | | / _\` |
|  _|  |  __/| || | >  < |_____|| |___ | || (_) || |_| || (_| |
|_|     \\___||_||_|/_/\\_\\        \\____||_| \\___/  \\__,_| \\__,_|${CL}

${TAB}${BD}${APP}${CL} ${DIM}·${CL} RADIUS-Server für WLAN und Switches ${DIM}·${CL} Proxmox-LXC
${TAB}${DIM}https://github.com/$REPO${CL}

EOF
}

SPINNER_PID=""
spinner() {
  local frames=(⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏) i=0
  while :; do
    printf '\r%s%s%s %s' "$TAB" "$YW" "${frames[i++ % ${#frames[@]}]}" "$CL$MSG"
    sleep 0.1
  done
}
stop_spinner() {
  if [ -n "$SPINNER_PID" ]; then kill "$SPINNER_PID" 2>/dev/null || true; wait "$SPINNER_PID" 2>/dev/null || true; SPINNER_PID=""; fi
  [ -t 1 ] && printf '\r\e[K\e[?25h'   # Zeile leeren, Cursor wieder zeigen
  return 0
}
trap 'stop_spinner' EXIT
msg_info() { MSG="$1"; if [ -t 1 ]; then printf '\e[?25l'; spinner & SPINNER_PID=$!; else echo "${TAB}${INFO} $1"; fi; }
msg_ok()   { stop_spinner; echo "${TAB}${CM} $1"; }
msg_warn() { stop_spinner; echo "${TAB}${YW}!${CL} $1"; }
msg_err()  { stop_spinner; echo "${TAB}${CROSS} ${RD}$1${CL}"; }
run()      { "$@" >>"$LOG" 2>&1; }   # Befehle laufen leise, Ausgabe landet im Protokoll

CREATED=0
on_error() {
  local code=$? line=${1:-?}
  [ "${BASH_SUBSHELL:-0}" -gt 0 ] && exit "$code"   # nur die Hauptshell meldet
  if [ "$code" -eq 99 ]; then stop_spinner; echo; echo "${TAB}${INFO} Abgebrochen – es wurde nichts verändert."; exit 0; fi
  stop_spinner
  echo
  msg_err "Fehlgeschlagen: ${MSG:-Vorbereitung} (Code $code, Zeile $line)"
  echo "${TAB}${DIM}Protokoll: $LOG${CL}"
  echo "${DIM}$(tail -n 15 "$LOG" 2>/dev/null)${CL}"
  if [ "$CREATED" -eq 1 ]; then
    if interactive && whiptail --title "$APP" --yesno "Die Installation ist fehlgeschlagen.\n\nContainer $CTID wieder entfernen?" 10 60; then
      pct stop "$CTID" >/dev/null 2>&1 || true
      pct destroy "$CTID" --purge >/dev/null 2>&1 || true
      msg_ok "Container $CTID entfernt"
    else
      echo "${TAB}${INFO} Container $CTID bleibt bestehen (entfernen: pct destroy $CTID)."
    fi
  fi
  exit "$code"
}
trap 'on_error $LINENO' ERR
trap 'stop_spinner; echo; exit 130' INT

die() { msg_err "$1"; exit 1; }
interactive() { [ -z "${EASYRADIUS_YES:-}" ] && [ -t 0 ] && command -v whiptail >/dev/null 2>&1; }

# ================================================================ Prüfungen
header
[ "$(id -u)" -eq 0 ] || die "Bitte als root auf dem Proxmox-Host ausführen."
command -v pct >/dev/null 2>&1 && command -v pveam >/dev/null 2>&1 && command -v pvesm >/dev/null 2>&1 \
  || die "Dieses Skript läuft nur auf einem Proxmox-VE-Host."
[ "$(uname -m)" = "x86_64" ] || die "Proxmox-LXC-Vorlagen gibt es hier nur für amd64."

# ================================================================ Vorgaben
storages() { # $1 = Inhaltstyp → "Name|Typ|frei in GB" je Zeile
  pvesm status -content "$1" 2>/dev/null | awk 'NR>1 && $3=="active" {printf "%s|%s|%.1f\n", $1, $2, $6/1024/1024}'
}
pick_storage() { # bevorzugt $2, sonst der Speicher mit dem meisten Platz
  local list; list="$(storages "$1")"
  [ -n "$list" ] || return 0
  if grep -q "^$2|" <<<"$list"; then echo "$2"; else sort -t'|' -k3 -rn <<<"$list" | head -n1 | cut -d'|' -f1; fi
}
bridges() { ip -o link show type bridge 2>/dev/null | awk -F': ' '{print $2}' | cut -d'@' -f1; }

CTID="${CTID:-$(pvesh get /cluster/nextid 2>/dev/null || echo 100)}"
CT_HOSTNAME="${CT_HOSTNAME:-easyradius}"
CT_CORES="${CT_CORES:-1}"
CT_RAM="${CT_RAM:-512}"
CT_SWAP="${CT_SWAP:-512}"
CT_DISK="${CT_DISK:-4}"
CT_STORAGE="${CT_STORAGE:-$(pick_storage rootdir local-lvm)}"
CT_TEMPLATE_STORAGE="${CT_TEMPLATE_STORAGE:-$(pick_storage vztmpl local)}"
CT_BRIDGE="${CT_BRIDGE:-$(bridges | grep -x vmbr0 || bridges | head -n1 || true)}"
CT_BRIDGE="${CT_BRIDGE:-vmbr0}"
CT_VLAN="${CT_VLAN:-}"
CT_IP="${CT_IP:-dhcp}"
CT_GW="${CT_GW:-}"
CT_DNS="${CT_DNS:-}"
CT_PASSWORD="${CT_PASSWORD:-}"
CT_UNPRIVILEGED="${CT_UNPRIVILEGED:-1}"
CT_ONBOOT="${CT_ONBOOT:-1}"
EASYRADIUS_VERSION="${EASYRADIUS_VERSION:-}"

[ -n "$CT_STORAGE" ] || die "Kein Speicher für Container gefunden (Inhalt „rootdir“)."
[ -n "$CT_TEMPLATE_STORAGE" ] || die "Kein Speicher für Vorlagen gefunden (Inhalt „vztmpl“)."

summary() {
  local typ="unprivilegiert"; [ "$CT_UNPRIVILEGED" = "1" ] || typ="privilegiert"
  cat <<EOF
Container-ID   $CTID
Name           $CT_HOSTNAME
Typ            $typ, Autostart: $([ "$CT_ONBOOT" = "1" ] && echo ja || echo nein)
CPU-Kerne      $CT_CORES
RAM            $CT_RAM MB (+ $CT_SWAP MB Swap)
Festplatte     $CT_DISK GB auf $CT_STORAGE
Netzwerk       $CT_BRIDGE${CT_VLAN:+, VLAN $CT_VLAN}
IP-Adresse     $CT_IP${CT_GW:+, Gateway $CT_GW}${CT_DNS:+, DNS $CT_DNS}
root-Passwort  $([ -n "$CT_PASSWORD" ] && echo gesetzt || echo "keins (Konsole meldet automatisch an)")
EOF
}

# ================================================================ Menüs
WT_TITLE="Felix-Cloud · $APP"
cancel() { # in Untermenüs (Subshell) mit Code 99 enden, damit die Hauptshell sauber abbricht
  [ "${BASH_SUBSHELL:-0}" -gt 0 ] && exit 99
  stop_spinner; echo; echo "${TAB}${INFO} Abgebrochen – es wurde nichts verändert."; exit 0
}

# Auswahl mit Vorgaben und „Eigener Wert“. $1 Titel $2 Text $3 aktueller Wert $4 Einheit $5.. Vorgaben
choose_number() {
  local title="$1" text="$2" cur="$3" unit="$4"; shift 4
  local args=() found=0 v
  for v in "$@"; do
    if [ "$v" = "$cur" ]; then args+=("$v" "$unit" ON); found=1; else args+=("$v" "$unit" OFF); fi
  done
  if [ "$found" -eq 0 ]; then args+=("eigen" "Eigener Wert ($cur $unit)" ON); else args+=("eigen" "Eigener Wert …" OFF); fi
  local pick
  pick="$(whiptail --backtitle "$WT_TITLE" --title "$title" --radiolist "$text" 16 60 7 "${args[@]}" 3>&1 1>&2 2>&3)" || cancel
  if [ "$pick" = "eigen" ]; then
    while :; do
      pick="$(whiptail --backtitle "$WT_TITLE" --title "$title" --inputbox "$text\n\nWert in $unit:" 11 60 "$cur" 3>&1 1>&2 2>&3)" || cancel
      [[ "$pick" =~ ^[0-9]+$ ]] && [ "$pick" -gt 0 ] && break
      whiptail --backtitle "$WT_TITLE" --title "$title" --msgbox "Bitte eine ganze Zahl größer als 0 eingeben." 8 50
    done
  fi
  echo "$pick"
}

choose_storage() { # $1 Inhaltstyp $2 aktueller Wert $3 Titel
  local args=() name typ free
  while IFS='|' read -r name typ free; do
    [ -n "$name" ] || continue
    args+=("$name" "$(printf '%-10s %8s GB frei' "$typ" "$free")" "$([ "$name" = "$2" ] && echo ON || echo OFF)")
  done < <(storages "$1")
  [ ${#args[@]} -gt 3 ] || { echo "$2"; return; }
  whiptail --backtitle "$WT_TITLE" --title "$3" --radiolist "Wo soll gespeichert werden?" 16 64 6 "${args[@]}" 3>&1 1>&2 2>&3 || cancel
}

advanced() {
  local v
  CTID="$(whiptail --backtitle "$WT_TITLE" --title "Container-ID" --inputbox "Freie ID für den Container:" 9 58 "$CTID" 3>&1 1>&2 2>&3)" || cancel
  CT_HOSTNAME="$(whiptail --backtitle "$WT_TITLE" --title "Name" --inputbox "Hostname des Containers:" 9 58 "$CT_HOSTNAME" 3>&1 1>&2 2>&3)" || cancel
  v="$(whiptail --backtitle "$WT_TITLE" --title "Container-Typ" --radiolist "Unprivilegiert ist sicherer und für EasyRadius ausreichend." 12 64 2 \
      1 "Unprivilegiert (empfohlen)" "$([ "$CT_UNPRIVILEGED" = 1 ] && echo ON || echo OFF)" \
      0 "Privilegiert" "$([ "$CT_UNPRIVILEGED" = 1 ] && echo OFF || echo ON)" 3>&1 1>&2 2>&3)" || cancel
  CT_UNPRIVILEGED="$v"

  CT_CORES="$(choose_number "CPU-Kerne" "Wie viele CPU-Kerne bekommt der Container?\nEasyRadius kommt mit einem Kern gut aus." "$CT_CORES" "Kern(e)" 1 2 4)"
  CT_RAM="$(choose_number "Arbeitsspeicher" "Wie viel RAM bekommt der Container?\n512 MB reichen für Hunderte Benutzer." "$CT_RAM" "MB" 512 1024 2048 4096)"
  CT_SWAP="$(choose_number "Swap" "Auslagerungsspeicher:" "$CT_SWAP" "MB" 0 512 1024)"
  CT_DISK="$(choose_number "Festplatte" "Größe der Festplatte. Programm und Daten brauchen weniger als 1 GB." "$CT_DISK" "GB" 4 8 16 32)"
  CT_STORAGE="$(choose_storage rootdir "$CT_STORAGE" "Speicher für den Container")"

  local br_args=() b
  while read -r b; do [ -n "$b" ] && br_args+=("$b" "Netzwerk-Bridge" "$([ "$b" = "$CT_BRIDGE" ] && echo ON || echo OFF)"); done < <(bridges)
  if [ ${#br_args[@]} -gt 3 ]; then
    CT_BRIDGE="$(whiptail --backtitle "$WT_TITLE" --title "Netzwerk" --radiolist "An welcher Bridge hängt der Container?" 14 60 5 "${br_args[@]}" 3>&1 1>&2 2>&3)" || cancel
  else
    CT_BRIDGE="$(whiptail --backtitle "$WT_TITLE" --title "Netzwerk" --inputbox "Bridge:" 9 58 "$CT_BRIDGE" 3>&1 1>&2 2>&3)" || cancel
  fi
  CT_VLAN="$(whiptail --backtitle "$WT_TITLE" --title "VLAN" --inputbox "VLAN-Tag (leer = kein VLAN):" 9 58 "$CT_VLAN" 3>&1 1>&2 2>&3)" || cancel

  v="$(whiptail --backtitle "$WT_TITLE" --title "IP-Adresse" --radiolist "Für einen RADIUS-Server ist eine feste IP empfehlenswert –\nAccess Points und Switches brauchen eine gleichbleibende Adresse." 13 66 2 \
      dhcp "Automatisch (DHCP)" "$([ "$CT_IP" = dhcp ] && echo ON || echo OFF)" \
      static "Feste IP-Adresse" "$([ "$CT_IP" = dhcp ] && echo OFF || echo ON)" 3>&1 1>&2 2>&3)" || cancel
  if [ "$v" = "static" ]; then
    local ip="$CT_IP"; [ "$ip" = "dhcp" ] && ip=""
    while :; do
      ip="$(whiptail --backtitle "$WT_TITLE" --title "Feste IP" --inputbox "IP-Adresse mit Präfix, z. B. 192.168.1.20/24:" 9 58 "$ip" 3>&1 1>&2 2>&3)" || cancel
      [[ "$ip" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}/[0-9]{1,2}$ ]] && break
      whiptail --backtitle "$WT_TITLE" --title "Feste IP" --msgbox "Bitte im Format 192.168.1.20/24 eingeben." 8 50
    done
    CT_IP="$ip"
    CT_GW="$(whiptail --backtitle "$WT_TITLE" --title "Gateway" --inputbox "Gateway, z. B. ${CT_IP%.*}.1:" 9 58 "${CT_GW:-${CT_IP%.*}.1}" 3>&1 1>&2 2>&3)" || cancel
    CT_DNS="$(whiptail --backtitle "$WT_TITLE" --title "DNS" --inputbox "DNS-Server (leer = wie der Proxmox-Host):" 9 58 "$CT_DNS" 3>&1 1>&2 2>&3)" || cancel
  else
    CT_IP="dhcp"; CT_GW=""; CT_DNS=""
  fi

  if whiptail --backtitle "$WT_TITLE" --title "root-Passwort" --yesno "root-Passwort für den Container setzen?\n\n„Nein“: Die Proxmox-Konsole meldet automatisch als root an\n(Zugriff nur über Proxmox selbst)." 12 64 --defaultno; then
    local p1 p2
    while :; do
      p1="$(whiptail --backtitle "$WT_TITLE" --title "root-Passwort" --passwordbox "Passwort:" 9 58 3>&1 1>&2 2>&3)" || cancel
      p2="$(whiptail --backtitle "$WT_TITLE" --title "root-Passwort" --passwordbox "Passwort wiederholen:" 9 58 3>&1 1>&2 2>&3)" || cancel
      [ -n "$p1" ] && [ "$p1" = "$p2" ] && [ ${#p1} -ge 5 ] && break
      whiptail --backtitle "$WT_TITLE" --title "root-Passwort" --msgbox "Die Passwörter stimmen nicht überein oder sind zu kurz (mind. 5 Zeichen)." 8 60
    done
    CT_PASSWORD="$p1"
  else
    CT_PASSWORD=""
  fi

  if whiptail --backtitle "$WT_TITLE" --title "Autostart" --yesno "Container beim Start des Proxmox-Hosts automatisch starten?" 9 60; then CT_ONBOOT=1; else CT_ONBOOT=0; fi
}

if interactive; then
  while :; do
    MODE="$(whiptail --backtitle "$WT_TITLE" --title "$APP · Proxmox-LXC" --menu \
      "Willkommen! Dieses Skript legt einen Debian-Container an und richtet EasyRadius darin ein.\n\nStandard: $CT_CORES Kern · $CT_RAM MB RAM · $CT_DISK GB auf $CT_STORAGE · $CT_BRIDGE · DHCP" \
      16 72 3 \
      "1" "Standard-Installation (empfohlen)" \
      "2" "Erweiterte Einstellungen" \
      "3" "Beenden" 3>&1 1>&2 2>&3)" || cancel
    case "$MODE" in
      1) break ;;
      2) advanced
         whiptail --backtitle "$WT_TITLE" --title "Zusammenfassung" --yesno "Container mit diesen Einstellungen anlegen?\n\n$(summary)" 20 72 && break ;;
      *) cancel ;;
    esac
  done
fi

header
echo "${BD}${TAB}Einstellungen${CL}"
summary | sed "s/^/${TAB}${DIM}·${CL} /"
echo

# ================================================================ Prüfen der Eingaben
[[ "$CTID" =~ ^[0-9]+$ ]] && [ "$CTID" -ge 100 ] || die "Container-ID muss eine Zahl ab 100 sein."
if pct status "$CTID" >/dev/null 2>&1 || qm status "$CTID" >/dev/null 2>&1; then die "ID $CTID ist bereits vergeben."; fi
[[ "$CT_HOSTNAME" =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$ ]] || die "Ungültiger Hostname: $CT_HOSTNAME"
for n in CT_CORES CT_RAM CT_DISK; do [[ "${!n}" =~ ^[0-9]+$ ]] && [ "${!n}" -gt 0 ] || die "$n muss eine Zahl größer 0 sein."; done
[[ "$CT_SWAP" =~ ^[0-9]+$ ]] || die "CT_SWAP muss eine Zahl sein."
[ -z "$CT_VLAN" ] || [[ "$CT_VLAN" =~ ^[0-9]+$ && "$CT_VLAN" -ge 1 && "$CT_VLAN" -le 4094 ]] || die "VLAN muss zwischen 1 und 4094 liegen."
if [ "$CT_IP" != "dhcp" ] && [[ ! "$CT_IP" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}/[0-9]{1,2}$ ]]; then die "IP bitte als CIDR angeben, z. B. 192.168.1.20/24"; fi

# ================================================================ Vorlage
msg_info "Suche aktuelle Debian-Vorlage"
run pveam update || true
AVAIL="$(pveam available --section system 2>>"$LOG" | awk '{print $2}')"
TEMPLATE=""
for v in 13 12; do
  TEMPLATE="$(grep -E "^debian-$v-standard_.*_amd64" <<<"$AVAIL" | sort -V | tail -n1 || true)"
  [ -n "$TEMPLATE" ] && break
done
[ -n "$TEMPLATE" ] || die "Keine Debian-Vorlage gefunden (Internetzugang des Hosts?)."
msg_ok "Vorlage ${BL}$TEMPLATE${CL}"

if ! grep -q "$TEMPLATE" <<<"$(pveam list "$CT_TEMPLATE_STORAGE" 2>/dev/null)"; then
  msg_info "Lade Vorlage herunter (einmalig)"
  run pveam download "$CT_TEMPLATE_STORAGE" "$TEMPLATE"
  msg_ok "Vorlage geladen"
fi

# ================================================================ Container
msg_info "Lege Container $CTID an"
NET="name=eth0,bridge=$CT_BRIDGE,ip=$CT_IP"
[ -n "$CT_GW" ] && NET="$NET,gw=$CT_GW"
[ -n "$CT_VLAN" ] && NET="$NET,tag=$CT_VLAN"
ARGS=(
  --hostname "$CT_HOSTNAME" --cores "$CT_CORES" --memory "$CT_RAM" --swap "$CT_SWAP"
  --rootfs "$CT_STORAGE:$CT_DISK" --net0 "$NET" --ostype debian
  --unprivileged "$CT_UNPRIVILEGED" --features nesting=1 --onboot "$CT_ONBOOT" --tags "easyradius;radius"
  --description "<div align='center'><h2>EasyRadius</h2><p>RADIUS-Server mit Weboberfläche</p><p>Port 8088 · <a href='https://github.com/$REPO'>GitHub</a></p><p><i>Felix-Cloud</i></p></div>"
)
[ -n "$CT_DNS" ] && ARGS+=(--nameserver "$CT_DNS")
[ -n "$CT_PASSWORD" ] && ARGS+=(--password "$CT_PASSWORD")
run pct create "$CTID" "$CT_TEMPLATE_STORAGE:vztmpl/$TEMPLATE" "${ARGS[@]}"
CREATED=1
msg_ok "Container ${BL}$CTID${CL} (${CT_HOSTNAME}) angelegt"

msg_info "Starte Container"
run pct start "$CTID"
for _ in $(seq 1 60); do
  if pct exec "$CTID" -- getent hosts github.com >/dev/null 2>&1; then break; fi
  sleep 1
done
pct exec "$CTID" -- getent hosts github.com >/dev/null 2>&1 || die "Der Container erreicht das Internet nicht (IP, Gateway und DNS prüfen)."
msg_ok "Container läuft, Netzwerk bereit"

msg_info "Richte Debian ein"
TZ_HOST="$(timedatectl show -p Timezone --value 2>/dev/null || cat /etc/timezone 2>/dev/null || echo Etc/UTC)"
run pct exec "$CTID" -- ln -sf "/usr/share/zoneinfo/$TZ_HOST" /etc/localtime || true
if [ -z "$CT_PASSWORD" ]; then
  # Ohne Passwort: Proxmox-Konsole meldet automatisch als root an (wie bei den Proxmox-Helper-Skripten).
  run pct exec "$CTID" -- bash -c 'mkdir -p /etc/systemd/system/container-getty@1.service.d && printf "[Service]\nExecStart=\nExecStart=-/sbin/agetty --autologin root --noclear --keep-baud tty%%I 115200,38400,9600 \$TERM\n" > /etc/systemd/system/container-getty@1.service.d/override.conf && systemctl daemon-reload' || true
fi
msg_ok "Zeitzone $TZ_HOST übernommen"

msg_info "Aktualisiere Debian (kann einige Minuten dauern)"
run pct exec "$CTID" -- bash -c "export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get -y -qq -o Dpkg::Options::=--force-confold dist-upgrade && apt-get install -y -qq curl ca-certificates"
msg_ok "Debian ist aktuell"

msg_info "Installiere $APP${EASYRADIUS_VERSION:+ $EASYRADIUS_VERSION}"
run pct exec "$CTID" -- bash -c "curl -fsSL $RAW/linux/install.sh | bash -s -- ${EASYRADIUS_VERSION:+--version $EASYRADIUS_VERSION}"
VERSION="$(pct exec "$CTID" -- easyradius -version 2>/dev/null || echo "")"
msg_ok "$APP ${VERSION} läuft"

msg_info "Räume auf"
run pct exec "$CTID" -- bash -c "apt-get -y -qq autoremove && apt-get -y -qq clean" || true
msg_ok "Fertig"

# ================================================================ Abschluss
IP="$(pct exec "$CTID" -- hostname -I 2>/dev/null | awk '{print $1}')"
IP="${IP:-<IP>}"
echo
echo "${GN}${BD}${TAB}$APP ist eingerichtet!${CL}"
echo
echo "${TAB}${BD}Weboberfläche${CL}    ${BL}http://$IP:8088${CL}"
echo "${TAB}${BD}Windows-App${CL}      als Server ${BL}$IP:8088${CL} eintragen"
echo "${TAB}${BD}RADIUS-Server${CL}    $IP · UDP 1812 (Anmeldung), 1813 (Accounting)"
echo
echo "${TAB}${DIM}Konsole:        pct enter $CTID${CL}"
echo "${TAB}${DIM}Aktualisieren:  pct exec $CTID -- easyradius-update${CL}"
echo "${TAB}${DIM}Protokoll:      $LOG${CL}"
echo
if [ "$CT_IP" = "dhcp" ]; then
  echo "${YW}${TAB}Tipp:${CL} Gib dem Container eine feste IP oder eine DHCP-Reservierung – Access Points und"
  echo "${TAB}      Switches brauchen den RADIUS-Server immer unter derselben Adresse."
  echo
fi
echo "${DIM}${TAB}Felix-Cloud · $APP${CL}"
exit 0
