// Code generiert von tools/genproviders – nicht von Hand bearbeiten.

package letsencrypt

import (
	"github.com/go-acme/lego/v4/challenge"
	allinkl "github.com/go-acme/lego/v4/providers/dns/allinkl"
	artfiles "github.com/go-acme/lego/v4/providers/dns/artfiles"
	autodns "github.com/go-acme/lego/v4/providers/dns/autodns"
	bunny "github.com/go-acme/lego/v4/providers/dns/bunny"
	checkdomain "github.com/go-acme/lego/v4/providers/dns/checkdomain"
	cloudflare "github.com/go-acme/lego/v4/providers/dns/cloudflare"
	corenetworks "github.com/go-acme/lego/v4/providers/dns/corenetworks"
	cpanel "github.com/go-acme/lego/v4/providers/dns/cpanel"
	ddnss "github.com/go-acme/lego/v4/providers/dns/ddnss"
	desec "github.com/go-acme/lego/v4/providers/dns/desec"
	digitalocean "github.com/go-acme/lego/v4/providers/dns/digitalocean"
	dnsimple "github.com/go-acme/lego/v4/providers/dns/dnsimple"
	dode "github.com/go-acme/lego/v4/providers/dns/dode"
	duckdns "github.com/go-acme/lego/v4/providers/dns/duckdns"
	dyndnsfree "github.com/go-acme/lego/v4/providers/dns/dyndnsfree"
	dynu "github.com/go-acme/lego/v4/providers/dns/dynu"
	gandiv5 "github.com/go-acme/lego/v4/providers/dns/gandiv5"
	godaddy "github.com/go-acme/lego/v4/providers/dns/godaddy"
	hetzner "github.com/go-acme/lego/v4/providers/dns/hetzner"
	hostingde "github.com/go-acme/lego/v4/providers/dns/hostingde"
	hostinger "github.com/go-acme/lego/v4/providers/dns/hostinger"
	httpnet "github.com/go-acme/lego/v4/providers/dns/httpnet"
	hurricane "github.com/go-acme/lego/v4/providers/dns/hurricane"
	inwx "github.com/go-acme/lego/v4/providers/dns/inwx"
	ionos "github.com/go-acme/lego/v4/providers/dns/ionos"
	ipv64 "github.com/go-acme/lego/v4/providers/dns/ipv64"
	ispconfig "github.com/go-acme/lego/v4/providers/dns/ispconfig"
	joker "github.com/go-acme/lego/v4/providers/dns/joker"
	keyhelp "github.com/go-acme/lego/v4/providers/dns/keyhelp"
	linode "github.com/go-acme/lego/v4/providers/dns/linode"
	mittwald "github.com/go-acme/lego/v4/providers/dns/mittwald"
	namecheap "github.com/go-acme/lego/v4/providers/dns/namecheap"
	namedotcom "github.com/go-acme/lego/v4/providers/dns/namedotcom"
	netcup "github.com/go-acme/lego/v4/providers/dns/netcup"
	netlify "github.com/go-acme/lego/v4/providers/dns/netlify"
	ovh "github.com/go-acme/lego/v4/providers/dns/ovh"
	pdns "github.com/go-acme/lego/v4/providers/dns/pdns"
	plesk "github.com/go-acme/lego/v4/providers/dns/plesk"
	porkbun "github.com/go-acme/lego/v4/providers/dns/porkbun"
	regfish "github.com/go-acme/lego/v4/providers/dns/regfish"
	rfc2136 "github.com/go-acme/lego/v4/providers/dns/rfc2136"
	selfhostde "github.com/go-acme/lego/v4/providers/dns/selfhostde"
	servercow "github.com/go-acme/lego/v4/providers/dns/servercow"
	simply "github.com/go-acme/lego/v4/providers/dns/simply"
	transip "github.com/go-acme/lego/v4/providers/dns/transip"
	variomedia "github.com/go-acme/lego/v4/providers/dns/variomedia"
	vercel "github.com/go-acme/lego/v4/providers/dns/vercel"
	vultr "github.com/go-acme/lego/v4/providers/dns/vultr"
	zoneee "github.com/go-acme/lego/v4/providers/dns/zoneee"
)

