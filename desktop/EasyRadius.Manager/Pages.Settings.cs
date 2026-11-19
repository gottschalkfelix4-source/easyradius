using System.Diagnostics;
using System.IO;
using System.Net.Http;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using Microsoft.Win32;
using Wpf.Ui.Controls;
using Image = System.Windows.Controls.Image;
using PasswordBox = Wpf.Ui.Controls.PasswordBox;
using TextBlock = System.Windows.Controls.TextBlock;
using TextBox = Wpf.Ui.Controls.TextBox;

namespace EasyRadius.Manager;

// ===================== Zertifikat =====================
public class CertPage : PageBase
{
    readonly System.Windows.Threading.DispatcherTimer poll = new() { Interval = TimeSpan.FromSeconds(4) };

    public CertPage() : base("Zertifikat", "Damit weisen sich Server und WLAN gegenüber den Endgeräten aus")
    {
        poll.Tick += async (_, _) => { poll.Stop(); await SafeReload(); };
        Unloaded += (_, _) => poll.Stop();
    }

    protected override async Task Reload()
    {
        var certTask = App.Api.Get<CertInfo>("/api/cert");
        var leTask = App.Api.Get<AcmeInfo>("/api/acme");
        var dash = await App.Api.Get<Dashboard>("/api/dashboard");
        var c = await certTask;
        var le = await leTask;
        Body.Children.Clear();

        // Uri.Host liefert IPv6 mit Klammern („[::1]“) – daher IdnHost und IPAddress.IsLoopback verwenden.
        var local = ApiClient.IsLoopbackHost(App.Api.BaseUri.IdnHost);
        var baseUrl = local && dash.Ips.Count > 0 ? $"{App.Api.BaseUri.Scheme}://{UrlHost(dash.Ips[0])}:{App.Api.BaseUri.Port}" : App.Api.BaseUri.ToString().TrimEnd('/');
        var onboard = baseUrl + "/einrichten";
        var days = (int)(c.NotAfter - DateTime.UtcNow).TotalDays;
        var issuer = c.Public ? "öffentliche Zertifizierungsstelle" : c.SelfSigned ? "eigene Zertifizierungsstelle" : "eigenes Zertifikat";

        // Kopf-Karte
        var info = new StackPanel();
        info.Children.Add(new TextBlock { Text = c.Subject, FontSize = 20, FontWeight = FontWeights.SemiBold }.Fg());
        info.Children.Add(new TextBlock { Text = $"Ausgestellt von {c.Issuer} ({issuer})", Style = UI.Style("Muted") });
        info.Children.Add(new TextBlock
        {
            Text = days < 30 ? $"Läuft in {days} Tagen ab" : $"Gültig bis {c.NotAfter.ToLocalTime():dd.MM.yyyy}" + (c.Public ? " · Android ohne Zertifikat-Installation" : ""),
            Foreground = Table.StatusBrush(days >= 30), Margin = new Thickness(0, 6, 0, 0),
        });
        info.Children.Add(new TextBlock { Text = "SHA-256: " + c.Fingerprint, Style = UI.Style("Muted"), FontFamily = new FontFamily("Cascadia Mono, Consolas"), Margin = new Thickness(0, 10, 0, 0) });
        Body.Children.Add(CardOf(UI.Hstack(new SymbolIcon(SymbolRegular.ShieldCheckmark24) { FontSize = 36, Margin = new Thickness(0, 0, 20, 0), VerticalAlignment = VerticalAlignment.Top }, info)));

        // QR-Code für Endgeräte
        Body.Children.Add(Section("Geräte einrichten"));
        var qr = new Image { Width = 150, Height = 150, Margin = new Thickness(0, 0, 20, 0) };
        RenderOptions.SetBitmapScalingMode(qr, BitmapScalingMode.NearestNeighbor);
        try
        {
            var bytes = await App.Api.Bytes("/api/qr.png?scale=6&text=" + Uri.EscapeDataString(onboard));
            var bmp = new BitmapImage();
            bmp.BeginInit(); bmp.StreamSource = new MemoryStream(bytes); bmp.CacheOption = BitmapCacheOption.OnLoad; bmp.EndInit();
            qr.Source = bmp;
        }
        catch { }
        var qrText = new StackPanel { VerticalAlignment = VerticalAlignment.Center };
        qrText.Children.Add(new TextBlock { Text = "Mit dem Handy scannen", FontWeight = FontWeights.SemiBold }.Fg());
        qrText.Children.Add(new TextBlock { Text = "Öffnet eine Anleitung für Android, iPhone, Windows und Mac – mit den passenden Werten" + (c.HasCa ? " und dem Zertifikat zum Herunterladen." : "."), Style = UI.Style("Muted"), MaxWidth = 520, Margin = new Thickness(0, 4, 0, 10) });
        qrText.Children.Add(UI.Hstack(
            UI.Btn("Anleitung öffnen", SymbolRegular.Phone24, () => { Open(onboard); return Task.CompletedTask; }),
            UI.Btn("Link kopieren", SymbolRegular.Copy24, () => { UI.Copy(onboard); return Task.CompletedTask; })));
        qrText.Children.Add(new TextBlock { Text = onboard, Style = UI.Style("Muted"), FontFamily = new FontFamily("Cascadia Mono, Consolas"), Margin = new Thickness(0, 8, 0, 0) });
        Body.Children.Add(CardOf(UI.Hstack(new Border { Background = Brushes.White, CornerRadius = new CornerRadius(8), Padding = new Thickness(4), Child = qr, Margin = new Thickness(0, 0, 20, 0) }, qrText)));

        // Let's Encrypt
        Body.Children.Add(Section("Let's Encrypt"));
        var method = le.Method switch { "acmedns" => "CNAME-Delegation", "http" => "HTTP (Port 80)", _ => "DNS-API" };
        var state = !le.Enabled ? "aus" : le.Running ? "wird beantragt …" : le.LastError != "" ? "Fehler" : "aktiv";
        var desc = le.Enabled
            ? $"Status: {state} · Domain {le.Domain} · {method}" + (le.LastSuccess.Year > 2000 ? $" · zuletzt erneuert {Fmt.Rel(le.LastSuccess)}" : "") + (le.Staging ? " · Testsystem" : "")
            : "Mit eigener Domain brauchen Android-Geräte keine Zertifikat-Installation mehr („Systemzertifikate verwenden“).";
        Body.Children.Add(UI.SettingCard(SymbolRegular.Globe24, "Öffentliches Zertifikat", desc, le.Enabled
            ? UI.Hstack(UI.Btn("Jetzt erneuern", SymbolRegular.ArrowSync24, async () => { await App.Api.Call(HttpMethod.Post, "/api/acme/run"); await UI.Toast("Zertifikat wird beantragt …"); await Reload(); }).AdminOnly(),
                        UI.Btn("Ändern", SymbolRegular.Edit24, async () => { if (await AcmeDialog()) await Reload(); }).AdminOnly())
            : UI.Btn("Einrichten", SymbolRegular.Add24, async () => { if (await AcmeDialog()) await Reload(); }, ControlAppearance.Primary).AdminOnly()));
        if (le.LastError != "")
            Body.Children.Add(new InfoBar { Title = "Let's Encrypt", Message = le.LastError, Severity = InfoBarSeverity.Error, IsOpen = true, IsClosable = false, Margin = new Thickness(0, 4, 0, 4) });
        if (le.CnameName != "")
        {
            var sp = new StackPanel();
            sp.Children.Add(new TextBlock { Text = "Noch ein Schritt: CNAME-Eintrag anlegen", FontWeight = FontWeights.SemiBold }.Fg());
            sp.Children.Add(new TextBlock { Text = "Lege bei deinem Domain-Anbieter einmalig diesen DNS-Eintrag an. Danach erneuert EasyRadius das Zertifikat dauerhaft automatisch.", Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 10) });
            sp.Children.Add(UI.Hstack(new TextBlock { Text = "Name:  " + le.CnameName, FontFamily = new FontFamily("Cascadia Mono, Consolas"), VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(0, 0, 8, 0) }.Fg(),
                UI.Btn("Kopieren", SymbolRegular.Copy24, () => { UI.Copy(le.CnameName); return Task.CompletedTask; })));
            sp.Children.Add(UI.Hstack(new TextBlock { Text = "Ziel:   " + le.CnameTarget, FontFamily = new FontFamily("Cascadia Mono, Consolas"), VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(0, 6, 8, 0) }.Fg(),
                UI.Btn("Kopieren", SymbolRegular.Copy24, () => { UI.Copy(le.CnameTarget); return Task.CompletedTask; })));
            sp.Children.Add(new Border { Height = 10 });
            sp.Children.Add(UI.Btn("Eintrag ist angelegt – jetzt prüfen", SymbolRegular.Checkmark24, async () => { await App.Api.Call(HttpMethod.Post, "/api/acme/run"); await Reload(); }, ControlAppearance.Primary).AdminOnly());
            Body.Children.Add(CardOf(sp));
        }
        if (le.Running) poll.Start();

