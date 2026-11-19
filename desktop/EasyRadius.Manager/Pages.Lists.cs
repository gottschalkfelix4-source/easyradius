using System.Net.Http;
using System.Text.Json;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using Wpf.Ui.Controls;
using Button = Wpf.Ui.Controls.Button;
using ListView = Wpf.Ui.Controls.ListView;
using TextBlock = System.Windows.Controls.TextBlock;
using TextBox = Wpf.Ui.Controls.TextBox;

namespace EasyRadius.Manager;

// ===================== Gruppen =====================
public class GroupsPage : PageBase
{
    readonly ListView list;

    public GroupsPage() : base("Gruppen", "Regeln für viele Benutzer auf einmal – VLAN, Sperre, erlaubte WLANs, Laufzeit und Attribute")
    {
        list = Table.Create<Group>([
            new("Gruppe", nameof(Group.Name), 220, nameof(Group.Description), Bold: true),
            new("Status", nameof(Group.StatusText), 100, ColorPath: nameof(Group.StatusColor)),
            new("Mitglieder", nameof(Group.MembersText), 90),
            new("VLAN", nameof(Group.VlanText), 100),
            new("Einschränkungen und Attribute", nameof(Group.LimitsText), 420),
        ], async g => { if (await Edit(g)) await Reload(); },
            ("Bearbeiten", async g => { if (await Edit(g)) await Reload(); }),
            ("Sperren / Entsperren", Toggle),
            ("Löschen", Delete));
        Body.Children.Add(CardOf(list, new Thickness(0)));
        Body.Children.Add(new TextBlock
        {
            Text = "Ein Benutzer gehört zu einer Gruppe. Eigene Werte am Benutzer haben Vorrang. AD-Benutzer erhalten die erste passende Gruppe (alphabetisch), deren verknüpfter AD-Gruppe sie angehören.",
            Style = UI.Style("Muted"), Margin = new Thickness(0, 8, 0, 0),
        });
        Actions(UI.Btn("Gruppe hinzufügen", SymbolRegular.Add24, async () => { if (await Edit(null)) await Reload(); }, ControlAppearance.Primary));
    }

    protected override async Task Reload()
    {
        var clientsTask = App.Api.Get<List<Client>>("/api/clients");
        var groups = await App.Api.Get<List<Group>>("/api/groups");
        var clients = await clientsTask;
        foreach (var g in groups)
        {
            var parts = new List<string>();
            if (g.AllowedSsids.Count > 0) parts.Add("WLAN: " + string.Join(", ", g.AllowedSsids));
            if (g.AllowedClients.Count > 0) parts.Add("Geräte: " + string.Join(", ", g.AllowedClients.Select(id => clients.FirstOrDefault(c => c.Id == id)?.Name ?? "?")));
            if (g.ExpiresAt != "") parts.Add("bis " + Fmt.Date(g.ExpiresAt));
            if (g.AdGroups.Count > 0) parts.Add("AD: " + string.Join(", ", g.AdGroups));
            foreach (var a in g.Attributes ?? []) parts.Add($"{a.KindText}: {a.Name} = {a.Value}");
            g.LimitsText = parts.Count == 0 ? "keine" : string.Join("  ·  ", parts);
        }
        list.ItemsSource = groups;
    }

    async Task Toggle(Group g)
    {
        g.Enabled = !g.Enabled;
        await App.Api.Call(HttpMethod.Put, "/api/groups/" + g.Id, g);
        await UI.Toast(g.Enabled ? $"Gruppe „{g.Name}“ entsperrt" : $"Gruppe „{g.Name}“ gesperrt");
        await Reload();
    }

    async Task Delete(Group g)
    {
        if (!await UI.Confirm("Gruppe löschen", $"Soll die Gruppe „{g.Name}“ gelöscht werden?{(g.Members > 0 ? $" Die {g.Members} Mitglieder bleiben erhalten, sind aber keiner Gruppe mehr zugeordnet." : "")}", "Löschen")) return;
        await App.Api.Call(HttpMethod.Delete, "/api/groups/" + g.Id);
        await UI.Toast("Gruppe gelöscht");
        await Reload();
    }

