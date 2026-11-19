package web

import (
	"html/template"
	"net/http"
	"strconv"

	"rsc.io/qr"
)

// Öffentliche Einrichtungsseite für Endgeräte (per QR-Code aufs Handy).
var onboardingTpl = template.Must(template.New("o").Parse(`<!doctype html>
<html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>WLAN einrichten – {{.Name}}</title>
<meta name="theme-color" content="#f3f3f3" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#202020" media="(prefers-color-scheme: dark)">
<link rel="stylesheet" href="/style.css">
<style>
 body{background:var(--layer)} .wrap{max-width:720px;margin:0 auto;padding:28px 16px 48px}
 .tabs{display:flex;gap:4px;flex-wrap:wrap;margin:20px 0 12px}
 .pane{display:none} .pane.on{display:block}
 .kv{grid-template-columns:150px 1fr} .val{font-weight:600}
 ol.steps li{align-items:flex-start} ol.steps li::before{margin-top:-2px}
</style></head><body><div class="wrap">
<h1>WLAN einrichten</h1>
<div class="sub muted" style="margin-top:4px">So verbindest du dein Gerät mit dem gesicherten WLAN.</div>
<div class="chips tabs" id="tabs">
 <button class="chip on" data-p="android">Android</button><button class="chip" data-p="ios">iPhone / iPad</button>
 <button class="chip" data-p="win">Windows</button><button class="chip" data-p="mac">Mac</button>
</div>

<div class="pane on" id="android">
{{if .Public}}
 <div class="card"><h2>Android – ohne Zertifikat-Installation</h2>
 <ol class="steps"><li><span class="s-text">WLAN antippen und diese Werte wählen:</span></li></ol>
 <dl class="kv" style="margin:8px 0 12px">
  <dt>EAP-Methode</dt><dd class="val">PEAP</dd>
  <dt>Phase-2-Authentifizierung</dt><dd class="val">MSCHAPV2</dd>
  <dt>CA-Zertifikat</dt><dd class="val">Systemzertifikate verwenden</dd>
  <dt>Online-Zertifikatstatus</dt><dd class="val">Nicht überprüfen</dd>
  <dt>Domain</dt><dd class="val mono">{{.Name}}</dd>
  <dt>Identität / Passwort</dt><dd class="val">dein Benutzername und Passwort</dd>
 </dl></div>
{{else}}
 <div class="card"><h2>Android</h2>
 <ol class="steps">
  {{if .HasCA}}<li><span class="s-text"><b>Zertifikat laden:</b><br><a class="btn primary" style="margin-top:8px" href="/ca.crt" download="WLAN-Zertifikat.crt">WLAN-Zertifikat herunterladen</a></span></li>
  <li><span class="s-text"><b>Zertifikat installieren:</b> Einstellungen → Sicherheit (ggf. „Weitere Sicherheitseinstellungen“) → <i>Zertifikat installieren</i> → <b>WLAN-Zertifikat</b> → heruntergeladene Datei wählen. Name z. B. „WLAN“.</span></li>{{end}}
  <li><span class="s-text"><b>Verbinden:</b> WLAN antippen und eintragen:</span></li>
 </ol>
 <dl class="kv" style="margin:8px 0 12px">
  <dt>EAP-Methode</dt><dd class="val">PEAP</dd>
  <dt>Phase-2-Authentifizierung</dt><dd class="val">MSCHAPV2</dd>
  <dt>CA-Zertifikat</dt><dd class="val">das eben installierte Zertifikat</dd>
  <dt>Online-Zertifikatstatus</dt><dd class="val">Nicht überprüfen</dd>
  <dt>Domain</dt><dd class="val mono">{{.Name}}</dd>
  <dt>Identität / Passwort</dt><dd class="val">dein Benutzername und Passwort</dd>
 </dl>
 <div class="callout"><h3>Tipp</h3>Bietet dein Gerät beim CA-Zertifikat die Option <b>„Bei erster Verwendung vertrauen“</b> (Trust on First Use) an, kannst du die ersten beiden Schritte überspringen.</div>
 </div>
{{end}}
</div>

<div class="pane" id="ios"><div class="card"><h2>iPhone / iPad</h2><ol class="steps">
 <li><span class="s-text">Einstellungen → WLAN → Netzwerk antippen.</span></li>
 <li><span class="s-text">Benutzername und Passwort eingeben.</span></li>
 <li><span class="s-text">Beim Zertifikat <b>{{.Name}}</b> auf <b>„Vertrauen“</b> tippen.</span></li></ol></div></div>

<div class="pane" id="win"><div class="card"><h2>Windows</h2><ol class="steps">
 <li><span class="s-text">Unten rechts auf das WLAN-Symbol klicken und das Netzwerk wählen.</span></li>
 <li><span class="s-text">Benutzername und Passwort eingeben.</span></li>
 <li><span class="s-text">Bei der Frage „Verbindung fortsetzen?“ prüfen, dass <b>{{.Name}}</b> angezeigt wird, dann <b>„Verbinden“</b>.</span></li></ol></div></div>

<div class="pane" id="mac"><div class="card"><h2>Mac</h2><ol class="steps">
 <li><span class="s-text">WLAN-Menü in der Menüleiste öffnen und das Netzwerk wählen.</span></li>
 <li><span class="s-text">Benutzername und Passwort eingeben.</span></li>
 <li><span class="s-text">Zertifikat <b>{{.Name}}</b> mit <b>„Fortfahren“</b> bestätigen (ggf. Mac-Passwort eingeben).</span></li></ol></div></div>

<p class="muted" style="margin-top:20px;font-size:12px">Fingerabdruck des Serverzertifikats (SHA-256):<br><span class="mono" style="font-size:11px;overflow-wrap:anywhere">{{.Fingerprint}}</span></p>
</div>
<script src="/onboarding.js"></script>
</body></html>`))

const onboardingJS = `document.getElementById("tabs").addEventListener("click", function (e) {
  var b = e.target.closest("button"); if (!b) return;
  document.querySelectorAll("#tabs button").forEach(function (x) { x.classList.toggle("on", x === b); });
  document.querySelectorAll(".pane").forEach(function (p) { p.classList.toggle("on", p.id === b.dataset.p); });
});
var ua = navigator.userAgent;
var pick = /iPhone|iPad/.test(ua) ? "ios" : /Android/.test(ua) ? "android" : /Windows/.test(ua) ? "win" : /Mac OS/.test(ua) ? "mac" : "android";
document.querySelector('#tabs button[data-p="' + pick + '"]').click();`

func (s *Server) onboarding(w http.ResponseWriter, r *http.Request) {
	info := s.Certs.Info()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	onboardingTpl.Execute(w, map[string]any{
		"Name": info.Subject, "Public": info.Public, "HasCA": info.HasCA, "Fingerprint": info.Fingerprint,
	})
}

func (s *Server) onboardingScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Write([]byte(onboardingJS))
}

// publicCA liefert das CA-Zertifikat ohne Anmeldung (es ist ohnehin öffentlich).
func (s *Server) publicCA(w http.ResponseWriter, r *http.Request) {
	b, err := s.Certs.ActiveCACertPEM()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="WLAN-Zertifikat.crt"`)
	w.Write(b)
}

// qrPNG erzeugt einen QR-Code (nur für angemeldete Administratoren).
func (s *Server) qrPNG(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" || len(text) > 500 {
		fail(w, 400, "Text fehlt")
		return
	}
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if sc, _ := strconv.Atoi(r.URL.Query().Get("scale")); sc > 0 && sc <= 16 {
		code.Scale = sc
	} else {
		code.Scale = 6
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(code.PNG())
}