        // Weitere Aktionen
        Body.Children.Add(Section("Weitere Aktionen"));
        if (c.HasCa)
            Body.Children.Add(UI.SettingCard(SymbolRegular.ArrowDownload24, "CA-Zertifikat speichern", "Zum Installieren auf Endgeräten, die das Zertifikat prüfen.",
                UI.Btn("Speichern …", SymbolRegular.Save24, SaveCA)));
        if (!c.SelfSigned && App.CanWrite)
            Body.Children.Add(UI.SettingCard(SymbolRegular.Shield24, "Wieder das Zertifikat der eigenen CA verwenden", "Geräte mit installierter EasyRadius-CA vertrauen ihm sofort. Let's Encrypt wird ausgeschaltet.",
                UI.Btn("Verwenden", SymbolRegular.Checkmark24, async () => { await App.Api.Call(HttpMethod.Post, "/api/cert/own"); await UI.Toast("Zertifikat der eigenen CA ist aktiv"); await Reload(); })));
        if (App.CanWrite)
        {
            Body.Children.Add(UI.SettingCard(SymbolRegular.ArrowSync24, "Komplett neu erzeugen", "Neue Zertifizierungsstelle und neues Serverzertifikat.",
                UI.Btn("Erzeugen …", SymbolRegular.Add24, async () => { if (await Generate()) await Reload(); })));
            Body.Children.Add(UI.SettingCard(SymbolRegular.ArrowUpload24, "Eigenes Zertifikat verwenden", "Zertifikat und Schlüssel im PEM-Format, z. B. von deiner Firmen-CA.",
                UI.Btn("Hochladen …", SymbolRegular.ArrowUpload24, async () => { if (await Upload()) await Reload(); })));
        }

