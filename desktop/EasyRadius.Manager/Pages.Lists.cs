using System.Collections.Concurrent;
using System.Collections.ObjectModel;
using System.Net;
using System.Net.Http;
using System.Text.Json;
using System.Windows;
using System.Windows.Automation;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Threading;
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
        ], App.CanWrite ? async g => { if (await Edit(g)) await Reload(); } : null,
            Table.Writable<Group>(
                ("Bearbeiten", async g => { if (await Edit(g)) await Reload(); }),
                ("Sperren / Entsperren", Toggle),
                ("Löschen", Delete)));
        Body.Children.Add(CardOf(list, new Thickness(0)));
        Body.Children.Add(new TextBlock
        {
            Text = "Ein Benutzer gehört zu einer Gruppe. Eigene Werte am Benutzer haben Vorrang. AD-Benutzer erhalten die erste passende Gruppe (alphabetisch), deren verknüpfter AD-Gruppe sie angehören.",
            Style = UI.Style("Muted"), Margin = new Thickness(0, 8, 0, 0),
        });
        Actions(UI.Btn("Gruppe hinzufügen", SymbolRegular.Add24, async () => { if (await Edit(null)) await Reload(); }, ControlAppearance.Primary).AdminOnly());
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
        // Kopie senden: die angezeigte Gruppe bleibt unverändert, falls der Server ablehnt.
        var changed = g.Copy();
        changed.Enabled = !g.Enabled;
        await App.Api.Call(HttpMethod.Put, "/api/groups/" + g.Id, changed);
        await UI.Toast(changed.Enabled ? $"Gruppe „{g.Name}“ entsperrt" : $"Gruppe „{g.Name}“ gesperrt");
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
            new("Schutz", nameof(Client.MaText), 100, ColorPath: nameof(Client.MaColor)),
            new("CoA-Port", nameof(Client.CoaText), 90),
            new("Zuletzt gesehen", nameof(Client.LastSeenText), 160),
        ], App.CanWrite ? async c => { if (await Edit(c)) await Reload(); } : null,
            Table.Writable<Client>(
                ("Bearbeiten", async c => { if (await Edit(c)) await Reload(); }),
                ("Shared Secret anzeigen", ShowSecret),
                ("Shared Secret kopieren", async c => UI.CopySecret(await Secret(c))),
                ("Entfernen", Delete)));
        Body.Children.Add(CardOf(list, new Thickness(0)));
        Body.Children.Add(Section("So richtest du deinen Access Point ein"));
        Body.Children.Add(info);
        Actions(UI.Btn("Gerät hinzufügen", SymbolRegular.Add24, async () => { if (await Edit(null)) await Reload(); }, ControlAppearance.Primary).AdminOnly());
    }

    /// <summary>Das Shared Secret steht nicht mehr in der Liste, sondern wird einzeln abgerufen (der Abruf wird protokolliert).</summary>
    static async Task<string> Secret(Client c)
    {
        var s = (await App.Api.Get<SecretInfo>($"/api/clients/{Uri.EscapeDataString(c.Id)}/secret")).Secret;
        if (string.IsNullOrEmpty(s)) throw new ApiException($"Für „{c.Name}“ ist kein Shared Secret hinterlegt.");
        return s;
    }

    static async Task ShowSecret(Client c)
    {
        var secret = await Secret(c);
        var f = new Form();
        f.Add("Shared Secret", UI.Hstack(
            new TextBlock { Text = secret, FontFamily = new FontFamily("Cascadia Mono, Consolas"), FontSize = 16, VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(0, 0, 12, 0), TextWrapping = TextWrapping.Wrap, MaxWidth = 420 }.Fg(),
            UI.Btn("Kopieren", SymbolRegular.Copy24, () => { UI.CopySecret(secret); return Task.CompletedTask; })));
        f.Note("Der Abruf wird im Protokoll vermerkt.");
        await UI.Info($"Shared Secret von „{c.Name}“", f.Panel);
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
        info.Children.Add(UI.SettingCard(SymbolRegular.Key24, "Shared Secret", "wie beim Gerät hinterlegt (Rechtsklick → anzeigen oder kopieren)", null));
        info.Children.Add(UI.SettingCard(SymbolRegular.ShieldCheckmark24, "Message-Authenticator", "Schützt vor Blast-RADIUS. Geräte mit „ohne MA“ möglichst umstellen, sobald sie das Attribut mitsenden.", null));
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
        var gen = UI.IconBtn(SymbolRegular.ArrowShuffle24, "Neues Shared Secret erzeugen", () => secret.Text = UI.RandomSecret());
        var row = new DockPanel();
        DockPanel.SetDock(gen, Dock.Right);
        row.Children.Add(gen);
        row.Children.Add(secret);
        f.Add("Shared Secret", row, "Dieses Geheimnis musst du genauso am Gerät eintragen. Es wird nach dem Speichern kopiert.");
        var note = f.Text("Notiz", c.Note);
        var requireMA = new CheckBox { Content = "Message-Authenticator verlangen (Schutz vor Blast-RADIUS)", IsChecked = isNew || c.RequireMA, Margin = new Thickness(0, 14, 0, 0) };
        f.Panel.Children.Add(requireMA);
        f.Note("Empfohlen. Nur ausschalten, wenn ein älteres Gerät das Attribut nicht mitsendet und deshalb abgelehnt wird.");
        var coa = f.Text("CoA-Port (optional)", c.CoaPort > 0 ? c.CoaPort.ToString() : "", "3799",
            "Port des Geräts für „Sitzung trennen“ (Disconnect-Request). Leer = 3799.");

        var dlg = new FormDialog(isNew ? "Gerät hinzufügen" : "Gerät bearbeiten", f.Panel, isNew ? "Hinzufügen" : "Speichern");
        dlg.OnSave = async () =>
        {
            int coaPort = 0;
            if (coa.Text.Trim() != "" && (!int.TryParse(coa.Text.Trim(), out coaPort) || coaPort is < 0 or > 65535))
                throw new InvalidOperationException("Der CoA-Port muss eine Zahl zwischen 1 und 65535 sein (leer = 3799).");
            var body = new
            {
                name = name.Text.Trim(), address = addr.Text.Trim(), secret = secret.Text.Trim(), note = note.Text.Trim(),
                requireMA = requireMA.IsChecked == true, coaPort,
            };
            if (isNew) await App.Api.Call(HttpMethod.Post, "/api/clients", body);
            else await App.Api.Call(HttpMethod.Put, "/api/clients/" + c.Id, body);
        };
        if (await dlg.ShowAsync() != ContentDialogResult.Primary) return false;
        if (secret.Text.Trim() != "") UI.CopySecret(secret.Text.Trim());
        await UI.Toast(isNew ? "Gerät hinzugefügt – Shared Secret in der Zwischenablage" : "Gespeichert");
        return true;
    }
}

