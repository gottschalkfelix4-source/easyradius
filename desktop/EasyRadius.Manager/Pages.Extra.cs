using System.Globalization;
using System.Net.Http;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Threading;
using Wpf.Ui.Controls;
using ListView = Wpf.Ui.Controls.ListView;
using TextBlock = System.Windows.Controls.TextBlock;
using TextBox = Wpf.Ui.Controls.TextBox;

namespace EasyRadius.Manager;

// ===================== MAC-Adressen (MAB) =====================
public class MacsPage : PageBase
{
    readonly ListView list;
    readonly TextBox search = new() { PlaceholderText = "Suchen …", Width = 240, Icon = new SymbolIcon(SymbolRegular.Search24), Margin = new Thickness(0, 0, 8, 0) };
    List<MacEntry> macs = [];

    const string ImportHelp = "Spalten (Trennzeichen „;“ oder „,“, erste Zeile = Überschriften): MAC; Name; Gruppe; VLAN; Aktiv (ja/nein); Läuft ab (JJJJ-MM-TT); Notiz.\n\n"
        + "Die MAC-Adresse darf in jeder üblichen Schreibweise angegeben werden. Vorhandene Adressen werden aktualisiert, die Gruppe wird über ihren Namen zugeordnet. "
        + "Bei einem Fehler wird nichts übernommen – die Meldung nennt die Zeile.";

    public MacsPage() : base("MAC-Adressen", "Drucker, Telefone, Kameras und andere Geräte ohne 802.1X – Anmeldung anhand der MAC-Adresse")
    {
        list = Table.Create<MacEntry>([
            new("MAC-Adresse", nameof(MacEntry.Mac), 200, nameof(MacEntry.SubText), Mono: true),
            new("Status", nameof(MacEntry.StatusText), 100, ColorPath: nameof(MacEntry.StatusColor)),
            new("Gruppe", nameof(MacEntry.GroupName), 160),
            new("VLAN", nameof(MacEntry.VlanText), 160),
            new("Läuft ab", nameof(MacEntry.ExpiresText), 100),
            new("Zuletzt gesehen", nameof(MacEntry.LastSeenText), 140),
        ], App.CanWrite ? async m => { if (await Edit(m)) await Reload(); } : null,
            Table.Writable<MacEntry>(
                ("Bearbeiten", async m => { if (await Edit(m)) await Reload(); }),
                ("Löschen", Delete)));
        Body.Children.Add(CardOf(list, new Thickness(0)));
        Body.Children.Add(new TextBlock
        {
            Text = "MAC Authentication Bypass (MAB): Der Switch bzw. Access Point schickt die MAC-Adresse des Geräts als Benutzernamen. "
                + "Ist sie hier eingetragen und aktiv, wird das Gerät zugelassen – mit VLAN und Attributen von Eintrag oder Gruppe. "
                + "Die Schreibweise ist egal (aa:bb:cc:dd:ee:ff, AA-BB-CC-DD-EE-FF, aabb.ccdd.eeff).",
            Style = UI.Style("Muted"), Margin = new Thickness(0, 8, 0, 0),
        });
        search.TextChanged += (_, _) => Filter();
        Actions(search,
            UI.MenuBtn("CSV", SymbolRegular.DocumentTable24,
                ("Als CSV exportieren …", () => Files.ExportCsv("/api/macs.csv", "EasyRadius-MAC-Adressen.csv")),
                (App.CanWrite ? "Aus CSV importieren …" : "", async () => { if (await Files.ImportCsv("/api/macs/import", "MAC-Adressen", ImportHelp)) await Reload(); })),
            UI.Btn("MAC-Adresse hinzufügen", SymbolRegular.Add24, async () => { if (await Edit(null)) await Reload(); }, ControlAppearance.Primary).AdminOnly());
    }

    protected override async Task Reload()
    {
        var groupsTask = ApiException.NewFeature(() => App.Api.Get<List<Group>>("/api/groups"));
        macs = await ApiException.NewFeature(() => App.Api.Get<List<MacEntry>>("/api/macs"));
        var groups = await groupsTask;
        foreach (var m in macs)
        {
            var g = groups.FirstOrDefault(x => x.Id == m.GroupId);
            m.GroupName = g?.Name ?? "–";
            m.VlanText = m.Vlan != "" ? "VLAN " + m.Vlan : g?.Vlan is { Length: > 0 } gv ? $"VLAN {gv} (Gruppe)" : "Standard";
        }
        Filter();
    }