        // Benutzerzertifikate (EAP-TLS)
        Body.Children.Add(Section("Benutzerzertifikate (EAP-TLS)"));
        try
        {
            var certs = await ApiException.NewFeature(() => App.Api.Get<List<ClientCert>>("/api/clientcerts"));
            if (certs.Count == 0)
                Body.Children.Add(new TextBlock { Text = "Noch keine Zertifikate ausgestellt. Unter „Benutzer“ → Rechtsklick → „Zertifikat für EAP-TLS ausstellen“ (EAP-TLS muss in den Einstellungen eingeschaltet sein).", Style = UI.Style("Muted") });
            else
            {
                var table = Table.Create<ClientCert>([
                    new("Benutzer", nameof(ClientCert.User), 200, nameof(ClientCert.Serial), Bold: true),
                    new("Status", nameof(ClientCert.StatusText), 110, ColorPath: nameof(ClientCert.StatusColor)),
                    new("Ausgestellt", nameof(ClientCert.CreatedText), 120),
                    new("Gültig bis", nameof(ClientCert.NotAfterText), 120),
                ], null, Table.Writable<ClientCert>(("Sperren", Revoke)));
                table.ItemsSource = certs.OrderBy(x => x.Revoked).ThenBy(x => x.User, StringComparer.OrdinalIgnoreCase).ToList();
                table.MaxHeight = 360;
                Body.Children.Add(CardOf(table, new Thickness(0)));
                Body.Children.Add(new TextBlock { Text = "Ein gesperrtes Zertifikat wird bei der nächsten Anmeldung abgelehnt (Rechtsklick → „Sperren“).", Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 0) });
            }
        }
        catch (ApiException e) when (e.Status == System.Net.HttpStatusCode.NotFound)
        {
            Body.Children.Add(new TextBlock { Text = e.Message, Style = UI.Style("Muted") });
        }
    }

    async Task Revoke(ClientCert c)
    {
        if (c.Revoked) throw new ApiException("Dieses Zertifikat ist bereits gesperrt.");
        if (!await UI.Confirm("Zertifikat sperren", $"Soll das Zertifikat von „{c.User}“ (Seriennummer {c.Serial}) gesperrt werden? Das Gerät kann sich damit nicht mehr anmelden. Das lässt sich nicht rückgängig machen.", "Sperren")) return;
        await ApiException.NewFeature(() => App.Api.Call(HttpMethod.Post, $"/api/clientcerts/{Uri.EscapeDataString(c.Serial)}/revoke"));
        await UI.Toast("Zertifikat gesperrt");
        await Reload();
    }

    public static string UrlHost(string ip) => ip.Contains(':') && !ip.StartsWith('[') ? $"[{ip}]" : ip;

    public static void Open(string url)
    {
        try { Process.Start(new ProcessStartInfo(url) { UseShellExecute = true }); } catch { }
    }

    static async Task SaveCA()
    {
        var dlg = new SaveFileDialog { FileName = "EasyRadius-CA.crt", Filter = "Zertifikat (*.crt)|*.crt" };
        if (dlg.ShowDialog() != true) return;
        await File.WriteAllBytesAsync(dlg.FileName, await App.Api.Bytes("/api/cert/ca.crt"));
        await UI.Toast("CA-Zertifikat gespeichert");
    }

    static async Task<bool> Generate()
    {
        var f = new Form();
        f.Add("", new InfoBar { Title = "Hinweis", Message = "Bereits verbundene Geräte müssen dem neuen Zertifikat beim nächsten Verbinden erneut vertrauen.", Severity = InfoBarSeverity.Warning, IsOpen = true, IsClosable = false });
        var name = f.Text("Servername im Zertifikat", App.State.Server == "" ? "EasyRadius" : App.State.Server, null, "Wird auf den Endgeräten angezeigt, z. B. „radius.zuhause.lan“.");
        var dlg = new FormDialog("Neues Zertifikat erzeugen", f.Panel, "Erzeugen");
        dlg.OnSave = () => App.Api.Call(HttpMethod.Post, "/api/cert/generate", new { name = name.Text.Trim() });
        if (await dlg.ShowAsync() != ContentDialogResult.Primary) return false;
        await UI.Toast("Neues Zertifikat aktiv");
        return true;
    }

    static async Task<bool> Upload()
    {
        var pick = (string title) =>
        {
            var d = new OpenFileDialog { Title = title, Filter = "PEM-Dateien (*.pem;*.crt;*.cer;*.key)|*.pem;*.crt;*.cer;*.key|Alle Dateien|*.*" };
            return d.ShowDialog() == true ? d.FileName : null;
        };
        var cert = pick("Zertifikat (inkl. Zwischenzertifikaten) wählen");
        if (cert == null) return false;
        var key = pick("Privaten Schlüssel wählen");
        if (key == null) return false;
        var form = new MultipartFormDataContent
        {
            { new ByteArrayContent(await File.ReadAllBytesAsync(cert)), "cert", Path.GetFileName(cert) },
            { new ByteArrayContent(await File.ReadAllBytesAsync(key)), "key", Path.GetFileName(key) },
        };
        await App.Api.Call(HttpMethod.Post, "/api/cert/upload", form);
        await UI.Toast("Zertifikat übernommen");
        return true;
    }

    static async Task<bool> AcmeDialog()
    {
        var le = await App.Api.Get<AcmeInfo>("/api/acme");
        var provs = await App.Api.Get<List<DnsProvider>>("/api/acme/providers");
        var f = new Form();
        f.Note("Du brauchst eine eigene Domain – egal bei welchem Anbieter. Der RADIUS-Server muss dafür (außer beim HTTP-Weg) nicht aus dem Internet erreichbar sein.");
        var domain = f.Text("Domain für den RADIUS-Server", le.Domain, "radius.example.de", "Diesen Namen tragen Android-Geräte später als „Domain“ ein.");
        f.Note("Wie soll die Domain bestätigt werden?", true);
        var rDns = new RadioButton { Content = $"Automatisch über meinen DNS-Anbieter ({provs.Count} Anbieter, z. B. IONOS, Hetzner, netcup, INWX, all-inkl)", GroupName = "m", IsChecked = le.Method is "dns" or "" };
        var rCname = new RadioButton { Content = "Jeder Anbieter, z. B. Strato: einmal einen CNAME-Eintrag anlegen", GroupName = "m", IsChecked = le.Method == "acmedns" };
        var rHttp = new RadioButton { Content = "Über Port 80 (Domain zeigt auf die öffentliche IP, Port weitergeleitet)", GroupName = "m", IsChecked = le.Method == "http" };
        foreach (var r in new[] { rDns, rCname, rHttp }) { r.Margin = new Thickness(0, 4, 0, 4); f.Panel.Children.Add(r); }

        var dnsBox = new StackPanel();
        var provCombo = new ComboBox { ItemsSource = provs, HorizontalAlignment = HorizontalAlignment.Stretch, SelectedItem = provs.FirstOrDefault(p => p.Code == le.Provider) };
        var fields = new StackPanel();
        dnsBox.Children.Add(new TextBlock { Text = "DNS-Anbieter", Margin = new Thickness(0, 12, 0, 6) }.Fg());
        dnsBox.Children.Add(provCombo);
        dnsBox.Children.Add(new TextBlock { Text = "Dein Anbieter fehlt? Dann den CNAME-Weg wählen – der funktioniert überall.", Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 0) });
        dnsBox.Children.Add(fields);
        var inputs = new Dictionary<string, PasswordBox>();
        void ShowFields()
        {
            fields.Children.Clear();
            inputs.Clear();
            if (provCombo.SelectedItem is not DnsProvider p) return;
            foreach (var fl in p.Fields)
            {
                var box = new PasswordBox { PlaceholderText = p.Code == le.Provider && le.SavedCredentials.Contains(fl.Env) ? "gespeichert – leer lassen zum Behalten" : "" };
                inputs[fl.Env] = box;
                fields.Children.Add(new TextBlock { Text = fl.Env, FontFamily = new FontFamily("Cascadia Mono, Consolas"), FontSize = 12, Margin = new Thickness(0, 12, 0, 6) }.Fg());
                fields.Children.Add(box);
                fields.Children.Add(new TextBlock { Text = fl.Help, Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 0) });
            }
        }
        provCombo.SelectionChanged += (_, _) => ShowFields();
        ShowFields();
        f.Panel.Children.Add(dnsBox);

        var cnameBox = new StackPanel();
        cnameBox.Children.Add(new TextBlock { Text = "Nach dem Speichern zeigt EasyRadius den CNAME-Eintrag an. Die Bestätigung läuft über den Dienst acme-dns – dorthin werden nur Prüfwerte übertragen, keine Zugangsdaten.", Style = UI.Style("Muted"), Margin = new Thickness(0, 8, 0, 0) });
        var acmeDns = new TextBox { Text = le.AcmeDnsUrl, PlaceholderText = le.DefaultAcmeDnsUrl };
        cnameBox.Children.Add(new TextBlock { Text = "acme-dns-Server (optional, eigener Server)", Margin = new Thickness(0, 12, 0, 6) }.Fg());
        cnameBox.Children.Add(acmeDns);
        f.Panel.Children.Add(cnameBox);

        void ShowMethod()
        {
            dnsBox.Visibility = rDns.IsChecked == true ? Visibility.Visible : Visibility.Collapsed;
            cnameBox.Visibility = rCname.IsChecked == true ? Visibility.Visible : Visibility.Collapsed;
        }
        foreach (var r in new[] { rDns, rCname, rHttp }) r.Checked += (_, _) => ShowMethod();
        ShowMethod();

        var email = f.Text("E-Mail (optional)", le.Email, "für Hinweise von Let's Encrypt");
        var staging = new CheckBox { Content = "Testsystem verwenden (Zertifikate sind nicht vertrauenswürdig – nur zum Ausprobieren)", IsChecked = le.Staging, Margin = new Thickness(0, 12, 0, 0) };
        var tos = new CheckBox { Content = "Ich akzeptiere die Nutzungsbedingungen von Let's Encrypt (letsencrypt.org/repository).", Margin = new Thickness(0, 8, 0, 0) };
        var off = new CheckBox { Content = "Let's Encrypt ausschalten", Margin = new Thickness(0, 8, 0, 0), Visibility = le.Enabled ? Visibility.Visible : Visibility.Collapsed };
        f.Panel.Children.Add(staging);
        f.Panel.Children.Add(tos);
        f.Panel.Children.Add(off);

        var dlg = new FormDialog("Let's Encrypt einrichten", f.Panel, "Speichern und beantragen");
        dlg.OnSave = () => App.Api.Call(HttpMethod.Put, "/api/acme", new
        {
            enabled = off.IsChecked != true, domain = domain.Text.Trim(), email = email.Text.Trim(),
            method = rCname.IsChecked == true ? "acmedns" : rHttp.IsChecked == true ? "http" : "dns",
            provider = (provCombo.SelectedItem as DnsProvider)?.Code ?? "",
            credentials = inputs.Where(x => x.Value.Password.Trim() != "").ToDictionary(x => x.Key, x => x.Value.Password.Trim()),
            acmeDnsUrl = acmeDns.Text.Trim(), staging = staging.IsChecked == true, acceptTos = tos.IsChecked == true,
        });
        if (await dlg.ShowAsync() != ContentDialogResult.Primary) return false;
        await UI.Toast(off.IsChecked == true ? "Let's Encrypt ausgeschaltet" : "Zertifikat wird beantragt – das dauert meist 1–5 Minuten");
        return true;
    }
}