// ===================== Protokoll =====================
public class LogPage : PageBase
{
    const int MaxEvents = 2000;
    static readonly TimeSpan Idle = TimeSpan.FromSeconds(60);       // Server sendet alle 25 s „: ping“
    static readonly TimeSpan MinDelay = TimeSpan.FromSeconds(3);
    static readonly TimeSpan MaxDelay = TimeSpan.FromSeconds(60);

    readonly System.Windows.Controls.ListBox list = new()
    {
        Height = 480, BorderThickness = new Thickness(0), Background = Brushes.Transparent,
        HorizontalContentAlignment = HorizontalAlignment.Stretch,
    };
    readonly TextBlock empty = new() { Text = "Keine Einträge.", Style = UI.Style("Muted"), Margin = new Thickness(0, 20, 0, 20), HorizontalAlignment = HorizontalAlignment.Center, Visibility = Visibility.Collapsed };
    readonly TextBlock status = new() { Style = UI.Style("Muted"), VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(0, 0, 12, 0) };
    readonly ComboBox filter = new() { Width = 160, Margin = new Thickness(0, 0, 8, 0), ItemsSource = new[] { "Alle", "Angenommen", "Abgelehnt", "Sitzungen", "System", "Änderungen" }, SelectedIndex = 0 };
    readonly TextBox search = new() { PlaceholderText = "Benutzer, Gerät, MAC …", Width = 240, Icon = new SymbolIcon(SymbolRegular.Search24) };
    readonly List<Event> items = [];                     // neueste zuerst, höchstens MaxEvents
    ObservableCollection<Event> shown = [];
    readonly ConcurrentQueue<Event> incoming = new();    // vom Live-Strom, wird gesammelt übernommen
    readonly DispatcherTimer flush = new() { Interval = TimeSpan.FromMilliseconds(250) };
    CancellationTokenSource? live;

