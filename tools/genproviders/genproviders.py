"""Erzeugt internal/letsencrypt/providers_gen.go aus den Anbieterbeschreibungen von lego.

Aufruf (im Projektordner):  python tools/genproviders/genproviders.py
"""
import glob
import json
import os
import re
import subprocess
import tomllib

# Ausgewählte Anbieter (ohne schwere Cloud-SDKs). Alles andere: CNAME-Delegation (acme-dns).
CODES = [
    "allinkl", "artfiles", "autodns", "bunny", "checkdomain", "cloudflare", "corenetworks", "cpanel", "ddnss",
    "desec", "digitalocean", "dnsimple", "dode", "duckdns", "dyndnsfree", "dynu", "gandiv5", "godaddy",
    "hetzner", "hostingde", "hostinger", "httpnet", "hurricane", "inwx", "ionos", "ipv64", "ispconfig", "joker",
    "keyhelp", "linode", "mittwald", "namecheap", "namedotcom", "netcup", "netlify", "ovh", "pdns", "plesk",
    "porkbun", "regfish", "rfc2136", "selfhostde", "servercow", "simply", "transip", "variomedia", "vercel",
    "vultr", "zoneee",
]

mod = subprocess.check_output(["go", "list", "-m", "-f", "{{.Dir}}", "github.com/go-acme/lego/v4"], text=True).strip()
entries = []
for code in CODES:
    d = os.path.join(mod, "providers", "dns", code)
    toml = glob.glob(os.path.join(d, "*.toml"))[0]
    meta = tomllib.load(open(toml, "rb"))
    gofile = os.path.join(d, code + ".go")
    pkg = re.search(r"^package (\w+)", open(gofile, encoding="utf-8").read(), re.M).group(1)
    creds = meta.get("Configuration", {}).get("Credentials", {})
    entries.append((code, pkg, meta["Name"], meta.get("URL", ""), creds))

out = ["// Code generiert von tools/genproviders – nicht von Hand bearbeiten.", "", "package letsencrypt", "", "import (",
       '\t"github.com/go-acme/lego/v4/challenge"']
for code, pkg, *_ in entries:
    out.append(f'\t{pkg} "github.com/go-acme/lego/v4/providers/dns/{code}"')
out += [")", "", "var providers = []Provider{"]
for code, pkg, name, url, creds in entries:
    fields = ", ".join("{Env: %s, Help: %s}" % (json.dumps(k), json.dumps(v, ensure_ascii=False)) for k, v in creds.items())
    out.append(f'\t{{Code: "{code}", Name: "{name}", URL: "{url}", Fields: []Field{{{fields}}},')
    out.append(f'\t\tnew: func() (challenge.Provider, error) {{ return {pkg}.NewDNSProvider() }}}},')
out.append("}")
open("internal/letsencrypt/providers_gen.go", "w", encoding="utf-8").write("\n".join(out) + "\n")
print(len(entries), "Anbieter")