// ===================== Active Directory =====================
public class AdPage : PageBase
{
    public AdPage() : base("Active Directory", "Benutzer aus dem Firmen-AD anmelden lassen") { }

    protected override async Task Reload()
    {
        var a = await App.Api.Get<AdInfo>("/api/ad");
        Body.Children.Clear();
        if (a.Status != "")
            Body.Children.Add(new InfoBar { Title = "AD nicht verfügbar", Message = a.Status, Severity = InfoBarSeverity.Warning, IsOpen = true, IsClosable = false, Margin = new Thickness(0, 0, 0, 8) });
        var mode = a.Mode switch { "domain" => "Domäne " + a.Domain, "ldap" => "LDAP", _ => "Aus" };
        Body.Children.Add(UI.SettingCard(SymbolRegular.Building24, mode,
            a.Platform != "windows" ? "EasyRadius läuft unter Linux – Active Directory ist hier per LDAP möglich."
                : a.Joined ? $"Dieser Rechner ist Mitglied der Domäne {a.Domain}." : "Dieser Rechner ist keiner Domäne beigetreten.",
            a.Mode != "off" && App.CanWrite ? UI.Btn("Anmeldung testen", SymbolRegular.Key24, UsersPage.TestLogin) : null));

        Body.Children.Add(Section("Anbindung"));
        var rOff = new RadioButton { GroupName = "ad", IsChecked = a.Mode == "off", Content = "Aus – nur lokale Benutzer" };
        var rDom = new RadioButton { GroupName = "ad", IsChecked = a.Mode == "domain", IsEnabled = a.Joined, Content = "Domänenmitglied (empfohlen) – WLAN und Geräte-Logins mit AD-Passwort, Gruppen inkl. Verschachtelung" + (a.Joined ? "" : a.Platform != "windows" ? " · nur wenn EasyRadius unter Windows läuft" : " · erst nach Domänenbeitritt verfügbar") };
        var rLdap = new RadioButton { GroupName = "ad", IsChecked = a.Mode == "ldap", Content = "LDAP – ohne Domänenbeitritt; nur für Switch-/Router-/VPN-Logins (PAP), nicht für WLAN" };
        var radios = new StackPanel();
        foreach (var r in new[] { rOff, rDom, rLdap }) { r.Margin = new Thickness(0, 4, 0, 4); radios.Children.Add(r); }
        Body.Children.Add(CardOf(radios));

        var ldap = new StackPanel();
        var url = new TextBox { Text = a.LdapUrl, PlaceholderText = "ldaps://dc01.firma.local", Width = 300 };
        var baseDn = new TextBox { Text = a.BaseDn, PlaceholderText = "DC=firma,DC=local", Width = 300 };
        var bindUser = new TextBox { Text = a.BindUser, PlaceholderText = "svc-radius@firma.local", Width = 300 };
        var bindPw = new PasswordBox { Width = 300, PlaceholderText = a.HasBindPassword ? "gespeichert" : "" };
        var attr = new TextBox { Text = a.UserAttribute, Width = 200 };
        var startTls = new ToggleSwitch { IsChecked = a.StartTls };
        var skip = new ToggleSwitch { IsChecked = a.SkipVerify };
        ldap.Children.Add(Section("LDAP-Verbindung"));
        ldap.Children.Add(UI.SettingCard(SymbolRegular.Globe24, "Server", "ldaps://… (Port 636) oder ldap://… mit StartTLS", url));
        ldap.Children.Add(UI.SettingCard(SymbolRegular.TextBulletListLtr24, "Basis-DN", "Ab hier wird nach Benutzern gesucht", baseDn));
        ldap.Children.Add(UI.SettingCard(SymbolRegular.Person24, "Dienstkonto", "Konto mit Leserechten im AD", bindUser));
        ldap.Children.Add(UI.SettingCard(SymbolRegular.Key24, "Passwort des Dienstkontos", a.HasBindPassword ? "Gespeichert – leer lassen, um es zu behalten" : null, bindPw));
        ldap.Children.Add(UI.SettingCard(SymbolRegular.Edit24, "Anmeldename-Attribut", "Standard: sAMAccountName", attr));
        ldap.Children.Add(UI.SettingCard(SymbolRegular.LockClosed24, "StartTLS verwenden", "Nur bei ldap://-Adressen", startTls));
        ldap.Children.Add(UI.SettingCard(SymbolRegular.Warning24, "Zertifikat des Domänencontrollers nicht prüfen", "Nur zum Testen – unsicher", skip));
        Body.Children.Add(ldap);
        void Vis() => ldap.Visibility = rLdap.IsChecked == true ? Visibility.Visible : Visibility.Collapsed;
        foreach (var r in new[] { rOff, rDom, rLdap }) r.Checked += (_, _) => Vis();
        Vis();

        Body.Children.Add(Section("Regeln"));
        var require = new ToggleSwitch { IsChecked = a.RequireGroup };
        Body.Children.Add(UI.SettingCard(SymbolRegular.PeopleTeam24, "Nur AD-Benutzer mit verknüpfter Gruppe zulassen",
            "Empfohlen. Verknüpfung unter „Gruppen“ → AD-Gruppen. Sonst darf sich jedes AD-Konto anmelden.", require));

        var save = UI.Btn("Speichern", SymbolRegular.Save24, async () =>
        {
            await App.Api.Call(HttpMethod.Put, "/api/ad", new
            {
                mode = rDom.IsChecked == true ? "domain" : rLdap.IsChecked == true ? "ldap" : "off",
                ldapUrl = url.Text.Trim(), baseDn = baseDn.Text.Trim(), bindUser = bindUser.Text.Trim(), bindPassword = bindPw.Password,
                userAttribute = attr.Text.Trim(), startTls = startTls.IsChecked == true, skipVerify = skip.IsChecked == true, requireGroup = require.IsChecked == true,
            });
            await UI.Toast("AD-Einstellungen gespeichert");
            await Reload();
        }, ControlAppearance.Primary).AdminOnly();
        save.HorizontalAlignment = HorizontalAlignment.Right;
        save.Margin = new Thickness(0, 12, 0, 0);
        Body.Children.Add(save);

        Body.Children.Add(new TextBlock
        {
            Style = UI.Style("Muted"), Margin = new Thickness(0, 16, 0, 0),
            Text = "Lokale Benutzer haben immer Vorrang vor gleichnamigen AD-Konten. Anmeldenamen dürfen als name, FIRMA\\name oder name@firma.local eingegeben werden."
                + (a.Joined || a.Platform != "windows" ? "" : "\nFür WLAN mit AD-Passwörtern muss der Rechner der Domäne beitreten (Einstellungen → Konten → Auf Arbeits- oder Schulkonto zugreifen). Danach den EasyRadius-Dienst neu starten."),
        });
    }
}