    static List<string> Split(string s) => s.Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries).ToList();

    public static async Task<bool> Edit(Group? g)
    {
        bool isNew = g == null;
        g ??= new Group();
        var dict = await AttributeEditor.Dictionary();
        var clients = await App.Api.Get<List<Client>>("/api/clients");
        var ad = await App.Api.Get<AdInfo>("/api/ad");

        var f = new Form();
        var name = f.Text("Name", g.Name, "z. B. Mitarbeiter");
        var desc = f.Text("Beschreibung (optional)", g.Description);
        var vlan = new TextBox { Text = g.Vlan, PlaceholderText = "z. B. 30" };
        var exp = new DatePicker();
        if (DateTime.TryParse(g.ExpiresAt, out var ed)) exp.SelectedDate = ed;
        f.Row(("VLAN (optional)", vlan), ("Gültig bis (optional)", exp));
        var ssids = f.Text("Erlaubte WLANs (SSID, optional)", string.Join(", ", g.AllowedSsids), "leer = alle, z. B. Firma, Firma-5G",
            "Mehrere mit Komma trennen. Der Access Point muss die SSID mitsenden (Called-Station-Id).");
        f.Note("Erlaubte Geräte (keins gewählt = alle)", true);
        var checks = new List<(CheckBox box, string id)>();
        foreach (var c in clients)
        {
            var cb = new CheckBox { Content = $"{c.Name}  ({c.Address})", IsChecked = g.AllowedClients.Contains(c.Id), Margin = new Thickness(0, 2, 0, 2) };
            checks.Add((cb, c.Id));
            f.Panel.Children.Add(cb);
        }
        if (clients.Count == 0) f.Note("Noch keine Geräte angelegt.");
        TextBox? adGroups = null;
        if (ad.Mode != "off")
            adGroups = f.Text("Verknüpfte AD-Gruppen", string.Join(", ", g.AdGroups), "z. B. WLAN-Mitarbeiter",
                "Mitglieder dieser AD-Gruppen (auch verschachtelt) erhalten die Regeln dieser Gruppe.");
        var enabled = f.Toggle("Gruppe ist aktiv (aus = alle Mitglieder gesperrt)", isNew || g.Enabled);
        f.Note("Check- und Reply-Attribute", true);
        f.Note("Gelten für alle Mitglieder (auch AD-Benutzer über verknüpfte AD-Gruppen).");
        var attrs = new AttributeEditor(dict, g.Attributes);
        f.Add("", attrs);

        var dlg = new FormDialog(isNew ? "Gruppe hinzufügen" : "Gruppe bearbeiten", f.Panel, isNew ? "Anlegen" : "Speichern");
        dlg.OnSave = async () =>
        {
            var body = new
            {
                name = name.Text.Trim(), description = desc.Text.Trim(), vlan = vlan.Text.Trim(),
                expiresAt = exp.SelectedDate?.ToString("yyyy-MM-dd") ?? "", enabled = enabled.IsChecked == true,
                allowedSsids = Split(ssids.Text), allowedClients = checks.Where(x => x.box.IsChecked == true).Select(x => x.id).ToList(),
                adGroups = adGroups != null ? Split(adGroups.Text) : g.AdGroups, attributes = attrs.Collect(),
            };
            if (isNew) await App.Api.Call(HttpMethod.Post, "/api/groups", body);
            else await App.Api.Call(HttpMethod.Put, "/api/groups/" + g.Id, body);
        };
        if (await dlg.ShowAsync() != ContentDialogResult.Primary) return false;
        await UI.Toast(isNew ? "Gruppe angelegt" : "Gespeichert");
        return true;
    }
}

// ===================== Geräte =====================
public class ClientsPage : PageBase
{
    readonly ListView list;
    readonly StackPanel info = new();

    public ClientsPage() : base("Geräte", "Access Points, Switches und Router, die beim Server anfragen dürfen")
    {
        list = Table.Create<Client>([
            new("Name", nameof(Client.Name), 260, nameof(Client.Note), Bold: true),
            new("IP-Adresse / Netz", nameof(Client.Address), 200, Mono: true),
            new("Zuletzt gesehen", nameof(Client.LastSeenText), 160),
        ], async c => { if (await Edit(c)) await Reload(); },
            ("Bearbeiten", async c => { if (await Edit(c)) await Reload(); }),
            ("Shared Secret kopieren", c => { UI.Copy(c.Secret); return Task.CompletedTask; }),
            ("Entfernen", Delete));
        Body.Children.Add(CardOf(list, new Thickness(0)));
        Body.Children.Add(Section("So richtest du deinen Access Point ein"));
        Body.Children.Add(info);
        Actions(UI.Btn("Gerät hinzufügen", SymbolRegular.Add24, async () => { if (await Edit(null)) await Reload(); }, ControlAppearance.Primary));
    }

