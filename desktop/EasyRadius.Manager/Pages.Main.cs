using System.Net.Http;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Controls.Primitives;
using System.Windows.Media;
using System.Windows.Shapes;
using Wpf.Ui.Controls;
using Button = Wpf.Ui.Controls.Button;
using ListView = Wpf.Ui.Controls.ListView;
using TextBlock = System.Windows.Controls.TextBlock;
using TextBox = Wpf.Ui.Controls.TextBox;

namespace EasyRadius.Manager;

// ===================== Übersicht =====================
public class DashboardPage : PageBase
{
    readonly System.Windows.Threading.DispatcherTimer timer = new() { Interval = TimeSpan.FromSeconds(15) };

    public DashboardPage() : base("Übersicht", "Alles Wichtige auf einen Blick")
    {
        timer.Tick += async (_, _) => await SafeReload();
        Loaded += (_, _) => timer.Start();
        Unloaded += (_, _) => timer.Stop();
    }

    static Card Stat(SymbolRegular icon, string label, string value, string hint, Brush? accent = null)
    {
        var sp = new StackPanel();
        var head = UI.Hstack(new SymbolIcon(icon) { FontSize = 18, Foreground = accent ?? UI.Brush("AccentTextFillColorPrimaryBrush"), Margin = new Thickness(0, 0, 8, 0) },
            new TextBlock { Text = label, Style = UI.Style("Muted"), VerticalAlignment = VerticalAlignment.Center });
        sp.Children.Add(head);
        sp.Children.Add(new TextBlock { Text = value, FontSize = 28, FontWeight = FontWeights.SemiBold, Margin = new Thickness(0, 8, 0, 0), Foreground = UI.Brush("TextFillColorPrimaryBrush") });
        sp.Children.Add(new TextBlock { Text = hint, Style = UI.Style("Muted") });
        return new Card { Content = sp, Margin = new Thickness(0, 0, 8, 8), Padding = new Thickness(18, 14, 18, 14), VerticalAlignment = VerticalAlignment.Stretch };
    }

    protected override async Task Reload()
    {
        var d = await App.Api.Get<Dashboard>("/api/dashboard");
        Body.Children.Clear();
        var ip = d.Ips.FirstOrDefault() ?? "–";

        if (!d.Status.Running)
            Body.Children.Add(new InfoBar { Title = "Der RADIUS-Dienst läuft nicht", Message = d.Status.Error, Severity = InfoBarSeverity.Error, IsOpen = true, IsClosable = false, Margin = new Thickness(0, 0, 0, 8) });
        foreach (var u in d.Unknown)
        {
            var bar = new InfoBar
            {
                Title = $"Unbekanntes Gerät {u.Ip}",
                Message = $"fragt an ({Fmt.Rel(u.LastSeen)}). Anfragen werden ignoriert, bis es freigegeben ist.",
                Severity = InfoBarSeverity.Warning, IsOpen = true, IsClosable = false, Margin = new Thickness(0, 0, 0, 8),
            };
            Body.Children.Add(bar);
            Body.Children.Add(UI.Btn("Gerät freigeben", SymbolRegular.Add24, async () =>
            {
                if (await ClientsPage.Edit(new Client { Address = u.Ip })) await Reload();
            }, ControlAppearance.Primary));
        }

        var stats = new UniformGrid { Columns = 4, Margin = new Thickness(0, 8, -8, 0) };
        stats.Children.Add(Stat(SymbolRegular.Power24, "Status", d.Status.Running ? "Läuft" : "Gestoppt", $"{ip} · UDP {d.Status.AuthPort}/{d.Status.AcctPort}",
            Table.StatusBrush(d.Status.Running)));
        stats.Children.Add(Stat(SymbolRegular.People24, "Benutzer", d.Users.ToString(), $"{d.ActiveUsers} aktiv · {d.Groups} Gruppen"));
        stats.Children.Add(Stat(SymbolRegular.CheckmarkCircle24, "Erfolgreich (24 h)", d.Stats.Accepted24h.ToString(), "Anmeldungen", Table.StatusBrush(true)));
        stats.Children.Add(Stat(SymbolRegular.DismissCircle24, "Abgelehnt (24 h)", d.Stats.Rejected24h.ToString(), "Anmeldungen", Table.StatusBrush(false)));
        Body.Children.Add(stats);

        var grid = new Grid { Margin = new Thickness(0, 0, 0, 0) };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1.5, GridUnitType.Star) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        var left = new StackPanel { Margin = new Thickness(0, 0, 8, 0) };
        var right = new StackPanel();
        Grid.SetColumn(right, 1);
        grid.Children.Add(left);
        grid.Children.Add(right);
        Body.Children.Add(grid);