    void Filter()
    {
        var q = search.Text.Trim().ToLowerInvariant();
        var plain = new string(q.Where(char.IsLetterOrDigit).ToArray());
        list.ItemsSource = macs.Where(m => q == ""
            || $"{m.Mac} {m.Name} {m.Note} {m.GroupName} {m.Vlan}".ToLowerInvariant().Contains(q)
            || (plain.Length >= 4 && m.Mac.Replace("-", "").Contains(plain))).ToList();
    }

    async Task Delete(MacEntry m)
    {
        if (!await UI.Confirm("MAC-Adresse löschen", $"Soll „{m.Mac}“{(m.Name != "" ? $" ({m.Name})" : "")} gelöscht werden? Das Gerät wird danach abgelehnt.", "Löschen")) return;
        await App.Api.Call(HttpMethod.Delete, "/api/macs/" + Uri.EscapeDataString(m.Id));
        await UI.Toast("MAC-Adresse gelöscht");
        await Reload();
    }

    public static async Task<bool> Edit(MacEntry? m)
    {
        bool isNew = m == null;
        m ??= new MacEntry();
        var dict = await AttributeEditor.Dictionary();
        var groups = await App.Api.Get<List<Group>>("/api/groups");

        var f = new Form();
        var mac = new TextBox { Text = m.Mac, PlaceholderText = "aa:bb:cc:dd:ee:ff", FontFamily = new FontFamily("Cascadia Mono, Consolas") };
        var name = new TextBox { Text = m.Name, PlaceholderText = "z. B. Drucker Flur" };
        f.Row(("MAC-Adresse", mac), ("Name (optional)", name));
        var groupItems = new List<object> { "Keine Gruppe" }.Concat(groups).ToList();
        var group = f.Combo("Gruppe", groupItems, groups.FirstOrDefault(g => g.Id == m.GroupId) ?? groupItems[0], "Das Gerät übernimmt VLAN, Regeln und Attribute der Gruppe.");
        var vlan = new TextBox { Text = m.Vlan, PlaceholderText = "z. B. 40" };
        var exp = new DatePicker();
        if (DateTime.TryParse(m.ExpiresAt, out var ed)) exp.SelectedDate = ed;
        f.Row(("Eigenes VLAN (optional)", vlan), ("Gültig bis (optional)", exp));
        var note = f.Text("Notiz", m.Note, "z. B. Raum 2.14");
        var enabled = f.Toggle("Gerät darf sich anmelden", isNew || m.Enabled);
        f.Note("Check- und Reply-Attribute", true);
        f.Note("Gleichnamige Attribute der Gruppe werden ersetzt.");
        var attrs = new AttributeEditor(dict, m.Attributes);
        f.Add("", attrs);

        var dlg = new FormDialog(isNew ? "MAC-Adresse hinzufügen" : "MAC-Adresse bearbeiten", f.Panel, isNew ? "Anlegen" : "Speichern");
        dlg.OnSave = async () =>
        {
            if (mac.Text.Trim() == "") throw new InvalidOperationException("Bitte die MAC-Adresse eintragen.");
            var body = new
            {
                mac = mac.Text.Trim(), name = name.Text.Trim(), enabled = enabled.IsChecked == true, vlan = vlan.Text.Trim(),
                groupId = (group.SelectedItem as Group)?.Id ?? "", attributes = attrs.Collect(), note = note.Text.Trim(),
                expiresAt = exp.SelectedDate?.ToString("yyyy-MM-dd") ?? "",
            };
            if (isNew) await App.Api.Call(HttpMethod.Post, "/api/macs", body);
            else await App.Api.Call(HttpMethod.Put, "/api/macs/" + Uri.EscapeDataString(m.Id), body);
        };
        if (await dlg.ShowAsync() != ContentDialogResult.Primary) return false;
        await UI.Toast(isNew ? "MAC-Adresse angelegt" : "Gespeichert");
        return true;
    }
}