    protected override async Task Reload()
    {
        var dashTask = App.Api.Get<Dashboard>("/api/dashboard");
        list.ItemsSource = await App.Api.Get<List<Client>>("/api/clients");
        var d = await dashTask;
        var ip = d.Ips.FirstOrDefault() ?? "–";
        info.Children.Clear();
        info.Children.Add(UI.SettingCard(SymbolRegular.Server24, "RADIUS-Server", string.Join(", ", d.Ips), UI.Btn("Kopieren", SymbolRegular.Copy24, () => { UI.Copy(ip); return Task.CompletedTask; })));
        info.Children.Add(UI.SettingCard(SymbolRegular.PlugConnected24, "Ports", $"Authentifizierung {d.Status.AuthPort}, Accounting {d.Status.AcctPort} (UDP)", null));
        info.Children.Add(UI.SettingCard(SymbolRegular.Wifi124, "Sicherheit im WLAN-Profil", "WPA2-Enterprise oder WPA3-Enterprise; am Switch-Port 802.1X", null));
        info.Children.Add(UI.SettingCard(SymbolRegular.Key24, "Shared Secret", "wie beim Gerät hinterlegt (Rechtsklick → kopieren)", null));
    }

    async Task Delete(Client c)
    {
        if (!await UI.Confirm("Gerät entfernen", $"Soll „{c.Name}“ entfernt werden? Anfragen von {c.Address} werden danach ignoriert.", "Entfernen")) return;
        await App.Api.Call(HttpMethod.Delete, "/api/clients/" + c.Id);
        await UI.Toast("Gerät entfernt");
        await Reload();
    }

    public static async Task<bool> Edit(Client? c)
    {
        bool isNew = c == null || c.Id == "";
        c ??= new Client();
        var f = new Form();
        var name = f.Text("Name", c.Name, "z. B. Access Point Büro");
        var addr = f.Text("IP-Adresse oder Netz", c.Address, "192.168.1.10 oder 192.168.1.0/24",
            "Die Adresse, von der das Gerät anfragt. Mit einem Netz (CIDR) gibst du mehrere Geräte auf einmal frei.");
        var secret = new TextBox { Text = isNew ? UI.RandomSecret() : "", PlaceholderText = isNew ? "" : "leer lassen = unverändert" };
        var gen = new Button { Icon = new SymbolIcon(SymbolRegular.ArrowShuffle24), ToolTip = "Neu erzeugen", Margin = new Thickness(6, 0, 0, 0) };
        gen.Click += (_, _) => secret.Text = UI.RandomSecret();
        var row = new DockPanel();
        DockPanel.SetDock(gen, Dock.Right);
        row.Children.Add(gen);
        row.Children.Add(secret);
        f.Add("Shared Secret", row, "Dieses Geheimnis musst du genauso am Gerät eintragen. Es wird nach dem Speichern kopiert.");
        var note = f.Text("Notiz", c.Note);

        var dlg = new FormDialog(isNew ? "Gerät hinzufügen" : "Gerät bearbeiten", f.Panel, isNew ? "Hinzufügen" : "Speichern");
        dlg.OnSave = async () =>
        {
            var body = new { name = name.Text.Trim(), address = addr.Text.Trim(), secret = secret.Text.Trim(), note = note.Text.Trim() };
            if (isNew) await App.Api.Call(HttpMethod.Post, "/api/clients", body);
            else await App.Api.Call(HttpMethod.Put, "/api/clients/" + c.Id, body);
        };
        if (await dlg.ShowAsync() != ContentDialogResult.Primary) return false;
        if (secret.Text.Trim() != "") UI.Copy(secret.Text.Trim());
        await UI.Toast(isNew ? "Gerät hinzugefügt – Shared Secret in der Zwischenablage" : "Gespeichert");
        return true;
    }
}

// ===================== Protokoll =====================
public class LogPage : PageBase
{
    readonly StackPanel rows = new();
    readonly ComboBox filter = new() { Width = 160, Margin = new Thickness(0, 0, 8, 0), ItemsSource = new[] { "Alle", "Angenommen", "Abgelehnt", "Sitzungen", "System" }, SelectedIndex = 0 };
    readonly TextBox search = new() { PlaceholderText = "Benutzer, Gerät, MAC …", Width = 240, Icon = new SymbolIcon(SymbolRegular.Search24) };
    List<Event> items = [];
    CancellationTokenSource? live;