// ===================== Einstellungen =====================
public class SettingsPage : PageBase
{
    public SettingsPage() : base("Einstellungen", "") { }

    static TextBox Num(int value, double width = 110) => new() { Text = value.ToString(), Width = width };

    protected override async Task Reload()
    {
        var s = await App.Api.Get<SettingsInfo>("/api/settings");
        var dash = await App.Api.Get<Dashboard>("/api/dashboard");
        Body.Children.Clear();
        bool rw = App.CanWrite;

        // --- RADIUS ---
        Body.Children.Add(new TextBlock { Text = "RADIUS-Server", Style = UI.Style("SectionTitle"), Margin = new Thickness(0, 0, 0, 8) });
        var name = new TextBox { Text = s.ServerName, Width = 240 };
        var auth = Num(s.AuthPort);
        var acct = Num(s.AcctPort);
        Body.Children.Add(UI.SettingCard(SymbolRegular.Router24, "Servername", "Wird den Geräten bei der Anmeldung genannt.", name));
        Body.Children.Add(UI.SettingCard(SymbolRegular.Key24, "Authentifizierungs-Port", "UDP-Port für Anmeldungen. Standard: 1812", auth));
        Body.Children.Add(UI.SettingCard(SymbolRegular.Pulse24, "Accounting-Port", "UDP-Port für Sitzungsdaten. Standard: 1813", acct));

        // --- WLAN-Verfahren ---
        Body.Children.Add(Section("WLAN-Anmeldeverfahren"));
        Body.Children.Add(UI.SettingCard(SymbolRegular.Wifi124, "PEAP (MSCHAPv2)", "Immer aktiv – funktioniert mit Benutzername und Passwort auf fast allen Geräten.", null));
        var ttls = new ToggleSwitch { IsChecked = s.Eap.Ttls };
        var tls = new ToggleSwitch { IsChecked = s.Eap.Tls };
        Body.Children.Add(UI.SettingCard(SymbolRegular.LockClosed24, "EAP-TTLS", "Zusätzlich anbieten (innen PAP oder MSCHAPv2) – z. B. für Linux, ältere Android-Geräte oder AD-Konten per LDAP.", ttls));
        Body.Children.Add(UI.SettingCard(SymbolRegular.Certificate24, "EAP-TLS mit Benutzerzertifikaten",
            "Anmeldung ohne Passwort mit einem Zertifikat der eigenen CA. Zertifikate stellst du unter „Benutzer“ (Rechtsklick) aus.", tls));

        // --- Sperre ---
        Body.Children.Add(Section("Schutz vor Passwort-Raten"));
        var lockOn = new ToggleSwitch { IsChecked = s.Lockout.Enabled };
        var attempts = Num(s.Lockout.Attempts, 90);
        var minutes = Num(s.Lockout.Minutes, 90);
        Body.Children.Add(UI.SettingCard(SymbolRegular.ShieldLock24, "Nach Fehlversuchen sperren",
            "Gilt je Benutzer und je Endgerät (MAC). Gesperrte findest du unter „Sitzungen“.", lockOn));
        Body.Children.Add(UI.SettingCard(SymbolRegular.NumberSymbol24, "Fehlversuche", "3 bis 100 innerhalb des Zeitraums", attempts));
        Body.Children.Add(UI.SettingCard(SymbolRegular.Timer24, "Zeitraum und Sperrdauer (Minuten)", "1 bis 1440", minutes));
        void LockVis() => attempts.IsEnabled = minutes.IsEnabled = rw && lockOn.IsChecked == true;
        lockOn.Checked += (_, _) => LockVis();
        lockOn.Unchecked += (_, _) => LockVis();
        LockVis();

        // --- Weboberfläche und Protokoll ---
        Body.Children.Add(Section("Weboberfläche und Protokoll"));
        var httpsOnly = new ToggleSwitch { IsChecked = s.HttpsOnly, IsEnabled = s.HttpsAddr != "" };
        Body.Children.Add(UI.SettingCard(SymbolRegular.Globe24, "Nur HTTPS aus dem Netz",
            s.HttpsAddr == ""
                ? "HTTPS ist auf dem Server nicht eingeschaltet."
                : "Andere Rechner erreichen Weboberfläche und Verwaltung nur noch verschlüsselt. Die Einrichtungsseite für Endgeräte bleibt per HTTP erreichbar. Einschalten nur über eine HTTPS-Verbindung oder an diesem Server selbst.",
            httpsOnly));
        var retention = Num(s.LogRetention > 0 ? s.LogRetention : 5000);
        Body.Children.Add(UI.SettingCard(SymbolRegular.TextBulletListLtr24, "Protokoll aufbewahren", "Anzahl der Einträge, die gespeichert bleiben (100 bis 100000).", retention));

        foreach (var c in new Control[] { name, auth, acct, ttls, tls, lockOn, httpsOnly, retention })
            if (!rw) c.IsEnabled = false;
        if (!rw) attempts.IsEnabled = minutes.IsEnabled = false;

        var save = UI.Btn("Speichern", SymbolRegular.Save24, async () =>
        {
            static int Parse(TextBox t, string what) =>
                int.TryParse(t.Text.Trim(), out var v) ? v : throw new ApiException($"{what}: bitte eine Zahl eintragen");
            var body = new
            {
                serverName = name.Text.Trim(),
                authPort = Parse(auth, "Authentifizierungs-Port"),
                acctPort = Parse(acct, "Accounting-Port"),
                httpsOnly = httpsOnly.IsChecked == true,
                logRetention = Parse(retention, "Protokoll"),
                lockout = new { enabled = lockOn.IsChecked == true, attempts = Parse(attempts, "Fehlversuche"), minutes = Parse(minutes, "Sperrdauer") },
                eap = new { ttls = ttls.IsChecked == true, tls = tls.IsChecked == true },
            };
            await App.Api.Call(HttpMethod.Put, "/api/settings", body);
            await UI.Toast("Einstellungen gespeichert");
            await Reload();
        }, ControlAppearance.Primary).AdminOnly();
        save.HorizontalAlignment = HorizontalAlignment.Right;
        save.Margin = new Thickness(0, 12, 0, 0);
        Body.Children.Add(save);

        // --- Fernzugriff ---
        Body.Children.Add(Section("Fernzugriff"));
        var urls = new List<string>();
        if (s.HttpsAddr != "" && App.State.HttpsPort != "")
            urls.AddRange(dash.Ips.Select(ip => $"https://{CertPage.UrlHost(ip)}:{App.State.HttpsPort}"));
        if (!s.HttpsOnly || urls.Count == 0)
            urls.AddRange(dash.Ips.Select(ip => $"{App.Api.BaseUri.Scheme}://{CertPage.UrlHost(ip)}:{App.Api.BaseUri.Port}").Where(u => !urls.Contains(u)));
        Body.Children.Add(UI.SettingCard(SymbolRegular.Globe24, "Weboberfläche im Netzwerk",
            urls.Count > 0 ? "Von jedem Rechner im lokalen Netz erreichbar: " + string.Join(", ", urls) : "Keine Netzwerkadresse gefunden.",
            UI.Hstack(UI.Btn("Im Browser öffnen", SymbolRegular.Open24, () => { CertPage.Open(App.Api.BaseUri.ToString()); return Task.CompletedTask; }),
                      UI.Btn("Kopieren", SymbolRegular.Copy24, () => { UI.Copy(urls.FirstOrDefault() ?? App.Api.BaseUri.ToString()); return Task.CompletedTask; }))));
        Body.Children.Add(UI.SettingCard(SymbolRegular.Desktop24, "Diese App mit einem anderen Server verbinden",
            "Die Verwaltung kann jeden EasyRadius im Netzwerk steuern – beim Anmelden einfach dessen Adresse eintragen.",
            UI.Btn("Server wechseln", SymbolRegular.ArrowSwap24, () => { App.ShowLogin(autoLogin: false); return Task.CompletedTask; })));

        // --- Konto ---
        Body.Children.Add(Section("Konto"));
        Body.Children.Add(UI.SettingCard(SymbolRegular.Person24, Fmt.Role(App.State.Role), $"Angemeldet als {s.AdminUser} an {App.Api.BaseUri.Authority}",
            UI.Btn("Passwort ändern", SymbolRegular.Key24, ChangePassword)));
        if (rw)
            Body.Children.Add(UI.SettingCard(SymbolRegular.PeopleSettings24, "Konten und API-Tokens", "Weitere Administratoren, Nur-Lese-Konten und Tokens für Skripte.",
                UI.Btn("Verwalten", SymbolRegular.ChevronRight24, () => { UI.Go(typeof(AccountsPage)); return Task.CompletedTask; })));
        Body.Children.Add(UI.SettingCard(SymbolRegular.SignOut24, "Abmelden", "Gespeichertes Passwort auf diesem PC wird entfernt.",
            UI.Btn("Abmelden", SymbolRegular.SignOut24, async () =>
            {
                try { await App.Api.Call(HttpMethod.Post, "/api/logout"); } catch { }
                var saved = SavedLogin.Load(); saved.Password = null; saved.Save();
                App.ShowLogin(autoLogin: false);
            })));
        Body.Children.Add(UI.SettingCard(SymbolRegular.PhoneLaptop24, "Überall abmelden", "Beendet alle Sitzungen deines Kontos – in Browsern, auf anderen PCs und hier.",
            UI.Btn("Überall abmelden", SymbolRegular.SignOut24, async () =>
            {
                if (!await UI.Confirm("Überall abmelden", "Alle Sitzungen deines Kontos werden beendet – auch diese. Danach musst du dich neu anmelden.", "Überall abmelden")) return;
                await App.Api.Call(HttpMethod.Post, "/api/logout-all");
                App.ShowLogin(autoLogin: false);
            })));

        // --- Sicherung ---
        if (rw)
        {
            Body.Children.Add(Section("Sicherung"));
            Body.Children.Add(UI.SettingCard(SymbolRegular.ArrowDownload24, "Sicherung herunterladen",
                "Komplette Konfiguration mit allen Geheimnissen und Zertifikaten, verschlüsselt mit einem Passwort.",
                UI.Btn("Herunterladen …", SymbolRegular.Save24, Backup)));
            Body.Children.Add(UI.SettingCard(SymbolRegular.ArrowUpload24, "Sicherung wiederherstellen",
                "Ersetzt alles auf diesem Server durch den Stand der Sicherung.",
                UI.Btn("Wiederherstellen …", SymbolRegular.History24, Restore)));
        }

        Body.Children.Add(Section("Info"));
        var ver = typeof(App).Assembly.GetName().Version;
        Body.Children.Add(UI.SettingCard(SymbolRegular.Info24, "EasyRadius", $"Server-Version {App.State.Version} ({(App.State.Platform == "windows" ? "Windows" : "Linux")}) · Verwaltung {ver?.ToString(3)}", null));
        var methods = "WPA2/WPA3-Enterprise (PEAP-MSCHAPv2" + (s.Eap.Ttls ? ", EAP-TTLS" : "") + (s.Eap.Tls ? ", EAP-TLS" : "") + "), PAP, MS-CHAPv2, MAC-Adressen (MAB), VLAN, Check-/Reply-Attribute, Accounting";
        Body.Children.Add(UI.SettingCard(SymbolRegular.Wifi124, "Unterstützte Verfahren", methods, null));
    }