        // Diagramm
        var chart = new UniformGrid { Rows = 1, Height = 130 };
        var max = Math.Max(1, d.Stats.Hours.Max(h => h.Accepted + h.Rejected));
        foreach (var h in d.Stats.Hours)
        {
            var col = new StackPanel { VerticalAlignment = VerticalAlignment.Bottom, Margin = new Thickness(1.5, 0, 1.5, 0), ToolTip = $"{h.Start.ToLocalTime():HH}:00 Uhr – {h.Accepted} angenommen, {h.Rejected} abgelehnt", Background = Brushes.Transparent };
            col.Children.Add(new Rectangle { Height = 130.0 * h.Rejected / max, Fill = Table.StatusBrush(false), RadiusX = 2, RadiusY = 2 });
            col.Children.Add(new Rectangle { Height = 130.0 * h.Accepted / max, Fill = UI.Brush("AccentFillColorDefaultBrush"), RadiusX = 2, RadiusY = 2 });
            chart.Children.Add(col);
        }
        var chartBox = new StackPanel();
        chartBox.Children.Add(new TextBlock { Text = "Anmeldungen der letzten 24 Stunden", FontWeight = FontWeights.SemiBold, Margin = new Thickness(0, 0, 0, 12), Foreground = UI.Brush("TextFillColorPrimaryBrush") });
        chartBox.Children.Add(new Border { Child = chart, Height = 130 });
        var axis = new Grid { Margin = new Thickness(0, 6, 0, 0) };
        axis.Children.Add(new TextBlock { Text = "vor 24 h", Style = UI.Style("Muted") });
        axis.Children.Add(new TextBlock { Text = "jetzt", Style = UI.Style("Muted"), HorizontalAlignment = HorizontalAlignment.Right });
        chartBox.Children.Add(axis);
        left.Children.Add(CardOf(chartBox));

        var recent = new StackPanel();
        recent.Children.Add(new TextBlock { Text = "Letzte Ereignisse", FontWeight = FontWeights.SemiBold, Margin = new Thickness(0, 0, 0, 8), Foreground = UI.Brush("TextFillColorPrimaryBrush") });
        foreach (var e in d.Recent) recent.Children.Add(LogPage.Row(e, false));
        if (d.Recent.Count == 0) recent.Children.Add(new TextBlock { Text = "Noch keine Ereignisse.", Style = UI.Style("Muted") });
        left.Children.Add(CardOf(recent));

        var online = new StackPanel();
        online.Children.Add(new TextBlock { Text = "Jetzt verbunden", FontWeight = FontWeights.SemiBold, Margin = new Thickness(0, 0, 0, 8), Foreground = UI.Brush("TextFillColorPrimaryBrush") });
        foreach (var o in d.Online.Take(12))
        {
            var sp = new StackPanel { Margin = new Thickness(0, 4, 0, 4) };
            sp.Children.Add(new TextBlock { Text = o.User, FontWeight = FontWeights.SemiBold, Foreground = UI.Brush("TextFillColorPrimaryBrush") });
            sp.Children.Add(new TextBlock { Text = $"{o.Meta} · {o.StartedText}", Style = UI.Style("Muted") });
            online.Children.Add(sp);
        }
        if (d.Online.Count == 0) online.Children.Add(new TextBlock { Text = $"Keine aktiven Sitzungen bekannt. Damit sie erscheinen, am Access Point RADIUS-Accounting (Port {d.Status.AcctPort}) aktivieren.", Style = UI.Style("Muted") });
        right.Children.Add(CardOf(online));

        var cert = new StackPanel();
        cert.Children.Add(new TextBlock { Text = "Zertifikat", FontWeight = FontWeights.SemiBold, Foreground = UI.Brush("TextFillColorPrimaryBrush") });
        cert.Children.Add(new TextBlock { Text = d.Cert.Subject, Margin = new Thickness(0, 8, 0, 0), Foreground = UI.Brush("TextFillColorPrimaryBrush") });
        cert.Children.Add(new TextBlock { Text = $"gültig bis {d.Cert.NotAfter.ToLocalTime():dd.MM.yyyy}", Style = UI.Style("Muted") });
        right.Children.Add(CardOf(cert));
    }
}

// ===================== Benutzer =====================
public class UsersPage : PageBase
{
    readonly ListView list;
    readonly TextBox search = new() { PlaceholderText = "Suchen …", Width = 240, Icon = new SymbolIcon(SymbolRegular.Search24), Margin = new Thickness(0, 0, 8, 0) };
    List<User> users = [];

