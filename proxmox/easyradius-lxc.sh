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
#            CT_BRIDGE, CT_VLAN, CT_IP (dhcp oder CIDR), CT_GW (Pflicht bei fester IP),
#            CT_DNS (IPv4, mehrere mit Komma oder Leerzeichen), CT_PASSWORD (mind. 5 Zeichen),
#            CT_UNPRIVILEGED (1/0), CT_ONBOOT (1/0), EASYRADIUS_VERSION (z. B. 1.6.0)
set -Eeuo pipefail

REPO="gottschalkfelix4-source/easyradius"
RAW_BASE="https://raw.githubusercontent.com/$REPO"
APP="EasyRadius"
WT_TITLE="Felix-Cloud · $APP"
LOG="/tmp/easyradius-lxc-$(date +%Y%m%d-%H%M%S).log"
(umask 077 && : >"$LOG")

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
MSG=""
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
msg_info() { stop_spinner; MSG="$1"; if [ -t 1 ]; then printf '\e[?25l'; spinner & SPINNER_PID=$!; else echo "${TAB}${INFO} $1"; fi; }
msg_ok()   { stop_spinner; echo "${TAB}${CM} $1"; }
msg_warn() { stop_spinner; echo "${TAB}${YW}!${CL} $1"; }
msg_err()  { stop_spinner; echo "${TAB}${CROSS} ${RD}$1${CL}"; }
run()      { "$@" >>"$LOG" 2>&1; }   # Befehle laufen leise, Ausgabe landet im Protokoll

interactive() { [ -z "${EASYRADIUS_YES:-}" ] && [ -t 0 ] && command -v whiptail >/dev/null 2>&1; }

# ================================================================ Fehler, Abbruch, Aufräumen
CTID="${CTID:-}"
CREATED=0    # 1 = Container wurde von diesem Lauf angelegt und ist noch nicht fertig eingerichtet
CREATING=0   # 1 = pct create wurde aufgerufen (die Konfiguration kann schon existieren)

ct_ours() { # gibt es einen (halb) angelegten Container aus diesem Lauf?
  [ "$CREATED" -eq 1 ] && return 0
  [ "$CREATING" -eq 1 ] && [ -n "$CTID" ] && [ -f "/etc/pve/lxc/$CTID.conf" ]
}

remove_ct() {
  msg_info "Entferne Container $CTID"
  pct unlock "$CTID" >/dev/null 2>&1 || true
  pct stop "$CTID" >/dev/null 2>&1 || true
  if pct destroy "$CTID" --purge >>"$LOG" 2>&1; then
    msg_ok "Container $CTID entfernt"
  else
    msg_err "Container $CTID ließ sich nicht entfernen – bitte von Hand: pct destroy $CTID --purge"
  fi
}

# Gemeinsamer Ausstieg für die(), Fehler (ERR) und Strg+C. $1 Exit-Code, $2 Meldung, $3 = "log": Protokollauszug zeigen
fail() {
  local code="$1" text="${2:-}" showlog="${3:-}"
  if [ "${BASH_SUBSHELL:-0}" -gt 0 ]; then
    [ -n "$text" ] && echo "$text" >&2
    exit "$code"
  fi
  trap - ERR
  trap 'exit 130' INT
  set +e
  stop_spinner
  echo
  [ -n "$text" ] && msg_err "$text"
  if [ "$showlog" = "log" ]; then
    echo "${TAB}${DIM}Protokoll: $LOG${CL}"
    echo "${DIM}$(tail -n 15 "$LOG" 2>/dev/null)${CL}"
  elif [ -s "$LOG" ]; then
    echo "${TAB}${DIM}Protokoll: $LOG${CL}"
  fi
  if ct_ours; then
    if interactive && whiptail --backtitle "$WT_TITLE" --title "$APP" --yesno "Die Installation wurde nicht abgeschlossen.\n\nContainer $CTID wieder entfernen?" 10 60; then
      remove_ct
    else
      echo "${TAB}${INFO} Container $CTID bleibt bestehen – entfernen mit: pct stop $CTID; pct destroy $CTID --purge"
    fi
  fi
  [ -t 1 ] && printf '\e[?25h'
  exit "$code"
}