// ===================== Sitzungen =====================
public class SessionsPage : PageBase
{
    readonly ListView online, acct, locks;
    readonly TextBlock onlineEmpty = Empty("Keine aktiven Sitzungen bekannt. Damit sie erscheinen, am Access Point RADIUS-Accounting aktivieren.");
    readonly TextBlock acctEmpty = Empty("Für diesen Monat gibt es keine abgeschlossenen Sitzungen.");
    readonly TextBlock locksEmpty = Empty("Zurzeit ist niemand gesperrt.");
    readonly TextBlock acctSum = new() { Style = UI.Style("Muted"), VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(12, 0, 0, 0) };
    readonly ComboBox month = new() { Width = 180, Margin = new Thickness(0, 0, 8, 0) };
    readonly DispatcherTimer timer = new() { Interval = TimeSpan.FromSeconds(15) };
    bool fillingMonths;

    record MonthItem(string Key)
    {
        public override string ToString() =>
            DateTime.TryParseExact(Key, "yyyy-MM", CultureInfo.InvariantCulture, DateTimeStyles.None, out var d)
                ? d.ToString("MMMM yyyy", CultureInfo.GetCultureInfo("de-DE")) : Key;
    }

    public SessionsPage() : base("Sitzungen", "Verbundene Geräte, Sitzungsverlauf (Accounting) und Sperren nach Fehlversuchen")
    {
        online = Table.Create<Online>([
            new("Benutzer", nameof(Online.User), 220, nameof(Online.Meta), Bold: true),
            new("Gerät", nameof(Online.Client), 180, nameof(Online.NasAddr)),
            new("Dauer", nameof(Online.DurationText), 140, nameof(Online.StartedText)),
            new("Datenmenge", nameof(Online.TrafficText), 200),
        ], null, Table.Writable<Online>(("Trennen", Disconnect)));
        online.MaxHeight = 380;

        acct = Table.Create<AcctRecord>([
            new("Benutzer", nameof(AcctRecord.User), 200, nameof(AcctRecord.Meta), Bold: true),
            new("Gerät", nameof(AcctRecord.Client), 160),
            new("Beginn", nameof(AcctRecord.StartText), 140),
            new("Dauer", nameof(AcctRecord.DurationText), 120),
            new("Datenmenge", nameof(AcctRecord.TrafficText), 180),
            new("Grund", nameof(AcctRecord.Cause), 140),
        ]);
        acct.MaxHeight = 420;

        locks = Table.Create<Lockout>([
            new("Gesperrt", nameof(Lockout.Name), 280, nameof(Lockout.KindText), Bold: true),
            new("Bis", nameof(Lockout.UntilText), 180),
        ], null, Table.Writable<Lockout>(("Entsperren", Unlock)));
        locks.MaxHeight = 300;

        Body.Children.Add(new TextBlock { Text = "Jetzt verbunden", Style = UI.Style("SectionTitle"), Margin = new Thickness(0, 0, 0, 8) });
        Body.Children.Add(CardOf(WithEmpty(online, onlineEmpty), new Thickness(0)));
        Body.Children.Add(new TextBlock { Text = "Rechtsklick → „Trennen“ schickt dem Gerät einen Disconnect-Request (RFC 5176) – es muss das unterstützen und den Server als CoA-Client kennen.", Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 0) });

        Body.Children.Add(Section("Sitzungsverlauf (Accounting)"));
        month.SelectionChanged += async (_, _) =>
        {
            if (fillingMonths || month.SelectedItem is not MonthItem m) return;
            await Safe(() => LoadAccounting(m.Key));
        };
        Body.Children.Add(UI.Hstack(month,
            UI.Btn("Als CSV exportieren …", SymbolRegular.ArrowDownload24, async () =>
            {
                if (month.SelectedItem is not MonthItem m) throw new ApiException("Es gibt noch keinen Monat mit Sitzungsdaten.");
                await Files.ExportCsv("/api/accounting.csv?month=" + Uri.EscapeDataString(m.Key), $"EasyRadius-Accounting-{m.Key}.csv");
            }), acctSum));
        Body.Children.Add(new Border { Height = 8 });
        Body.Children.Add(CardOf(WithEmpty(acct, acctEmpty), new Thickness(0)));