    public LogPage() : base("Protokoll", "Neue Ereignisse erscheinen automatisch")
    {
        VirtualizingPanel.SetIsVirtualizing(list, true);
        VirtualizingPanel.SetVirtualizationMode(list, VirtualizationMode.Recycling);
        VirtualizingPanel.SetScrollUnit(list, ScrollUnit.Pixel);
        ScrollViewer.SetCanContentScroll(list, true);
        ScrollViewer.SetHorizontalScrollBarVisibility(list, ScrollBarVisibility.Disabled);
        list.ItemsPanel = new ItemsPanelTemplate(new FrameworkElementFactory(typeof(VirtualizingStackPanel)));
        list.ItemTemplate = new DataTemplate { VisualTree = new FrameworkElementFactory(typeof(EventRow)) };
        list.ItemsSource = shown;
        AutomationProperties.SetName(list, "Protokolleinträge");
        AutomationProperties.SetName(filter, "Filter");
        AutomationProperties.SetName(search, "Protokoll durchsuchen");

        var box = new Grid();
        box.Children.Add(list);
        box.Children.Add(empty);
        Body.Children.Add(CardOf(box, new Thickness(8)));
        filter.SelectionChanged += (_, _) => Rebuild();
        search.TextChanged += (_, _) => Rebuild();
        Actions(status, filter, search);
        flush.Tick += (_, _) => Flush();
        SizeChanged += (_, _) => list.Height = Math.Max(240, ActualHeight - 200);
        Loaded += (_, _) => { flush.Start(); StartLive(); };
        Unloaded += (_, _) => { flush.Stop(); live?.Cancel(); };
    }

    protected override async Task Reload()
    {
        var loaded = await App.Api.Get<List<Event>>("/api/events?limit=1000");
        items.Clear();
        items.AddRange(loaded.Take(MaxEvents));
        Rebuild();
    }

    void StartLive()
    {
        live?.Cancel();
        live = new CancellationTokenSource();
        var ct = live.Token;
        var api = App.Api;
        SetStatus("Verbinde …", false);
        _ = Task.Run(async () =>
        {
            var delay = MinDelay;
            while (!ct.IsCancellationRequested)
            {
                string? why = null;
                try
                {
                    await api.Stream("/api/events/stream", data =>
                    {
                        try { if (JsonSerializer.Deserialize<Event>(data, ApiClient.Json) is { } e) incoming.Enqueue(e); }
                        catch (JsonException) { }
                    }, () => { delay = MinDelay; SetStatus("Live verbunden", true); }, Idle, ct);
                }
                catch (OperationCanceledException) when (ct.IsCancellationRequested) { return; }
                catch (ApiException e) when (e.Status == HttpStatusCode.Unauthorized) { SetStatus("Sitzung abgelaufen", false); return; }
                catch (Exception e) { why = UI.Describe(e); }
                if (ct.IsCancellationRequested) return;
                SetStatus($"Verbindung getrennt – neuer Versuch in {delay.TotalSeconds:0} s …", false, why);
                try { await Task.Delay(delay, ct); } catch (OperationCanceledException) { return; }
                delay = TimeSpan.FromSeconds(Math.Min(MaxDelay.TotalSeconds, delay.TotalSeconds * 2));
                SetStatus("Verbinde …", false);
            }
        }, ct);
    }

    void SetStatus(string text, bool ok, string? detail = null) => Dispatcher.BeginInvoke(() =>
    {
        status.Text = text;
        status.ToolTip = detail;
        status.SetResourceReference(TextBlock.ForegroundProperty, ok ? "StatusOkBrush" : "TextFillColorSecondaryBrush");
    });