    static async Task ChangePassword()
    {
        var f = new Form();
        var cur = f.Password("Aktuelles Passwort");
        var n1 = f.Password("Neues Passwort", null, "8 bis 72 Zeichen. Deine anderen Sitzungen werden dabei beendet.");
        var n2 = f.Password("Neues Passwort wiederholen");
        var dlg = new FormDialog("Passwort ändern", f.Panel, "Ändern");
        dlg.OnSave = async () =>
        {
            if (n1.Password != n2.Password) throw new InvalidOperationException("Die neuen Passwörter stimmen nicht überein.");
            await App.Api.Call(HttpMethod.Post, "/api/admin/password", new { current = cur.Password, @new = n1.Password });
            var saved = SavedLogin.Load();
            if (saved.Password != null) { saved.Password = n1.Password; saved.Save(); }
        };
        if (await dlg.ShowAsync() == ContentDialogResult.Primary) await UI.Toast("Passwort geändert");
    }

    const string BackupFilter = "EasyRadius-Sicherung (*.erbackup)|*.erbackup|Alle Dateien|*.*";

    static async Task Backup()
    {
        var f = new Form();
        f.Note("Die Sicherung enthält die komplette Konfiguration mit allen Passwörtern, Shared Secrets und Zertifikaten. "
            + "Sie wird mit dem Passwort verschlüsselt – ohne dieses Passwort lässt sie sich nicht wiederherstellen.");
        var p1 = f.Password("Passwort für die Sicherung", null, "Mindestens 8 Zeichen. Gut aufbewahren!");
        var p2 = f.Password("Passwort wiederholen");
        string? saved = null;
        var dlg = new FormDialog("Sicherung herunterladen", f.Panel, "Speichern unter …");
        dlg.OnSave = async () =>
        {
            if (p1.Password.Length < 8) throw new InvalidOperationException("Das Passwort muss mindestens 8 Zeichen haben.");
            if (p1.Password != p2.Password) throw new InvalidOperationException("Die Passwörter stimmen nicht überein.");
            var file = Files.SaveAs($"EasyRadius-Sicherung-{DateTime.Now:yyyy-MM-dd}.erbackup", BackupFilter);
            if (file == null) throw new KeepOpen();
            var (data, _) = await ApiException.NewFeature(() => App.Api.Download(HttpMethod.Post, "/api/backup", new { password = p1.Password }));
            await File.WriteAllBytesAsync(file, data);
            saved = file;
        };
        if (await dlg.ShowAsync() == ContentDialogResult.Primary && saved != null)
            await UI.Toast($"Sicherung gespeichert: {Path.GetFileName(saved)}");
    }