    public UsersPage() : base("Benutzer", "Personen, die sich am WLAN oder an Geräten anmelden dürfen")
    {
        list = Table.Create<User>([
            new("Benutzer", nameof(User.Username), 230, nameof(User.SubText), Bold: true),
            new("Status", nameof(User.StatusText), 100, ColorPath: nameof(User.StatusColor)),
            new("Gruppe", nameof(User.GroupName), 150, nameof(User.AttrText)),
            new("VLAN", nameof(User.VlanText), 160),
            new("Läuft ab", nameof(User.ExpiresText), 100),
            new("Letzte Anmeldung", nameof(User.LastLoginText), 140),
        ], async u => { if (await Edit(u)) await Reload(); },
            ("Bearbeiten", async u => { if (await Edit(u)) await Reload(); }),
            ("Löschen", Delete));
        Body.Children.Add(CardOf(list, new Thickness(0)));
        search.TextChanged += (_, _) => Filter();
        Actions(search,
            UI.Btn("Anmeldung testen", SymbolRegular.Key24, TestLogin),
            UI.Btn("Benutzer hinzufügen", SymbolRegular.PersonAdd24, async () => { if (await Edit(null)) await Reload(); }, ControlAppearance.Primary));
    }

    protected override async Task Reload()
    {
        var groupsTask = App.Api.Get<List<Group>>("/api/groups");
        users = await App.Api.Get<List<User>>("/api/users");
        var groups = await groupsTask;
        foreach (var u in users)
        {
            var g = groups.FirstOrDefault(x => x.Id == u.GroupId);
            u.GroupName = g?.Name ?? "–";
            u.VlanText = u.Vlan != "" ? "VLAN " + u.Vlan : g?.Vlan is { Length: > 0 } gv ? $"VLAN {gv} (Gruppe)" : "Standard";
        }
        Filter();
    }

    void Filter()
    {
        var q = search.Text.Trim().ToLowerInvariant();
        list.ItemsSource = users.Where(u => q == "" || $"{u.Username} {u.FullName} {u.Note} {u.GroupName} {u.Vlan}".ToLowerInvariant().Contains(q)).ToList();
    }

    async Task Delete(User u)
    {
        if (!await UI.Confirm("Benutzer löschen", $"Soll „{u.Username}“ wirklich gelöscht werden? Der Benutzer kann sich danach nicht mehr anmelden.", "Löschen")) return;
        await App.Api.Call(HttpMethod.Delete, "/api/users/" + u.Id);
        await UI.Toast("Benutzer gelöscht");
        await Reload();
    }

    public static async Task<bool> Edit(User? u)
    {
        bool isNew = u == null;
        u ??= new User();
        var dict = await AttributeEditor.Dictionary();
        var groups = await App.Api.Get<List<Group>>("/api/groups");

        var f = new Form();
        var name = new TextBox { Text = u.Username };
        var full = new TextBox { Text = u.FullName };
        f.Row(("Benutzername", name), ("Name (optional)", full));
        var pw = new TextBox { PlaceholderText = isNew ? "" : "leer lassen = unverändert" };
        var gen = new Button { Icon = new SymbolIcon(SymbolRegular.ArrowShuffle24), ToolTip = "Zufällig erzeugen", Margin = new Thickness(6, 0, 0, 0) };
        gen.Click += (_, _) => pw.Text = UI.RandomPassword();
        var pwRow = new DockPanel();
        DockPanel.SetDock(gen, Dock.Right);
        pwRow.Children.Add(gen);
        pwRow.Children.Add(pw);
        f.Add(isNew ? "Passwort" : "Neues Passwort", pwRow);
        var groupItems = new List<object> { "Keine Gruppe" }.Concat(groups).ToList();
        var group = f.Combo("Gruppe", groupItems, groups.FirstOrDefault(g => g.Id == u.GroupId) ?? groupItems[0], "Der Benutzer übernimmt VLAN, Regeln und Attribute der Gruppe.");
        var vlan = new TextBox { Text = u.Vlan, PlaceholderText = "z. B. 20" };
        var exp = new DatePicker();
        if (DateTime.TryParse(u.ExpiresAt, out var ed)) exp.SelectedDate = ed;
        f.Row(("Eigenes VLAN (optional)", vlan), ("Gültig bis (optional)", exp));
        var note = f.Text("Notiz", u.Note, "z. B. Laptop Büro");
        var enabled = f.Toggle("Benutzer darf sich anmelden", isNew || u.Enabled);
        f.Note("Check- und Reply-Attribute", true);
        f.Note("Check = Bedingung an die Anfrage des Switches/Access Points. Reply = wird bei Erfolg zurückgeschickt. Gleichnamige Attribute der Gruppe werden ersetzt.");
        var ed2 = new AttributeEditor(dict, u.Attributes);
        f.Add("", ed2);

        var dlg = new FormDialog(isNew ? "Benutzer hinzufügen" : "Benutzer bearbeiten", f.Panel, isNew ? "Anlegen" : "Speichern");
        string? shownPw = null;
        dlg.OnSave = async () =>
        {
            if (isNew && pw.Text.Trim() == "") throw new InvalidOperationException("Bitte ein Passwort vergeben (oder mit dem Würfel erzeugen).");
            var body = new
            {
                username = name.Text.Trim(), fullName = full.Text.Trim(), password = pw.Text.Trim(),
                groupId = (group.SelectedItem as Group)?.Id ?? "", vlan = vlan.Text.Trim(),
                expiresAt = exp.SelectedDate?.ToString("yyyy-MM-dd") ?? "", note = note.Text.Trim(),
                enabled = enabled.IsChecked == true, attributes = ed2.Collect(),
            };
            if (isNew) await App.Api.Call(HttpMethod.Post, "/api/users", body);
            else await App.Api.Call(HttpMethod.Put, "/api/users/" + u.Id, body);
            shownPw = body.password != "" ? body.password : null;
        };
        if (await dlg.ShowAsync() != ContentDialogResult.Primary) return false;
        if (shownPw != null) await ShowCredentials(name.Text.Trim(), shownPw);
        else await UI.Toast("Gespeichert");
        return true;
    }