on_error() {
  local code=$? line=${1:-?}
  [ "${BASH_SUBSHELL:-0}" -gt 0 ] && exit "$code"   # nur die Hauptshell meldet
  if [ "$code" -eq 99 ]; then cancel; fi             # Abbruch in einem Untermenü
  fail "$code" "Fehlgeschlagen: ${MSG:-Vorbereitung} (Code $code, Zeile $line)" log
}
trap 'on_error $LINENO' ERR
trap 'fail 130 "Abgebrochen (Strg+C)."' INT

die() { fail 1 "$1"; }

# ================================================================ Prüfungen
header
[ "$(id -u)" -eq 0 ] || die "Bitte als root auf dem Proxmox-Host ausführen."
command -v pct >/dev/null 2>&1 && command -v pveam >/dev/null 2>&1 && command -v pvesm >/dev/null 2>&1 \
  || die "Dieses Skript läuft nur auf einem Proxmox-VE-Host."
[ "$(uname -m)" = "x86_64" ] || die "Proxmox-LXC-Vorlagen gibt es hier nur für amd64."
if [ ! -t 0 ] && [ -z "${EASYRADIUS_YES:-}" ]; then
  msg_err "Keine Eingabe möglich – das Skript wurde per Pipe gestartet („curl … | bash“)."
  echo "${TAB}${INFO} So starten:       bash -c \"\$(curl -fsSL $RAW_BASE/main/proxmox/easyradius-lxc.sh)\""
  echo "${TAB}${INFO} Ohne Rückfragen:  EASYRADIUS_YES=1 setzen (Werte per Variablen, siehe Kopf des Skripts)."
  exit 1
fi