    static bool Matches(Event e, int f, string q) =>
        (f == 0 || (f == 1 && e.Kind == "accept") || (f == 2 && e.Kind == "reject") || (f == 3 && e.Kind == "acct") || (f == 4 && (e.Kind == "info" || e.Kind == "warning")) || (f == 5 && e.Kind == "audit"))
        && (q == "" || $"{e.User} {e.Client} {e.ClientIp} {e.Station} {e.Message} {e.Method} {e.Admin}".ToLowerInvariant().Contains(q));

    /// <summary>Übernimmt neue Ereignisse gesammelt (höchstens alle 250 ms) statt bei jedem Ereignis neu zu zeichnen.</summary>
    void Flush()
    {
        if (incoming.IsEmpty) return;
        var batch = new List<Event>();
        while (incoming.TryDequeue(out var e)) batch.Add(e);
        foreach (var e in batch) items.Insert(0, e);
        if (items.Count > MaxEvents) items.RemoveRange(MaxEvents, items.Count - MaxEvents);
        if (batch.Count > 100) { Rebuild(); return; }
        var f = filter.SelectedIndex;
        var q = search.Text.Trim().ToLowerInvariant();
        foreach (var e in batch) if (Matches(e, f, q)) shown.Insert(0, e);
        while (shown.Count > MaxEvents) shown.RemoveAt(shown.Count - 1);
        empty.Visibility = shown.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    void Rebuild()
    {
        var f = filter.SelectedIndex;
        var q = search.Text.Trim().ToLowerInvariant();
        shown = new ObservableCollection<Event>(items.Where(e => Matches(e, f, q)));
        list.ItemsSource = shown;
        empty.Visibility = shown.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    static SolidColorBrush Frozen(Color c) { var b = new SolidColorBrush(c); b.Freeze(); return b; }
    static readonly SolidColorBrush BgAccept = Frozen(Color.FromArgb(0x30, 0x0F, 0x7B, 0x0F));
    static readonly SolidColorBrush BgReject = Frozen(Color.FromArgb(0x30, 0xC4, 0x2B, 0x1C));
    static readonly SolidColorBrush BgWarning = Frozen(Color.FromArgb(0x30, 0x9D, 0x5D, 0x00));
    static readonly SolidColorBrush BgAudit = Frozen(Color.FromArgb(0x30, 0x00, 0x5F, 0xB8));
    static readonly SolidColorBrush BgOther = Frozen(Color.FromArgb(0x18, 0x80, 0x80, 0x80));

    public static FrameworkElement Row(Event e, bool absolute)
    {
        var (bg, fg) = e.Kind switch
        {
            "accept" => (BgAccept, "StatusOkBrush"),
            "reject" => (BgReject, "StatusErrorBrush"),
            "warning" => (BgWarning, "StatusWarnBrush"),
            "audit" => (BgAudit, "ReplyBrush"),
            _ => (BgOther, "TextFillColorSecondaryBrush"),
        };
        var g = new Grid { Margin = new Thickness(0, 6, 0, 6) };
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        g.ColumnDefinitions.Add(new ColumnDefinition());
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var icon = new Border
        {
            Width = 28, Height = 28, CornerRadius = new CornerRadius(14), Background = bg, Margin = new Thickness(0, 0, 12, 0), VerticalAlignment = VerticalAlignment.Top,
            Child = new TextBlock { Text = e.Glyph, FontFamily = new FontFamily("Segoe Fluent Icons, Segoe MDL2 Assets"), FontSize = 12, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center }.Fg(fg),
        };
        var text = new StackPanel();
        text.Children.Add(new TextBlock { Text = e.Title, TextWrapping = TextWrapping.Wrap }.Fg());
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

/// <summary>Eine Zeile im Protokoll; wird beim Blättern wiederverwendet (virtualisierte Liste).</summary>
public class EventRow : ContentControl
{
    public EventRow()
    {
        Focusable = false;
        DataContextChanged += (_, _) => Content = DataContext is Event e ? LogPage.Row(e, true) : null;
    }
}