    static async Task ShowCredentials(string user, string pw)
    {
        var f = new Form();
        f.Add("", new InfoBar { Title = "Gespeichert", Message = "Das Passwort wird aus Sicherheitsgründen nur jetzt angezeigt.", Severity = InfoBarSeverity.Success, IsOpen = true, IsClosable = false });
        f.Add("Benutzername", UI.Hstack(new TextBlock { Text = user, FontFamily = new FontFamily("Cascadia Mono, Consolas"), FontSize = 16, VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(0, 0, 12, 0) },
            UI.Btn("Kopieren", SymbolRegular.Copy24, () => { UI.Copy(user); return Task.CompletedTask; })));
        f.Add("Passwort", UI.Hstack(new TextBlock { Text = pw, FontFamily = new FontFamily("Cascadia Mono, Consolas"), FontSize = 16, VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(0, 0, 12, 0) },
            UI.Btn("Kopieren", SymbolRegular.Copy24, () => { UI.Copy(pw); return Task.CompletedTask; })));
        f.Note("Beim Verbinden mit dem WLAN: Methode PEAP, Phase 2 MSCHAPv2. Eine Anleitung für jedes Gerät gibt es unter „Zertifikat“ (QR-Code).");
        await UI.Info("Zugangsdaten", f.Panel);
    }

    public static async Task TestLogin()
    {
        var f = new Form();
        f.Note("Prüft Benutzername und Passwort genau wie bei einer echten Anmeldung – lokal und im Active Directory.");
        var user = f.Text("Benutzername", "", @"max oder FIRMA\max");
        var pass = f.Password("Passwort");
        var result = new StackPanel { Margin = new Thickness(0, 12, 0, 0) };
        f.Add("", result);
        var dlg = new FormDialog("Anmeldung testen", f.Panel, "Testen");
        dlg.OnSave = async () =>
        {
            var r = await App.Api.Call<TestResult>(HttpMethod.Post, "/api/users/test", new { username = user.Text.Trim(), password = pass.Password });
            result.Children.Clear();
            result.Children.Add(new InfoBar { Title = r.Ok ? "Erfolgreich" : "Abgelehnt", Message = r.Message, Severity = r.Ok ? InfoBarSeverity.Success : InfoBarSeverity.Error, IsOpen = true, IsClosable = false });
            if (r.Source != "")
            {
                var lines = new List<string> { $"Quelle: {r.Source}" };
                if (r.Ok) { lines.Add($"Gruppe: {(r.Group == "" ? "keine" : r.Group)}"); lines.Add($"VLAN: {(r.Vlan == "" ? "Standard" : r.Vlan)}"); }
                lines.Add($"WLAN (PEAP): {(r.WlanOk ? "funktioniert" : "nicht möglich – " + r.WlanNote)}");
                if (r.Groups is { Count: > 0 }) lines.Add("AD-Gruppen: " + string.Join(", ", r.Groups));
                result.Children.Add(new TextBlock { Text = string.Join("\n", lines), Margin = new Thickness(0, 8, 0, 0), TextWrapping = TextWrapping.Wrap, Foreground = UI.Brush("TextFillColorPrimaryBrush") });
            }
            throw new KeepOpen();
        };
        await dlg.ShowAsync();
    }
}

/// <summary>Hält einen Dialog nach erfolgreicher Aktion offen (z. B. Testergebnis anzeigen).</summary>
public class KeepOpen : InvalidOperationException { public KeepOpen() : base("") { } }