# ================================================================ Prüffunktionen
valid_ipv4() {
  local o parts=()
  [[ "$1" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || return 1
  IFS=. read -r -a parts <<<"$1"
  for o in "${parts[@]}"; do
    if [ "$((10#$o))" -gt 255 ]; then return 1; fi
  done
  return 0
}
valid_cidr() { # 192.168.1.20/24, Präfix 1–32
  [[ "$1" =~ ^([0-9.]+)/([0-9]{1,2})$ ]] || return 1
  local pfx="${BASH_REMATCH[2]}"
  valid_ipv4 "${BASH_REMATCH[1]}" && [ "$((10#$pfx))" -ge 1 ] && [ "$((10#$pfx))" -le 32 ]
}
norm_dns() { # Kommas und Leerzeichen → eine Liste mit einfachen Leerzeichen
  local list=()
  read -r -a list <<<"${1//,/ }"
  echo "${list[*]+"${list[*]}"}"
}
valid_dns() { # leer oder IPv4-Adressen, durch Leerzeichen getrennt
  local d list=()
  read -r -a list <<<"$1"
  for d in ${list[@]+"${list[@]}"}; do valid_ipv4 "$d" || return 1; done
  return 0
}
valid_hostname() { [[ "$1" =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$ ]]; }
ctid_check() { # gibt einen Fehlertext aus, wenn die ID nicht verwendbar ist
  if ! [[ "$1" =~ ^[0-9]{3,9}$ ]] || [ "$1" -lt 100 ]; then echo "Die Container-ID muss eine Zahl ab 100 sein."; return 0; fi
  if pct status "$1" >/dev/null 2>&1 || qm status "$1" >/dev/null 2>&1; then echo "ID $1 ist bereits vergeben."; return 0; fi
  # Prüft den ganzen Cluster (schlägt fehl, wenn die ID auf einem anderen Knoten benutzt wird).
  if command -v pvesh >/dev/null 2>&1 && ! pvesh get /cluster/nextid --vmid "$1" >/dev/null 2>&1; then
    echo "ID $1 ist im Cluster bereits vergeben."; return 0
  fi
  return 0
}

# ================================================================ Vorgaben
storages() { # $1 = Inhaltstyp → "Name|Typ|frei in GB" je Zeile
  pvesm status -content "$1" 2>/dev/null | awk 'NR>1 && $3=="active" {printf "%s|%s|%.1f\n", $1, $2, $6/1024/1024}' || true
}
pick_storage() { # bevorzugt $2, sonst der Speicher mit dem meisten Platz
  local list; list="$(storages "$1")"
  [ -n "$list" ] || return 0
  if grep -q "^$2|" <<<"$list"; then echo "$2"; else sort -t'|' -k3 -rn <<<"$list" | head -n1 | cut -d'|' -f1; fi
}
bridges() { # nur Proxmox-Bridges (vmbr*, SDN-vnet*), keine fwbr*, docker0, lxcbr0 …
  local node json names=""
  node="$(hostname -s 2>/dev/null || hostname)"
  if command -v pvesh >/dev/null 2>&1 \
     && json="$(pvesh get "/nodes/$node/network" --type any_bridge --output-format json 2>/dev/null)"; then
    names="$(grep -oE '"iface"[[:space:]]*:[[:space:]]*"[^"]+"' <<<"$json" | sed -E 's/.*"([^"]+)"$/\1/' || true)"
  fi
  if [ -z "$names" ]; then
    names="$(ip -o link show type bridge 2>/dev/null | awk -F': ' '{print $2}' | cut -d'@' -f1 || true)"
  fi
  grep -E '^(vmbr|vnet)' <<<"$names" | sort -u -V || true
}
BRIDGES="$(bridges)"

CTID="${CTID:-$(pvesh get /cluster/nextid 2>/dev/null || echo 100)}"
CT_HOSTNAME="${CT_HOSTNAME:-easyradius}"
CT_CORES="${CT_CORES:-1}"
CT_RAM="${CT_RAM:-512}"
CT_SWAP="${CT_SWAP:-512}"
CT_DISK="${CT_DISK:-4}"
CT_STORAGE="${CT_STORAGE:-$(pick_storage rootdir local-lvm)}"
CT_TEMPLATE_STORAGE="${CT_TEMPLATE_STORAGE:-$(pick_storage vztmpl local)}"
CT_BRIDGE="${CT_BRIDGE:-$(grep -x vmbr0 <<<"$BRIDGES" || head -n1 <<<"$BRIDGES" || true)}"
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

# install.sh aus dem passenden Tag laden, wenn eine Version vorgegeben ist.
INSTALL_REF="main"
if [ -n "$EASYRADIUS_VERSION" ]; then
  [[ "$EASYRADIUS_VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "EASYRADIUS_VERSION ungültig: $EASYRADIUS_VERSION (erwartet z. B. 1.6.0)"
  EASYRADIUS_VERSION="${EASYRADIUS_VERSION#v}"
  INSTALL_REF="v$EASYRADIUS_VERSION"
fi
INSTALL_URL="$RAW_BASE/$INSTALL_REF/linux/install.sh"

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
$APP     ${EASYRADIUS_VERSION:-neueste Version}
EOF
}

# ================================================================ Menüs
cancel() { # in Untermenüs (Subshell) mit Code 99 enden, damit die Hauptshell sauber abbricht
  [ "${BASH_SUBSHELL:-0}" -gt 0 ] && exit 99
  stop_spinner; echo; echo "${TAB}${INFO} Abgebrochen – es wurde nichts verändert."; exit 0
}

# Hinweisfenster. Läuft oft innerhalb von $( ): Anzeige auf stderr, ESC darf keinen Fehler auslösen.
note() { whiptail --backtitle "$WT_TITLE" --title "$1" --msgbox "$2" "${3:-8}" "${4:-60}" >&2 || true; }

# Auswahl mit Vorgaben und „Eigener Wert“. $1 Titel $2 Text $3 aktueller Wert $4 Einheit $5.. Vorgaben
choose_number() {
  local title="$1" text="$2" cur="$3" unit="$4"; shift 4
  local args=() found=0 v
  for v in "$@"; do
    if [ "$v" = "$cur" ]; then args+=("$v" "$unit" ON); found=1; else args+=("$v" "$unit" OFF); fi
  done
  if [ "$found" -eq 0 ]; then args+=("eigen" "Eigener Wert ($cur $unit)" ON); else args+=("eigen" "Eigener Wert …" OFF); fi
  local pick
  while :; do
    pick="$(whiptail --backtitle "$WT_TITLE" --title "$title" --radiolist "$text" 16 60 7 "${args[@]}" 3>&1 1>&2 2>&3)" || cancel
    [ -n "$pick" ] && break
    note "$title" "Bitte einen Eintrag mit der Leertaste auswählen."
  done
  if [ "$pick" = "eigen" ]; then
    while :; do
      pick="$(whiptail --backtitle "$WT_TITLE" --title "$title" --inputbox "$text\n\nWert in $unit:" 11 60 "$cur" 3>&1 1>&2 2>&3)" || cancel
      [[ "$pick" =~ ^[0-9]+$ ]] && [ "$pick" -gt 0 ] && break
      note "$title" "Bitte eine ganze Zahl größer als 0 eingeben." 8 50
    done
  fi
  echo "$pick"
}

# Radioliste, die immer einen Eintrag vorwählt. $1 Titel $2 Text $3 aktueller Wert, dann Paare: Name Beschreibung
radio_pick() {
  local title="$1" text="$2" cur="$3"; shift 3
  local -a all=("$@")
  local args=() sel="" pick
  while [ $# -ge 2 ]; do
    if [ "$1" = "$cur" ]; then sel="$cur"; fi
    shift 2
  done
  [ -n "$sel" ] || sel="${all[0]}"   # aktueller Wert nicht in der Liste → ersten Eintrag vorwählen
  set -- "${all[@]}"
  while [ $# -ge 2 ]; do
    if [ "$1" = "$sel" ]; then args+=("$1" "$2" ON); else args+=("$1" "$2" OFF); fi
    shift 2
  done
  while :; do
    pick="$(whiptail --backtitle "$WT_TITLE" --title "$title" --radiolist "$text" 16 68 6 "${args[@]}" 3>&1 1>&2 2>&3)" || cancel
    [ -n "$pick" ] && break
    note "$title" "Bitte einen Eintrag mit der Leertaste auswählen."
  done
  echo "$pick"
}

choose_storage() { # $1 Inhaltstyp $2 aktueller Wert $3 Titel
  local pairs=() name typ free
  while IFS='|' read -r name typ free; do
    [ -n "$name" ] || continue
    pairs+=("$name" "$(printf '%-10s %8s GB frei' "$typ" "$free")")
  done < <(storages "$1")
  if [ ${#pairs[@]} -eq 0 ]; then echo "$2"; return 0; fi
  if [ ${#pairs[@]} -eq 2 ]; then echo "${pairs[0]}"; return 0; fi
  radio_pick "$3" "Wo soll gespeichert werden?" "$2" "${pairs[@]}"
}

choose_bridge() { # $1 aktueller Wert
  local pairs=() b v
  while read -r b; do
    if [ -n "$b" ]; then pairs+=("$b" "Netzwerk-Bridge"); fi
  done <<<"$BRIDGES"
  if [ ${#pairs[@]} -gt 2 ]; then
    radio_pick "Netzwerk" "An welcher Bridge hängt der Container?" "$1" "${pairs[@]}"
    return 0
  fi
  v="${1:-${pairs[0]:-vmbr0}}"
  while :; do
    v="$(whiptail --backtitle "$WT_TITLE" --title "Netzwerk" --inputbox "Bridge, z. B. vmbr0:" 9 58 "$v" 3>&1 1>&2 2>&3)" || cancel
    [ -n "$v" ] && break
    note "Netzwerk" "Bitte den Namen der Bridge angeben, z. B. vmbr0."
  done
  echo "$v"
}

advanced() {
  local v err
  while :; do
    v="$(whiptail --backtitle "$WT_TITLE" --title "Container-ID" --inputbox "Freie ID für den Container (ab 100):" 9 58 "$CTID" 3>&1 1>&2 2>&3)" || cancel
    err="$(ctid_check "$v")"
    [ -z "$err" ] && break
    note "Container-ID" "$err"
  done
  CTID="$v"
  while :; do
    v="$(whiptail --backtitle "$WT_TITLE" --title "Name" --inputbox "Hostname des Containers:" 9 58 "$CT_HOSTNAME" 3>&1 1>&2 2>&3)" || cancel
    valid_hostname "$v" && break
    note "Name" "Nur Buchstaben, Ziffern und Bindestriche (nicht am Anfang oder Ende)." 8 64
  done
  CT_HOSTNAME="$v"
  v="$(whiptail --backtitle "$WT_TITLE" --title "Container-Typ" --radiolist "Unprivilegiert ist sicherer und für EasyRadius ausreichend." 12 64 2 \
      1 "Unprivilegiert (empfohlen)" "$([ "$CT_UNPRIVILEGED" = 1 ] && echo ON || echo OFF)" \
      0 "Privilegiert" "$([ "$CT_UNPRIVILEGED" = 1 ] && echo OFF || echo ON)" 3>&1 1>&2 2>&3)" || cancel
  CT_UNPRIVILEGED="${v:-1}"

  CT_CORES="$(choose_number "CPU-Kerne" "Wie viele CPU-Kerne bekommt der Container?\nEasyRadius kommt mit einem Kern gut aus." "$CT_CORES" "Kern(e)" 1 2 4)"
  CT_RAM="$(choose_number "Arbeitsspeicher" "Wie viel RAM bekommt der Container?\n512 MB reichen für Hunderte Benutzer." "$CT_RAM" "MB" 512 1024 2048 4096)"
  CT_SWAP="$(choose_number "Swap" "Auslagerungsspeicher:" "$CT_SWAP" "MB" 0 512 1024)"
  CT_DISK="$(choose_number "Festplatte" "Größe der Festplatte. Programm und Daten brauchen weniger als 1 GB." "$CT_DISK" "GB" 4 8 16 32)"
  CT_STORAGE="$(choose_storage rootdir "$CT_STORAGE" "Speicher für den Container")"
  CT_BRIDGE="$(choose_bridge "$CT_BRIDGE")"

  while :; do
    v="$(whiptail --backtitle "$WT_TITLE" --title "VLAN" --inputbox "VLAN-Tag (leer = kein VLAN):" 9 58 "$CT_VLAN" 3>&1 1>&2 2>&3)" || cancel
    if [ -z "$v" ] || { [[ "$v" =~ ^[0-9]{1,4}$ ]] && [ "$v" -ge 1 ] && [ "$v" -le 4094 ]; }; then break; fi
    note "VLAN" "Der VLAN-Tag muss zwischen 1 und 4094 liegen (oder leer bleiben)."
  done
  CT_VLAN="$v"

  v="$(whiptail --backtitle "$WT_TITLE" --title "IP-Adresse" --radiolist "Für einen RADIUS-Server ist eine feste IP empfehlenswert –\nAccess Points und Switches brauchen eine gleichbleibende Adresse." 13 66 2 \
      dhcp "Automatisch (DHCP)" "$([ "$CT_IP" = dhcp ] && echo ON || echo OFF)" \
      static "Feste IP-Adresse" "$([ "$CT_IP" = dhcp ] && echo OFF || echo ON)" 3>&1 1>&2 2>&3)" || cancel
  if [ "$v" = "static" ]; then
    local ip="$CT_IP" gw dns
    if [ "$ip" = "dhcp" ]; then ip=""; fi
    while :; do
      ip="$(whiptail --backtitle "$WT_TITLE" --title "Feste IP" --inputbox "IP-Adresse mit Präfix, z. B. 192.168.1.20/24:" 9 58 "$ip" 3>&1 1>&2 2>&3)" || cancel
      valid_cidr "$ip" && break
      note "Feste IP" "Bitte im Format 192.168.1.20/24 eingeben\n(Zahlen bis 255, Präfix 1–32)." 9 56
    done
    CT_IP="$ip"
    gw="${CT_GW:-${CT_IP%.*}.1}"
    while :; do
      gw="$(whiptail --backtitle "$WT_TITLE" --title "Gateway" --inputbox "Gateway (bei fester IP erforderlich), z. B. ${CT_IP%.*}.1:" 9 64 "$gw" 3>&1 1>&2 2>&3)" || cancel
      valid_ipv4 "$gw" && break
      note "Gateway" "Bitte die IPv4-Adresse des Gateways angeben, z. B. ${CT_IP%.*}.1." 8 64
    done
    CT_GW="$gw"
    dns="$CT_DNS"
    while :; do
      dns="$(whiptail --backtitle "$WT_TITLE" --title "DNS" --inputbox "DNS-Server (leer = wie der Proxmox-Host).\nMehrere mit Komma oder Leerzeichen trennen:" 10 64 "$dns" 3>&1 1>&2 2>&3)" || cancel
      dns="$(norm_dns "$dns")"
      valid_dns "$dns" && break
      note "DNS" "Bitte IPv4-Adressen angeben, z. B. 192.168.1.1, 9.9.9.9" 8 64
    done
    CT_DNS="$dns"
  else
    CT_IP="dhcp"; CT_GW=""; CT_DNS=""
  fi

  if whiptail --backtitle "$WT_TITLE" --title "root-Passwort" --yesno "root-Passwort für den Container setzen?\n\n„Nein“: Die Proxmox-Konsole meldet automatisch als root an\n(Zugriff nur über Proxmox selbst)." 12 64 --defaultno; then
    local p1 p2
    while :; do
      p1="$(whiptail --backtitle "$WT_TITLE" --title "root-Passwort" --passwordbox "Passwort:" 9 58 3>&1 1>&2 2>&3)" || cancel
      p2="$(whiptail --backtitle "$WT_TITLE" --title "root-Passwort" --passwordbox "Passwort wiederholen:" 9 58 3>&1 1>&2 2>&3)" || cancel
      [ -n "$p1" ] && [ "$p1" = "$p2" ] && [ ${#p1} -ge 5 ] && break
      note "root-Passwort" "Die Passwörter stimmen nicht überein oder sind zu kurz (mind. 5 Zeichen)."
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
         whiptail --backtitle "$WT_TITLE" --title "Zusammenfassung" --yesno "Container mit diesen Einstellungen anlegen?\n\n$(summary)" 21 72 && break ;;
      *) cancel ;;
    esac
  done
fi

# ================================================================ Prüfen der Eingaben
ERR_TEXT="$(ctid_check "$CTID")"
[ -z "$ERR_TEXT" ] || die "$ERR_TEXT"
valid_hostname "$CT_HOSTNAME" || die "Ungültiger Hostname: $CT_HOSTNAME"
for n in CT_CORES CT_RAM CT_DISK; do [[ "${!n}" =~ ^[0-9]+$ ]] && [ "${!n}" -gt 0 ] || die "$n muss eine Zahl größer 0 sein."; done
[[ "$CT_SWAP" =~ ^[0-9]+$ ]] || die "CT_SWAP muss eine Zahl sein."
[[ "$CT_UNPRIVILEGED" =~ ^[01]$ ]] || die "CT_UNPRIVILEGED muss 1 oder 0 sein."
[[ "$CT_ONBOOT" =~ ^[01]$ ]] || die "CT_ONBOOT muss 1 oder 0 sein."
[ -z "$CT_VLAN" ] || [[ "$CT_VLAN" =~ ^[0-9]{1,4}$ && "$CT_VLAN" -ge 1 && "$CT_VLAN" -le 4094 ]] || die "VLAN muss zwischen 1 und 4094 liegen."
if [ "${CT_IP,,}" = "dhcp" ]; then
  CT_IP="dhcp"; CT_GW=""   # Gateway kommt bei DHCP vom DHCP-Server
else
  valid_cidr "$CT_IP" || die "IP bitte als CIDR angeben, z. B. 192.168.1.20/24 (Zahlen bis 255, Präfix 1–32)."
  [ -n "$CT_GW" ] || die "Bei fester IP wird ein Gateway benötigt (CT_GW), z. B. CT_GW=${CT_IP%.*}.1"
  valid_ipv4 "$CT_GW" || die "Ungültiges Gateway: $CT_GW"
fi
CT_DNS="$(norm_dns "$CT_DNS")"
valid_dns "$CT_DNS" || die "Ungültige DNS-Server: $CT_DNS (IPv4-Adressen, getrennt durch Komma oder Leerzeichen)"
[ -z "$CT_PASSWORD" ] || [ ${#CT_PASSWORD} -ge 5 ] || die "CT_PASSWORD muss mindestens 5 Zeichen lang sein."

header
echo "${BD}${TAB}Einstellungen${CL}"
summary | sed "s/^/${TAB}${DIM}·${CL} /"
echo

# ================================================================ Vorlage
msg_info "Suche aktuelle Debian-Vorlage"
run pveam update || true
if ! AVAIL="$(pveam available --section system 2>>"$LOG" | awk '{print $2}')"; then
  die "Keine Debian-Vorlage gefunden (Internetzugang des Hosts?)."
fi
latest_template() { grep -E "^debian-$1-standard_.*_amd64" <<<"$AVAIL" | sort -V | tail -n1 || true; }
TEMPLATE=""
for v in 13 12; do
  TEMPLATE="$(latest_template "$v")"
  if [ -n "$TEMPLATE" ]; then break; fi
done
[ -n "$TEMPLATE" ] || die "Keine Debian-Vorlage gefunden (Internetzugang des Hosts?)."
msg_ok "Vorlage ${BL}$TEMPLATE${CL}"

ensure_template() { # $1 Vorlage – bei Bedarf herunterladen
  local have
  have="$(pveam list "$CT_TEMPLATE_STORAGE" 2>/dev/null || true)"
  if ! grep -qF "$1" <<<"$have"; then
    msg_info "Lade Vorlage $1 herunter (einmalig)"
    run pveam download "$CT_TEMPLATE_STORAGE" "$1"
    msg_ok "Vorlage geladen"
  fi
}
ensure_template "$TEMPLATE"

# ================================================================ Container
NET="name=eth0,bridge=$CT_BRIDGE,ip=$CT_IP"
[ -n "$CT_GW" ] && NET="$NET,gw=$CT_GW"
[ -n "$CT_VLAN" ] && NET="$NET,tag=$CT_VLAN"
ARGS=(
  --hostname "$CT_HOSTNAME" --cores "$CT_CORES" --memory "$CT_RAM" --swap "$CT_SWAP"
  --rootfs "$CT_STORAGE:$CT_DISK" --net0 "$NET" --ostype debian
  --unprivileged "$CT_UNPRIVILEGED" --onboot "$CT_ONBOOT" --tags "easyradius;radius"
  --description "<div align='center'><h2>EasyRadius</h2><p>RADIUS-Server mit Weboberfläche</p><p>HTTPS 8443 · HTTP 8088 · <a href='https://github.com/$REPO'>GitHub</a></p><p><i>Felix-Cloud</i></p></div>"
)
# nesting=1 nur für unprivilegierte Container (erlaubt systemd die Sandbox des Dienstes).
if [ "$CT_UNPRIVILEGED" = "1" ]; then ARGS+=(--features nesting=1); fi
if [ -n "$CT_DNS" ]; then ARGS+=(--nameserver "$CT_DNS"); fi
# Das root-Passwort wird nicht per --password übergeben (wäre in der Prozessliste sichtbar), sondern nach dem Start gesetzt.

TZ_OPT=1          # 1 = --timezone host verwenden (ältere Proxmox-Versionen kennen das nicht)
CREATE_OUT=""
try_create() { # $1 Vorlage; Ausgabe landet in CREATE_OUT und im Protokoll
  local extra=()
  if [ "$TZ_OPT" -eq 1 ]; then extra=(--timezone host); fi
  CREATING=1
  if CREATE_OUT="$(pct create "$CTID" "$CT_TEMPLATE_STORAGE:vztmpl/$1" "${ARGS[@]}" ${extra[@]+"${extra[@]}"} 2>&1)"; then
    printf '%s\n' "$CREATE_OUT" >>"$LOG"
    return 0
  fi
  printf '%s\n' "$CREATE_OUT" >>"$LOG"
  return 1
}
remove_partial() { # Reste eines fehlgeschlagenen pct create (die ID war vorher frei)
  if [ -f "/etc/pve/lxc/$CTID.conf" ]; then
    pct unlock "$CTID" >/dev/null 2>&1 || true
    pct destroy "$CTID" --purge >>"$LOG" 2>&1 || true
  fi
}

msg_info "Lege Container $CTID an"
while ! try_create "$TEMPLATE"; do
  remove_partial
  if [ "$TZ_OPT" -eq 1 ] && grep -qi 'timezone' <<<"$CREATE_OUT" \
     && grep -qiE 'unknown option|not defined|unable to parse|invalid' <<<"$CREATE_OUT"; then
    TZ_OPT=0
    echo "--timezone wird nicht unterstützt – die Zeitzone wird nach dem Start gesetzt." >>"$LOG"
    continue
  fi
  if [[ "$TEMPLATE" == debian-13-* ]] && grep -qi 'unsupported debian version' <<<"$CREATE_OUT"; then
    FALLBACK="$(latest_template 12)"
    [ -n "$FALLBACK" ] || die "Diese Proxmox-Version unterstützt Debian 13 noch nicht, und es gibt keine Debian-12-Vorlage."
    msg_warn "Diese Proxmox-Version unterstützt Debian 13 noch nicht – nehme stattdessen ${BL}$FALLBACK${CL}"
    TEMPLATE="$FALLBACK"
    ensure_template "$TEMPLATE"
    msg_info "Lege Container $CTID an"
    continue
  fi
  die "Container konnte nicht angelegt werden: $(grep -v '^[[:space:]]*$' <<<"$CREATE_OUT" | tail -n1 || true)"
done
CREATED=1
msg_ok "Container ${BL}$CTID${CL} (${CT_HOSTNAME}) angelegt"

msg_info "Starte Container"
run pct start "$CTID"
for _ in $(seq 1 60); do
  if pct exec "$CTID" -- getent hosts github.com >/dev/null 2>&1; then break; fi
  sleep 1
done
pct exec "$CTID" -- getent hosts github.com >/dev/null 2>&1 \
  || die "Der Container kann keine Namen auflösen – IP, Gateway und DNS prüfen (Bridge $CT_BRIDGE${CT_VLAN:+, VLAN $CT_VLAN})."
NET_OK=0
for _ in 1 2 3 4 5; do
  if pct exec "$CTID" -- timeout 5 bash -c '</dev/tcp/github.com/443' >/dev/null 2>&1; then NET_OK=1; break; fi
  sleep 2
done
[ "$NET_OK" -eq 1 ] || die "Der Container erreicht github.com nicht über HTTPS (Port 443) – Gateway, Firewall oder Proxy prüfen."
msg_ok "Container läuft, Netzwerk bereit"

msg_info "Richte Debian ein"
if [ -n "$CT_PASSWORD" ]; then
  # printf ist eingebaut: Das Passwort taucht in keiner Prozessliste auf.
  printf 'root:%s\n' "$CT_PASSWORD" | pct exec "$CTID" -- chpasswd >>"$LOG" 2>&1 || die "Das root-Passwort konnte nicht gesetzt werden."
else
  # Ohne Passwort: Proxmox-Konsole meldet automatisch als root an (wie bei den Proxmox-Helper-Skripten).
  run pct exec "$CTID" -- bash -c 'mkdir -p /etc/systemd/system/container-getty@1.service.d && printf "[Service]\nExecStart=\nExecStart=-/sbin/agetty --autologin root --noclear --keep-baud tty%%I 115200,38400,9600 \$TERM\n" > /etc/systemd/system/container-getty@1.service.d/override.conf && systemctl daemon-reload && { systemctl restart container-getty@1.service || true; }' || true
fi
if [ "$TZ_OPT" -eq 0 ]; then
  TZ_HOST="$(timedatectl show -p Timezone --value 2>/dev/null || cat /etc/timezone 2>/dev/null || echo Etc/UTC)"
  run pct exec "$CTID" -- ln -sf "/usr/share/zoneinfo/$TZ_HOST" /etc/localtime || true
fi
msg_ok "Zeitzone vom Host übernommen, root-Zugang eingerichtet"

msg_info "Aktualisiere Debian (kann einige Minuten dauern)"
run pct exec "$CTID" -- bash -c "export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get -y -qq -o Dpkg::Options::=--force-confold dist-upgrade && apt-get install -y -qq curl ca-certificates"
run pct exec "$CTID" -- curl -fsS --max-time 20 -o /dev/null https://github.com \
  || die "Der Container erreicht github.com nicht per HTTPS (Proxy, Firewall oder Uhrzeit prüfen)."
msg_ok "Debian ist aktuell"

msg_info "Installiere $APP${EASYRADIUS_VERSION:+ $EASYRADIUS_VERSION}"
INST_ARGS=()
if [ -n "$EASYRADIUS_VERSION" ]; then INST_ARGS=(--version "$EASYRADIUS_VERSION"); fi
# Erst vollständig herunterladen, dann ausführen (kein „curl | bash“ ohne pipefail).
run pct exec "$CTID" -- bash -c 'curl -fsSL -o /tmp/easyradius-install.sh "$1" || exit 1; shift; bash /tmp/easyradius-install.sh "$@"; rc=$?; rm -f /tmp/easyradius-install.sh; exit $rc' \
  easyradius-install "$INSTALL_URL" ${INST_ARGS[@]+"${INST_ARGS[@]}"}
# Ab hier läuft der Container: spätere, harmlose Fehler bieten kein Löschen mehr an.
CREATED=0; CREATING=0
VERSION="$(pct exec "$CTID" -- easyradius -version 2>/dev/null || echo "")"
msg_ok "$APP ${VERSION} läuft"

msg_info "Räume auf"
run pct exec "$CTID" -- bash -c "apt-get -y -qq autoremove && apt-get -y -qq clean" || true
msg_ok "Fertig"

# ================================================================ Abschluss
IP="$(pct exec "$CTID" -- hostname -I 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]{1,3}(\.[0-9]{1,3}){3}$' | head -n1 || true)"
if [ -z "$IP" ] && [ "$CT_IP" != "dhcp" ]; then IP="${CT_IP%/*}"; fi
IP="${IP:-<IP>}"
echo
echo "${GN}${BD}${TAB}$APP ist eingerichtet!${CL}"
echo
echo "${TAB}${BD}Weboberfläche${CL}    ${BL}https://$IP:8443${CL}  ${DIM}(empfohlen – Zertifikat beim ersten Aufruf einmal bestätigen)${CL}"
echo "${TAB}                 ${BL}http://$IP:8088${CL}"
echo "${TAB}${BD}Windows-App${CL}      als Server ${BL}$IP${CL} eintragen (verbindet per HTTPS)"
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