    static async Task Restore()
    {
        var file = Files.Open("Sicherung wählen", BackupFilter);
        if (file == null) return;
        var f = new Form();
        f.Add("", new InfoBar
        {
            Title = "Alles wird ersetzt", Severity = InfoBarSeverity.Warning, IsOpen = true, IsClosable = false,
            Message = "Benutzer, Gruppen, MAC-Adressen, Geräte, Konten, Einstellungen und Zertifikate dieses Servers werden durch den Stand der Sicherung ersetzt. "
                + "Alle Anmeldungen an der Verwaltung enden – danach meldest du dich neu an (mit den Konten aus der Sicherung).",
        });
        f.Note("Datei: " + Path.GetFileName(file));
        var pw = f.Password("Passwort der Sicherung");
        var dlg = new FormDialog("Sicherung wiederherstellen", f.Panel, "Wiederherstellen") { PrimaryButtonAppearance = ControlAppearance.Danger };
        dlg.OnSave = async () =>
        {
            if (pw.Password == "") throw new InvalidOperationException("Bitte das Passwort der Sicherung eingeben.");
            var content = new MultipartFormDataContent
            {
                { new ByteArrayContent(await File.ReadAllBytesAsync(file)), "file", Path.GetFileName(file) },
                { new StringContent(pw.Password), "password" },
            };
            await ApiException.NewFeature(() => App.Api.Call(HttpMethod.Post, "/api/restore", content));
        };
        if (await dlg.ShowAsync() != ContentDialogResult.Primary) return;
        App.ShowLogin("Die Sicherung wurde wiederhergestellt. Bitte neu anmelden.", autoLogin: false, info: true);
    }
}