        Body.Children.Add(Section("Sperren nach Fehlversuchen"));
        Body.Children.Add(UI.Btn("Alle entsperren", SymbolRegular.LockOpen24, async () =>
        {
            await App.Api.Call(HttpMethod.Post, "/api/lockouts/unlock", new { key = "" });
            await UI.Toast("Alle Sperren aufgehoben");
            await LoadLockouts();
        }).AdminOnly());
        Body.Children.Add(new Border { Height = 8 });
        Body.Children.Add(CardOf(WithEmpty(locks, locksEmpty), new Thickness(0)));

        Actions(UI.Btn("Aktualisieren", SymbolRegular.ArrowSync24, SafeReload));
        timer.Tick += async (_, _) => await Safe(async () => { await LoadOnline(); await LoadLockouts(); });
        Loaded += (_, _) => timer.Start();
        Unloaded += (_, _) => timer.Stop();
    }

    static TextBlock Empty(string text) => new() { Text = text, Style = UI.Style("Muted"), Margin = new Thickness(16, 56, 16, 0), HorizontalAlignment = HorizontalAlignment.Center, TextAlignment = TextAlignment.Center, IsHitTestVisible = false };

    static Grid WithEmpty(ListView lv, TextBlock empty)
    {
        var g = new Grid();
        g.Children.Add(lv);
        g.Children.Add(empty);
        return g;
    }

    static async Task Safe(Func<Task> f)
    {
        try { await f(); }
        catch (Exception e) when (UI.IsExpected(e)) { UI.ReportError(e); }
    }

    protected override async Task Reload()
    {
        var a = LoadOnline();
        var b = LoadLockouts();
        var c = LoadAccounting((month.SelectedItem as MonthItem)?.Key);
        await Task.WhenAll(a, b, c);
    }

    async Task LoadOnline()
    {
        var list = await ApiException.NewFeature(() => App.Api.Get<List<Online>>("/api/online"));
        online.ItemsSource = list.OrderBy(o => o.User, StringComparer.OrdinalIgnoreCase).ToList();
        onlineEmpty.Visibility = list.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    async Task LoadLockouts()
    {
        var list = await ApiException.NewFeature(() => App.Api.Get<List<Lockout>>("/api/lockouts"));
        locks.ItemsSource = list;
        locksEmpty.Visibility = list.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    async Task LoadAccounting(string? key)
    {
        var a = await ApiException.NewFeature(() => App.Api.Get<AccountingInfo>("/api/accounting" + (key != null ? "?month=" + Uri.EscapeDataString(key) : "")));
        fillingMonths = true;
        try
        {
            var items = a.Months.Select(x => new MonthItem(x)).ToList();
            if (a.Month != "" && !items.Contains(new MonthItem(a.Month))) items.Insert(0, new MonthItem(a.Month));
            month.ItemsSource = items;
            month.SelectedItem = items.FirstOrDefault(x => x.Key == a.Month);
            month.IsEnabled = items.Count > 0;
        }
        finally { fillingMonths = false; }
        acct.ItemsSource = a.Records;
        acctEmpty.Visibility = a.Records.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
        var total = a.Records.Aggregate(0UL, (s, r) => s + r.InBytes + r.OutBytes);
        acctSum.Text = a.Records.Count == 0 ? "" : $"{a.Records.Count} Sitzungen · {Fmt.Duration(a.Records.Sum(r => r.Seconds))} · {Fmt.Bytes(total)}";
    }

    async Task Disconnect(Online o)
    {
        if (!await UI.Confirm("Sitzung trennen",
            $"Soll die Sitzung von „{o.User}“{(o.Meta != "" ? $" ({o.Meta})" : "")} getrennt werden? Das Gerät erhält einen Disconnect-Request; viele Endgeräte verbinden sich danach automatisch neu.",
            "Trennen")) return;
        await App.Api.Call(HttpMethod.Post, "/api/online/disconnect", new { key = o.Key });
        await UI.Toast("Sitzung getrennt");
        await LoadOnline();
    }

    async Task Unlock(Lockout l)
    {
        await App.Api.Call(HttpMethod.Post, "/api/lockouts/unlock", new { key = l.Key });
        await UI.Toast($"„{l.Name}“ entsperrt");
        await LoadLockouts();
    }
}

// ===================== Konten =====================
public class AccountsPage : PageBase
{
    readonly ListView admins, tokens;

    static readonly string[] Roles = ["Administrator", "Nur lesen"];
    static string RoleKey(object? item) => Equals(item, Roles[1]) ? "viewer" : "admin";

    public AccountsPage() : base("Konten", "Wer EasyRadius verwalten darf – Administratoren, Nur-Lese-Konten und API-Tokens für Skripte")
    {
        admins = Table.Create<AdminAccount>([
            new("Benutzername", nameof(AdminAccount.Username), 240, nameof(AdminAccount.SubText), Bold: true),
            new("Rolle", nameof(AdminAccount.RoleText), 140),
            new("Letzte Anmeldung", nameof(AdminAccount.LastLoginText), 160),
        ], async a => { if (await EditAdmin(a)) await Reload(); },
            ("Bearbeiten", async a => { if (await EditAdmin(a)) await Reload(); }),
            ("Löschen", DeleteAdmin));
        tokens = Table.Create<ApiToken>([
            new("Name", nameof(ApiToken.Name), 220, nameof(ApiToken.SubText), Bold: true),
            new("Kennung", nameof(ApiToken.PrefixText), 140, Mono: true),
            new("Rolle", nameof(ApiToken.RoleText), 120),
            new("Zuletzt benutzt", nameof(ApiToken.LastUsedText), 140),
            new("Läuft ab", nameof(ApiToken.ExpiresText), 100),
        ], null, ("Löschen", DeleteToken));

        Body.Children.Add(new TextBlock { Text = "Konten für Weboberfläche und Verwaltung", Style = UI.Style("SectionTitle"), Margin = new Thickness(0, 0, 0, 8) });
        Body.Children.Add(CardOf(admins, new Thickness(0)));
        Body.Children.Add(new TextBlock { Text = "„Nur lesen“ darf alles ansehen, aber nichts ändern – z. B. für den Support. Das letzte Administratorkonto lässt sich nicht löschen oder herabstufen.", Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 0) });

        Body.Children.Add(Section("API-Tokens"));
        Body.Children.Add(UI.Btn("Token erstellen", SymbolRegular.Key24, async () => { if (await CreateToken()) await Reload(); }));
        Body.Children.Add(new Border { Height = 8 });
        Body.Children.Add(CardOf(tokens, new Thickness(0)));
        Body.Children.Add(new TextBlock { Text = "Für Skripte und Automatisierung: Das Token im Header „Authorization: Bearer …“ mitsenden. Löschen sperrt den Zugriff sofort.", Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 0) });

        Actions(UI.Btn("Konto hinzufügen", SymbolRegular.PersonAdd24, async () => { if (await EditAdmin(null)) await Reload(); }, ControlAppearance.Primary));
    }

    protected override async Task Reload()
    {
        var t = ApiException.NewFeature(() => App.Api.Get<List<ApiToken>>("/api/tokens"));
        admins.ItemsSource = await ApiException.NewFeature(() => App.Api.Get<List<AdminAccount>>("/api/admins"));
        tokens.ItemsSource = await t;
    }

    static async Task<bool> EditAdmin(AdminAccount? a)
    {
        bool isNew = a == null;
        a ??= new AdminAccount();
        var f = new Form();
        var name = f.Text("Benutzername", a.Username);
        var role = f.Combo("Rolle", Roles, a.Role == "viewer" ? Roles[1] : Roles[0], "Administrator darf alles, „Nur lesen“ nur ansehen.");
        var p1 = f.Password(isNew ? "Passwort" : "Neues Passwort", isNew ? "" : "leer lassen = unverändert", "8 bis 72 Zeichen.");
        var p2 = f.Password("Passwort wiederholen");
        if (a.Self) f.Note("Das ist dein eigenes Konto. Ändert sich dein Passwort hier, werden deine anderen Sitzungen beendet.");

        var dlg = new FormDialog(isNew ? "Konto hinzufügen" : "Konto bearbeiten", f.Panel, isNew ? "Anlegen" : "Speichern");
        dlg.OnSave = async () =>
        {
            if (name.Text.Trim() == "") throw new InvalidOperationException("Bitte einen Benutzernamen eintragen.");
            if (isNew && p1.Password == "") throw new InvalidOperationException("Bitte ein Passwort vergeben.");
            if (p1.Password != p2.Password) throw new InvalidOperationException("Die Passwörter stimmen nicht überein.");
            var body = new { username = name.Text.Trim(), role = RoleKey(role.SelectedItem), password = p1.Password };
            if (isNew) await App.Api.Call(HttpMethod.Post, "/api/admins", body);
            else await App.Api.Call(HttpMethod.Put, "/api/admins/" + Uri.EscapeDataString(a.Id), body);
        };
        if (await dlg.ShowAsync() != ContentDialogResult.Primary) return false;
        if (a.Self && p1.Password != "")
        {
            var saved = SavedLogin.Load();
            if (saved.Password != null) { saved.Password = p1.Password; saved.Save(); }
        }
        await UI.Toast(isNew ? "Konto angelegt" : "Gespeichert");
        return true;
    }

    async Task DeleteAdmin(AdminAccount a)
    {
        if (a.Self) throw new ApiException("Das eigene Konto kann nicht gelöscht werden.");
        if (!await UI.Confirm("Konto löschen", $"Soll das Konto „{a.Username}“ gelöscht werden? Laufende Sitzungen dieses Kontos enden sofort.", "Löschen")) return;
        await App.Api.Call(HttpMethod.Delete, "/api/admins/" + Uri.EscapeDataString(a.Id));
        await UI.Toast("Konto gelöscht");
        await Reload();
    }

    static async Task<bool> CreateToken()
    {
        var f = new Form();
        var name = f.Text("Name", "", "z. B. Backup-Skript", "Woran du das Token später erkennst.");
        var role = f.Combo("Rolle", Roles, Roles[1], "Für reine Abfragen genügt „Nur lesen“.");
        var exp = f.Date("Gültig bis (optional)", "", "Leer = unbegrenzt gültig.");
        ApiToken? created = null;
        var dlg = new FormDialog("API-Token erstellen", f.Panel, "Erstellen");
        dlg.OnSave = async () =>
        {
            if (name.Text.Trim() == "") throw new InvalidOperationException("Bitte einen Namen eintragen.");
            created = await App.Api.Call<ApiToken>(HttpMethod.Post, "/api/tokens",
                new { name = name.Text.Trim(), role = RoleKey(role.SelectedItem), expiresAt = exp.SelectedDate?.ToString("yyyy-MM-dd") ?? "" });
        };
        if (await dlg.ShowAsync() != ContentDialogResult.Primary || created == null) return false;
        await ShowToken(created);
        return true;
    }

    /// <summary>Zeigt ein neues Token genau einmal an.</summary>
    static async Task ShowToken(ApiToken t)
    {
        var f = new Form();
        f.Add("", new InfoBar
        {
            Title = "Nur jetzt sichtbar", Severity = InfoBarSeverity.Warning, IsOpen = true, IsClosable = false,
            Message = "Das Token wird aus Sicherheitsgründen nur dieses eine Mal angezeigt und lässt sich später nicht wieder anzeigen. Kopiere es jetzt an einen sicheren Ort.",
        });
        var box = new TextBox { Text = t.Token, IsReadOnly = true, FontFamily = new FontFamily("Cascadia Mono, Consolas"), TextWrapping = TextWrapping.Wrap, ClearButtonEnabled = false };
        f.Add($"Token „{t.Name}“", box);
        var copy = UI.Btn("Kopieren", SymbolRegular.Copy24, () => { UI.CopySecret(t.Token); return Task.CompletedTask; });
        copy.Margin = new Thickness(0, 8, 0, 0);
        f.Panel.Children.Add(copy);
        f.Note("Verwendung: Header „Authorization: Bearer <Token>“.");
        await UI.Info("API-Token erstellt", f.Panel);
    }

    async Task DeleteToken(ApiToken t)
    {
        if (!await UI.Confirm("Token löschen", $"Soll das Token „{t.Name}“ gelöscht werden? Skripte, die es verwenden, verlieren sofort den Zugriff.", "Löschen")) return;
        await App.Api.Call(HttpMethod.Delete, "/api/tokens/" + Uri.EscapeDataString(t.Id));
        await UI.Toast("Token gelöscht");
        await Reload();
    }
}