    public LogPage() : base("Protokoll", "Neue Ereignisse erscheinen automatisch")
    {
        Body.Children.Add(CardOf(rows));
        filter.SelectionChanged += (_, _) => Draw();
        search.TextChanged += (_, _) => Draw();
        Actions(filter, search);
        Loaded += (_, _) => StartLive();
        Unloaded += (_, _) => live?.Cancel();
    }

    protected override async Task Reload()
    {
        items = await App.Api.Get<List<Event>>("/api/events?limit=1000");
        Draw();
    }

    void StartLive()
    {
        live?.Cancel();
        live = new CancellationTokenSource();
        var ct = live.Token;
        _ = Task.Run(async () =>
        {
            while (!ct.IsCancellationRequested)
            {
                try
                {
                    await App.Api.Stream("/api/events/stream", data =>
                    {
                        var e = JsonSerializer.Deserialize<Event>(data, ApiClient.Json);
                        if (e != null) Dispatcher.Invoke(() => { items.Insert(0, e); Draw(); });
                    }, ct);
                }
                catch { }
                try { await Task.Delay(3000, ct); } catch { }
            }
        }, ct);
    }

    void Draw()
    {
        var q = search.Text.Trim().ToLowerInvariant();
        var f = filter.SelectedIndex;
        var list = items.Where(e =>
            (f == 0 || (f == 1 && e.Kind == "accept") || (f == 2 && e.Kind == "reject") || (f == 3 && e.Kind == "acct") || (f == 4 && (e.Kind == "info" || e.Kind == "warning")))
            && (q == "" || $"{e.User} {e.Client} {e.ClientIp} {e.Station} {e.Message} {e.Method}".ToLowerInvariant().Contains(q))).Take(300).ToList();
        rows.Children.Clear();
        foreach (var e in list) rows.Children.Add(Row(e, true));
        if (list.Count == 0) rows.Children.Add(new TextBlock { Text = "Keine Einträge.", Style = UI.Style("Muted"), Margin = new Thickness(0, 20, 0, 20), HorizontalAlignment = HorizontalAlignment.Center });
    }

    public static FrameworkElement Row(Event e, bool absolute)
    {
        var (bg, fg) = e.Kind switch
        {
            "accept" => (Color.FromArgb(0x30, 0x0F, 0x7B, 0x0F), Color.FromRgb(0x0F, 0x7B, 0x0F)),
            "reject" => (Color.FromArgb(0x30, 0xC4, 0x2B, 0x1C), Color.FromRgb(0xC4, 0x2B, 0x1C)),
            "warning" => (Color.FromArgb(0x30, 0x9D, 0x5D, 0x00), Color.FromRgb(0x9D, 0x5D, 0x00)),
            _ => (Color.FromArgb(0x18, 0x80, 0x80, 0x80), Color.FromRgb(0x80, 0x80, 0x80)),
        };
        var g = new Grid { Margin = new Thickness(0, 6, 0, 6) };
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        g.ColumnDefinitions.Add(new ColumnDefinition());
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var icon = new Border
        {
            Width = 28, Height = 28, CornerRadius = new CornerRadius(14), Background = new SolidColorBrush(bg), Margin = new Thickness(0, 0, 12, 0), VerticalAlignment = VerticalAlignment.Top,
            Child = new TextBlock { Text = e.Glyph, FontFamily = new FontFamily("Segoe Fluent Icons, Segoe MDL2 Assets"), FontSize = 12, Foreground = new SolidColorBrush(fg), HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center },
        };
        var text = new StackPanel();
        text.Children.Add(new TextBlock { Text = e.Title, TextWrapping = TextWrapping.Wrap, Foreground = UI.Brush("TextFillColorPrimaryBrush") });
        if (e.Meta != "") text.Children.Add(new TextBlock { Text = e.Meta, Style = UI.Style("Muted") });
        var time = new TextBlock { Text = absolute ? e.TimeText : e.RelText, Style = UI.Style("Muted"), Margin = new Thickness(12, 0, 0, 0), ToolTip = e.TimeText };
        Grid.SetColumn(text, 1);
        Grid.SetColumn(time, 2);
        g.Children.Add(icon);
        g.Children.Add(text);
        g.Children.Add(time);
        return g;
    }
}