var providers = []Provider{
	{Code: "allinkl", Name: "all-inkl", URL: "https://all-inkl.com", Fields: []Field{{Env: "ALL_INKL_LOGIN", Help: "KAS login"}, {Env: "ALL_INKL_PASSWORD", Help: "KAS password"}},
		new: func() (challenge.Provider, error) { return allinkl.NewDNSProvider() }},
	{Code: "artfiles", Name: "ArtFiles", URL: "https://www.artfiles.de/extras/domains/", Fields: []Field{{Env: "ARTFILES_USERNAME", Help: "API username"}, {Env: "ARTFILES_PASSWORD", Help: "API password"}},
		new: func() (challenge.Provider, error) { return artfiles.NewDNSProvider() }},
	{Code: "autodns", Name: "Autodns", URL: "https://www.internetx.com/domains/autodns/", Fields: []Field{{Env: "AUTODNS_API_USER", Help: "Username"}, {Env: "AUTODNS_API_PASSWORD", Help: "User Password"}},
		new: func() (challenge.Provider, error) { return autodns.NewDNSProvider() }},
	{Code: "bunny", Name: "Bunny", URL: "https://bunny.net", Fields: []Field{{Env: "BUNNY_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return bunny.NewDNSProvider() }},
	{Code: "checkdomain", Name: "Checkdomain", URL: "https://checkdomain.de/", Fields: []Field{{Env: "CHECKDOMAIN_TOKEN", Help: "API token"}},
		new: func() (challenge.Provider, error) { return checkdomain.NewDNSProvider() }},
	{Code: "cloudflare", Name: "Cloudflare", URL: "https://www.cloudflare.com/dns/", Fields: []Field{{Env: "CF_API_EMAIL", Help: "Account email"}, {Env: "CF_API_KEY", Help: "API key"}, {Env: "CF_DNS_API_TOKEN", Help: "API token with DNS:Edit permission (since v3.1.0)"}, {Env: "CF_ZONE_API_TOKEN", Help: "API token with Zone:Read permission (since v3.1.0)"}, {Env: "CLOUDFLARE_EMAIL", Help: "Alias to CF_API_EMAIL"}, {Env: "CLOUDFLARE_API_KEY", Help: "Alias to CF_API_KEY"}, {Env: "CLOUDFLARE_DNS_API_TOKEN", Help: "Alias to CF_DNS_API_TOKEN"}, {Env: "CLOUDFLARE_ZONE_API_TOKEN", Help: "Alias to CF_ZONE_API_TOKEN"}},
		new: func() (challenge.Provider, error) { return cloudflare.NewDNSProvider() }},
	{Code: "corenetworks", Name: "Core-Networks", URL: "https://www.core-networks.de/", Fields: []Field{{Env: "CORENETWORKS_LOGIN", Help: "The username of the API account"}, {Env: "CORENETWORKS_PASSWORD", Help: "The password"}},
		new: func() (challenge.Provider, error) { return corenetworks.NewDNSProvider() }},
	{Code: "cpanel", Name: "CPanel/WHM", URL: "https://cpanel.net/", Fields: []Field{{Env: "CPANEL_USERNAME", Help: "username"}, {Env: "CPANEL_TOKEN", Help: "API token"}, {Env: "CPANEL_BASE_URL", Help: "API server URL"}},
		new: func() (challenge.Provider, error) { return cpanel.NewDNSProvider() }},
	{Code: "ddnss", Name: "DDnss (DynDNS Service)", URL: "https://ddnss.de/", Fields: []Field{{Env: "DDNSS_KEY", Help: "Update key"}},
		new: func() (challenge.Provider, error) { return ddnss.NewDNSProvider() }},
	{Code: "desec", Name: "deSEC.io", URL: "https://desec.io", Fields: []Field{{Env: "DESEC_TOKEN", Help: "Domain token"}},
		new: func() (challenge.Provider, error) { return desec.NewDNSProvider() }},
	{Code: "digitalocean", Name: "Digital Ocean", URL: "https://www.digitalocean.com/docs/networking/dns/", Fields: []Field{{Env: "DO_AUTH_TOKEN", Help: "Authentication token"}},
		new: func() (challenge.Provider, error) { return digitalocean.NewDNSProvider() }},
	{Code: "dnsimple", Name: "DNSimple", URL: "https://dnsimple.com/", Fields: []Field{{Env: "DNSIMPLE_OAUTH_TOKEN", Help: "OAuth token"}},
		new: func() (challenge.Provider, error) { return dnsimple.NewDNSProvider() }},
	{Code: "dode", Name: "Domain Offensive (do.de)", URL: "https://www.do.de/", Fields: []Field{{Env: "DODE_TOKEN", Help: "API token"}},
		new: func() (challenge.Provider, error) { return dode.NewDNSProvider() }},
	{Code: "duckdns", Name: "Duck DNS", URL: "https://www.duckdns.org/", Fields: []Field{{Env: "DUCKDNS_TOKEN", Help: "Account token"}},
		new: func() (challenge.Provider, error) { return duckdns.NewDNSProvider() }},
	{Code: "dyndnsfree", Name: "DynDnsFree.de", URL: "https://www.dyndnsfree.de", Fields: []Field{{Env: "DYNDNSFREE_USERNAME", Help: "Username"}, {Env: "DYNDNSFREE_PASSWORD", Help: "Password"}},
		new: func() (challenge.Provider, error) { return dyndnsfree.NewDNSProvider() }},
	{Code: "dynu", Name: "Dynu", URL: "https://www.dynu.com/", Fields: []Field{{Env: "DYNU_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return dynu.NewDNSProvider() }},
	{Code: "gandiv5", Name: "Gandi Live DNS (v5)", URL: "https://www.gandi.net", Fields: []Field{{Env: "GANDIV5_PERSONAL_ACCESS_TOKEN", Help: "Personal Access Token"}, {Env: "GANDIV5_API_KEY", Help: "API key (Deprecated)"}},
		new: func() (challenge.Provider, error) { return gandiv5.NewDNSProvider() }},
	{Code: "godaddy", Name: "Go Daddy", URL: "https://godaddy.com", Fields: []Field{{Env: "GODADDY_API_KEY", Help: "API key"}, {Env: "GODADDY_API_SECRET", Help: "API secret"}},
		new: func() (challenge.Provider, error) { return godaddy.NewDNSProvider() }},
	{Code: "hetzner", Name: "Hetzner", URL: "https://hetzner.com", Fields: []Field{{Env: "HETZNER_API_TOKEN", Help: "API token"}},
		new: func() (challenge.Provider, error) { return hetzner.NewDNSProvider() }},
	{Code: "hostingde", Name: "Hosting.de", URL: "https://www.hosting.de/", Fields: []Field{{Env: "HOSTINGDE_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return hostingde.NewDNSProvider() }},
	{Code: "hostinger", Name: "Hostinger", URL: "https://www.hostinger.com/", Fields: []Field{{Env: "HOSTINGER_API_TOKEN", Help: "API Token"}},
		new: func() (challenge.Provider, error) { return hostinger.NewDNSProvider() }},
	{Code: "httpnet", Name: "http.net", URL: "https://www.http.net/", Fields: []Field{{Env: "HTTPNET_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return httpnet.NewDNSProvider() }},
	{Code: "hurricane", Name: "Hurricane Electric DNS", URL: "https://dns.he.net/", Fields: []Field{{Env: "HURRICANE_TOKENS", Help: "TXT record names and tokens"}},
		new: func() (challenge.Provider, error) { return hurricane.NewDNSProvider() }},
	{Code: "inwx", Name: "INWX", URL: "https://www.inwx.de/en", Fields: []Field{{Env: "INWX_USERNAME", Help: "Username"}, {Env: "INWX_PASSWORD", Help: "Password"}},
		new: func() (challenge.Provider, error) { return inwx.NewDNSProvider() }},
	{Code: "ionos", Name: "Ionos", URL: "https://ionos.com", Fields: []Field{{Env: "IONOS_API_KEY", Help: "API key `<prefix>.<secret>` https://developer.hosting.ionos.com/docs/getstarted"}},
		new: func() (challenge.Provider, error) { return ionos.NewDNSProvider() }},
	{Code: "ipv64", Name: "IPv64", URL: "https://ipv64.net/", Fields: []Field{{Env: "IPV64_API_KEY", Help: "Account API Key"}},
		new: func() (challenge.Provider, error) { return ipv64.NewDNSProvider() }},
	{Code: "ispconfig", Name: "ISPConfig 3", URL: "https://www.ispconfig.org/", Fields: []Field{{Env: "ISPCONFIG_SERVER_URL", Help: "Server URL"}, {Env: "ISPCONFIG_USERNAME", Help: "Username"}, {Env: "ISPCONFIG_PASSWORD", Help: "Password"}},
		new: func() (challenge.Provider, error) { return ispconfig.NewDNSProvider() }},
	{Code: "joker", Name: "Joker", URL: "https://joker.com", Fields: []Field{{Env: "JOKER_API_MODE", Help: "'DMAPI' or 'SVC'. DMAPI is for resellers accounts. (Default: DMAPI)"}, {Env: "JOKER_USERNAME", Help: "Joker.com username"}, {Env: "JOKER_PASSWORD", Help: "Joker.com password"}, {Env: "JOKER_API_KEY", Help: "API key (only with DMAPI mode)"}},
		new: func() (challenge.Provider, error) { return joker.NewDNSProvider() }},
	{Code: "keyhelp", Name: "KeyHelp", URL: "https://www.keyweb.de/en/keyhelp/keyhelp/", Fields: []Field{{Env: "KEYHELP_BASE_URL", Help: "Server URL"}, {Env: "KEYHELP_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return keyhelp.NewDNSProvider() }},
	{Code: "linode", Name: "Linode (v4)", URL: "https://www.linode.com/", Fields: []Field{{Env: "LINODE_TOKEN", Help: "API token"}},
		new: func() (challenge.Provider, error) { return linode.NewDNSProvider() }},
	{Code: "mittwald", Name: "Mittwald", URL: "https://www.mittwald.de/", Fields: []Field{{Env: "MITTWALD_TOKEN", Help: "API token"}},
		new: func() (challenge.Provider, error) { return mittwald.NewDNSProvider() }},
	{Code: "namecheap", Name: "Namecheap", URL: "https://www.namecheap.com", Fields: []Field{{Env: "NAMECHEAP_API_USER", Help: "API user"}, {Env: "NAMECHEAP_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return namecheap.NewDNSProvider() }},
	{Code: "namedotcom", Name: "Name.com", URL: "https://www.name.com", Fields: []Field{{Env: "NAMECOM_USERNAME", Help: "Username"}, {Env: "NAMECOM_API_TOKEN", Help: "API token"}},
		new: func() (challenge.Provider, error) { return namedotcom.NewDNSProvider() }},
	{Code: "netcup", Name: "Netcup", URL: "https://www.netcup.eu/", Fields: []Field{{Env: "NETCUP_CUSTOMER_NUMBER", Help: "Customer number"}, {Env: "NETCUP_API_KEY", Help: "API key"}, {Env: "NETCUP_API_PASSWORD", Help: "API password"}},
		new: func() (challenge.Provider, error) { return netcup.NewDNSProvider() }},
	{Code: "netlify", Name: "Netlify", URL: "https://www.netlify.com", Fields: []Field{{Env: "NETLIFY_TOKEN", Help: "Token"}},
		new: func() (challenge.Provider, error) { return netlify.NewDNSProvider() }},
	{Code: "ovh", Name: "OVH", URL: "https://www.ovh.com/", Fields: []Field{{Env: "OVH_ENDPOINT", Help: "Endpoint URL (ovh-eu or ovh-ca)"}, {Env: "OVH_APPLICATION_KEY", Help: "Application key (Application Key authentication)"}, {Env: "OVH_APPLICATION_SECRET", Help: "Application secret (Application Key authentication)"}, {Env: "OVH_CONSUMER_KEY", Help: "Consumer key (Application Key authentication)"}, {Env: "OVH_CLIENT_ID", Help: "Client ID (OAuth2)"}, {Env: "OVH_CLIENT_SECRET", Help: "Client secret (OAuth2)"}, {Env: "OVH_ACCESS_TOKEN", Help: "Access token"}},
		new: func() (challenge.Provider, error) { return ovh.NewDNSProvider() }},
	{Code: "pdns", Name: "PowerDNS", URL: "https://www.powerdns.com/", Fields: []Field{{Env: "PDNS_API_KEY", Help: "API key"}, {Env: "PDNS_API_URL", Help: "API URL"}},
		new: func() (challenge.Provider, error) { return pdns.NewDNSProvider() }},
	{Code: "plesk", Name: "plesk.com", URL: "https://www.plesk.com/", Fields: []Field{{Env: "PLESK_SERVER_BASE_URL", Help: "Base URL of the server (ex: https://plesk.myserver.com:8443)"}, {Env: "PLESK_USERNAME", Help: "API username"}, {Env: "PLESK_PASSWORD", Help: "API password"}},
		new: func() (challenge.Provider, error) { return plesk.NewDNSProvider() }},
	{Code: "porkbun", Name: "Porkbun", URL: "https://porkbun.com/", Fields: []Field{{Env: "PORKBUN_SECRET_API_KEY", Help: "secret API key"}, {Env: "PORKBUN_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return porkbun.NewDNSProvider() }},
	{Code: "regfish", Name: "Regfish", URL: "https://regfish.de/", Fields: []Field{{Env: "REGFISH_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return regfish.NewDNSProvider() }},
	{Code: "rfc2136", Name: "DNS Update (RFC2136)", URL: "https://www.rfc-editor.org/rfc/rfc2136.html", Fields: []Field{{Env: "DNSUPDATE_NAMESERVER", Help: "Network address in the form \"host\" or \"host:port\""}},
		new: func() (challenge.Provider, error) { return rfc2136.NewDNSProvider() }},
	{Code: "selfhostde", Name: "SelfHost.(de|eu)", URL: "https://www.selfhost.de", Fields: []Field{{Env: "SELFHOSTDE_USERNAME", Help: "Username"}, {Env: "SELFHOSTDE_PASSWORD", Help: "Password"}, {Env: "SELFHOSTDE_RECORDS_MAPPING", Help: "Record IDs mapping with domains (ex: example.com:123:456,example.org:789,foo.example.com:147)"}},
		new: func() (challenge.Provider, error) { return selfhostde.NewDNSProvider() }},
	{Code: "servercow", Name: "Servercow", URL: "https://servercow.de/", Fields: []Field{{Env: "SERVERCOW_USERNAME", Help: "API username"}, {Env: "SERVERCOW_PASSWORD", Help: "API password"}},
		new: func() (challenge.Provider, error) { return servercow.NewDNSProvider() }},
	{Code: "simply", Name: "Simply.com", URL: "https://www.simply.com/en/domains/", Fields: []Field{{Env: "SIMPLY_ACCOUNT_NAME", Help: "Account name"}, {Env: "SIMPLY_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return simply.NewDNSProvider() }},
	{Code: "transip", Name: "TransIP", URL: "https://www.transip.nl/", Fields: []Field{{Env: "TRANSIP_ACCOUNT_NAME", Help: "Account name"}, {Env: "TRANSIP_PRIVATE_KEY_PATH", Help: "Private key path"}},
		new: func() (challenge.Provider, error) { return transip.NewDNSProvider() }},
	{Code: "variomedia", Name: "Variomedia", URL: "https://www.variomedia.de/", Fields: []Field{{Env: "VARIOMEDIA_API_TOKEN", Help: "API token"}},
		new: func() (challenge.Provider, error) { return variomedia.NewDNSProvider() }},
	{Code: "vercel", Name: "Vercel", URL: "https://vercel.com", Fields: []Field{{Env: "VERCEL_API_TOKEN", Help: "Authentication token"}},
		new: func() (challenge.Provider, error) { return vercel.NewDNSProvider() }},
	{Code: "vultr", Name: "Vultr", URL: "https://www.vultr.com/", Fields: []Field{{Env: "VULTR_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return vultr.NewDNSProvider() }},
	{Code: "zoneee", Name: "Zone.ee", URL: "https://www.zone.ee/", Fields: []Field{{Env: "ZONEEE_API_USER", Help: "API user"}, {Env: "ZONEEE_API_KEY", Help: "API key"}},
		new: func() (challenge.Provider, error) { return zoneee.NewDNSProvider() }},
}
