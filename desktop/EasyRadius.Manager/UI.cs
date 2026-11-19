using System.IO;
using System.Net;
using System.Net.Http;
using System.Text.Json;
using System.Windows;
using System.Windows.Automation;
using System.Windows.Controls;
using System.Windows.Documents;
using System.Windows.Input;
using System.Windows.Media;
using Wpf.Ui.Controls;
using Button = Wpf.Ui.Controls.Button;
using TextBlock = System.Windows.Controls.TextBlock;
using TextBox = Wpf.Ui.Controls.TextBox;
using PasswordBox = Wpf.Ui.Controls.PasswordBox;

namespace EasyRadius.Manager;

/// <summary>Dialog mit Speichern-Logik: schließt nur, wenn das Speichern klappt.</summary>
public class FormDialog : ContentDialog
{
    readonly InfoBar error = new() { Severity = InfoBarSeverity.Error, IsClosable = false, Margin = new Thickness(0, 0, 0, 12) };
    public Func<Task>? OnSave { get; set; }
    bool busy;

    public FormDialog(string title, UIElement body, string primary = "Speichern", ContentDialogHost? host = null) : base(host ?? UI.RequireHost())
    {
        SetResourceReference(StyleProperty, typeof(ContentDialog)); // Stil von ContentDialog übernehmen
        Title = title;
        PrimaryButtonText = primary;
        CloseButtonText = "Abbrechen";
        DefaultButton = ContentDialogButton.Primary;
        DialogMaxWidth = 760;
        var panel = new StackPanel();
        panel.Children.Add(error);
        panel.Children.Add(body);
        Content = new ScrollViewer { Content = panel, VerticalScrollBarVisibility = ScrollBarVisibility.Auto, MaxHeight = 560, Padding = new Thickness(0, 0, 12, 0) };
        // Während gespeichert wird, lässt sich der Dialog weder per Esc noch per Abbrechen schließen.
        Closing += (_, e) => { if (busy) e.Cancel = true; };
        PreviewKeyDown += (_, e) => { if (busy && e.Key == Key.Escape) e.Handled = true; };
    }

    public void ShowError(string? msg)
    {
        error.Message = msg ?? "";
        error.Title = "Fehler";
        error.IsOpen = !string.IsNullOrEmpty(msg);
    }

    void SetBusy(bool on)
    {
        busy = on;
        IsPrimaryButtonEnabled = !on;
        IsSecondaryButtonEnabled = !on;
        if (on) Cursor = Cursors.Wait; else ClearValue(CursorProperty);
    }

    protected override async void OnButtonClick(ContentDialogButton button)
    {
        if (busy) return;
        if (button != ContentDialogButton.Primary || OnSave == null) { base.OnButtonClick(button); return; }
        SetBusy(true);
        try
        {
            await OnSave();
        }
        catch (ApiException e) when (e.Status == HttpStatusCode.Unauthorized) { return; } // Anmeldefenster übernimmt
        catch (ApiException e) { ShowError(e.Message); return; }
        catch (InvalidOperationException e) { ShowError(e.Message); return; }
        catch (Exception e) when (UI.IsExpected(e)) { ShowError(UI.Describe(e)); return; }
        finally { SetBusy(false); }
        base.OnButtonClick(button);
    }
}

/// <summary>Baut einfache Formulare (Beschriftung, Feld, Hinweis).</summary>
public class Form
{
    public StackPanel Panel { get; } = new();

    public T Add<T>(string label, T control, string? help = null) where T : UIElement
    {
        if (!string.IsNullOrEmpty(label))
        {
            var l = new TextBlock { Text = label, Margin = new Thickness(0, 12, 0, 6) }.Fg();
            Panel.Children.Add(l);
            UI.Label(control, l);
        }
        Panel.Children.Add(control);
        if (!string.IsNullOrEmpty(help))
        {
            Panel.Children.Add(new TextBlock { Text = help, Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 0) });
            AutomationProperties.SetHelpText(UI.InputOf(control), help);
        }
        return control;
    }

    public TextBox Text(string label, string? value = "", string? placeholder = null, string? help = null) =>
        Add(label, new TextBox { Text = value ?? "", PlaceholderText = placeholder ?? "" }, help);

    public PasswordBox Password(string label, string? placeholder = null, string? help = null) =>
        Add(label, new PasswordBox { PlaceholderText = placeholder ?? "" }, help);

    public ToggleSwitch Toggle(string label, bool value, string? help = null)
    {
        var t = new ToggleSwitch { IsChecked = value, Content = label, Margin = new Thickness(0, 12, 0, 0) };
        Panel.Children.Add(t);
        if (help != null)
        {
            Panel.Children.Add(new TextBlock { Text = help, Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 0) });
            AutomationProperties.SetHelpText(t, help);
        }
        return t;
    }

    public ComboBox Combo(string label, IEnumerable<object> items, object? selected = null, string? help = null)
    {
        var c = new ComboBox { ItemsSource = items.ToList(), HorizontalAlignment = HorizontalAlignment.Stretch };
        if (selected != null) c.SelectedItem = selected;
        return Add(label, c, help);
    }

    public DatePicker Date(string label, string ymd, string? help = null)
    {
        var d = new DatePicker { SelectedDateFormat = DatePickerFormat.Short };
        if (DateTime.TryParse(ymd, out var dt)) d.SelectedDate = dt;
        return Add(label, d, help);
    }

    public void Row(params (string label, FrameworkElement control)[] cells)
    {
        var g = new Grid();
        for (int i = 0; i < cells.Length; i++)
        {
            g.ColumnDefinitions.Add(new ColumnDefinition());
            var sp = new StackPanel { Margin = new Thickness(i == 0 ? 0 : 8, 0, i == cells.Length - 1 ? 0 : 8, 0) };
            var l = new TextBlock { Text = cells[i].label, Margin = new Thickness(0, 12, 0, 6) }.Fg();
            sp.Children.Add(l);
            sp.Children.Add(cells[i].control);
            UI.Label(cells[i].control, l);
            Grid.SetColumn(sp, i);
            g.Children.Add(sp);
        }
        Panel.Children.Add(g);
    }

    public TextBlock Note(string text, bool section = false)
    {
        var t = new TextBlock { Text = text, Style = UI.Style(section ? "SectionTitle" : "Muted"), Margin = new Thickness(0, section ? 20 : 8, 0, 4) };
        Panel.Children.Add(t);
        return t;
    }
}

public static class UI
{
    /// <summary>Das (einzige) Hauptfenster – null, wenn gerade das Anmeldefenster offen ist.</summary>
    static MainWindow? Main => Application.Current?.Windows.OfType<MainWindow>().FirstOrDefault();

    public static ContentDialogHost? Host => Main?.DialogHost;

    public static ContentDialogHost RequireHost() => Host ?? throw new ApiException("Das Hauptfenster ist nicht mehr geöffnet.");

    public static Brush Brush(string key) => (Brush)Application.Current.FindResource(key);
    public static Style Style(string key) => (Style)Application.Current.FindResource(key);

    /// <summary>Bindet eine Eigenschaft an eine (Design-)Ressource, damit sie beim Wechsel hell/dunkel mitgeht.</summary>
    public static T Res<T>(this T e, DependencyProperty dp, string key) where T : FrameworkElement
    {
        e.SetResourceReference(dp, key);
        return e;
    }

    /// <summary>Textfarbe aus dem Design (Standard: primäre Textfarbe).</summary>
    public static T Fg<T>(this T e, string key = "TextFillColorPrimaryBrush") where T : FrameworkElement =>
        e.Res(TextElement.ForegroundProperty, key);

    public static Task Toast(string msg, bool error = false)
    {
        Main?.Toast(msg, error);
        return Task.CompletedTask;
    }

    /// <summary>Fehler, die zur Laufzeit erwartbar sind (Netz, Datei, Server) – werden als kurze Meldung angezeigt.</summary>
    public static bool IsExpected(Exception e) =>
        e is ApiException or IOException or UnauthorizedAccessException or HttpRequestException or JsonException or TimeoutException;

    public static string Describe(Exception e) => e switch
    {
        ApiException a => a.Message,
        HttpRequestException h => $"Netzwerkfehler: {h.Message}",
        JsonException => "Unerwartete Antwort vom Server – ist das ein EasyRadius?",
        TimeoutException => "Zeitüberschreitung – der Server antwortet nicht.",
        UnauthorizedAccessException u => $"Zugriff verweigert: {u.Message}",
        IOException io => $"Datei- oder Netzwerkfehler: {io.Message}",
        _ => e.Message,
    };

    /// <summary>Zeigt einen Fehler als kurze Meldung. Abgelaufene Sitzungen (401) werden nicht gemeldet – das Anmeldefenster erscheint.</summary>
    public static void ReportError(Exception e)
    {
        if (e is ApiException { Status: HttpStatusCode.Unauthorized }) return;
        _ = Toast(Describe(e), true);
    }

    public static async Task<bool> Confirm(string title, string text, string action)
    {
        if (Host is not { } host) return false;
        var d = new ContentDialog(host)
        {
            Title = title,
            Content = new TextBlock { Text = text, TextWrapping = TextWrapping.Wrap, MaxWidth = 420 },
            PrimaryButtonText = action,
            PrimaryButtonAppearance = ControlAppearance.Danger,
            CloseButtonText = "Abbrechen",
            DefaultButton = ContentDialogButton.Close,
        };
        return await d.ShowAsync() == ContentDialogResult.Primary;
    }

    public static async Task Info(string title, UIElement content)
    {
        if (Host is not { } host) return;
        var d = new ContentDialog(host) { Title = title, Content = content, CloseButtonText = "Schließen", DialogMaxWidth = 640 };
        await d.ShowAsync();
    }

    public static Button Btn(string text, SymbolRegular icon, Func<Task> onClick, ControlAppearance look = ControlAppearance.Secondary)
    {
        var b = new Button { Content = text, Icon = new SymbolIcon(icon), Appearance = look, Margin = new Thickness(0, 0, 8, 0) };
        b.Click += async (_, _) =>
        {
            b.IsEnabled = false;
            try { await onClick(); }
            catch (Exception e) when (IsExpected(e)) { ReportError(e); }
            finally { b.IsEnabled = true; }
        };
        return b;
    }

    /// <summary>Wechselt im Hauptfenster zu einer Seite.</summary>
    public static void Go(Type page) => Main?.Go(page);

    /// <summary>Schaltfläche mit Aufklappmenü. Einträge mit leerem Text werden weggelassen.</summary>
    public static Button MenuBtn(string text, SymbolRegular icon, params (string text, Func<Task> act)[] items)
    {
        var b = new Button { Content = text, Icon = new SymbolIcon(icon), Margin = new Thickness(0, 0, 8, 0) };
        var cm = new ContextMenu { PlacementTarget = b, Placement = System.Windows.Controls.Primitives.PlacementMode.Bottom };
        foreach (var (t, act) in items.Where(i => i.text != ""))
        {
            var mi = new System.Windows.Controls.MenuItem { Header = t };
            mi.Click += async (_, _) =>
            {
                try { await act(); }
                catch (Exception e) when (IsExpected(e)) { ReportError(e); }
            };
            cm.Items.Add(mi);
        }
        b.ContextMenu = cm;
        b.Click += (_, _) => cm.IsOpen = true;
        return b;
    }

    /// <summary>Blendet ein Element für „Nur lesen“-Konten aus (Hinzufügen, Speichern, Löschen …).</summary>
    public static T AdminOnly<T>(this T e) where T : UIElement
    {
        if (!App.CanWrite) e.Visibility = Visibility.Collapsed;
        return e;
    }

    /// <summary>Schaltfläche nur mit Symbol – mit Namen für Screenreader und Tooltip.</summary>
    public static Button IconBtn(SymbolRegular icon, string name, Action onClick)
    {
        var b = new Button { Icon = new SymbolIcon(icon), ToolTip = name, Margin = new Thickness(6, 0, 0, 0) };
        AutomationProperties.SetName(b, name);
        b.Click += (_, _) => onClick();
        return b;
    }

    /// <summary>Eingabefeld eines zusammengesetzten Elements (z. B. Textfeld neben einem Würfel-Knopf).</summary>
    public static UIElement InputOf(UIElement e) =>
        e is Panel p ? p.Children.OfType<UIElement>().FirstOrDefault(c => c is System.Windows.Controls.TextBox or System.Windows.Controls.PasswordBox
            or TextBox or PasswordBox or ComboBox or DatePicker) ?? e : e;

    /// <summary>Verknüpft ein Feld mit seiner Beschriftung (für Screenreader).</summary>
    public static void Label(UIElement control, TextBlock label) => AutomationProperties.SetLabeledBy(InputOf(control), label);

    /// <summary>Karte im Stil der Windows-Einstellungen: Symbol, Titel, Beschreibung, Steuerelement rechts.</summary>
    public static FrameworkElement SettingCard(SymbolRegular icon, string title, string? desc, UIElement? control)
    {
        var grid = new Grid();
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        grid.ColumnDefinitions.Add(new ColumnDefinition());
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var ic = new SymbolIcon(icon) { FontSize = 20, Margin = new Thickness(0, 0, 16, 0), VerticalAlignment = VerticalAlignment.Center };
        var text = new StackPanel { VerticalAlignment = VerticalAlignment.Center };
        var head = new TextBlock { Text = title, TextWrapping = TextWrapping.Wrap }.Fg();
        text.Children.Add(head);
        if (!string.IsNullOrEmpty(desc)) text.Children.Add(new TextBlock { Text = desc, Style = Style("Muted") });
        grid.Children.Add(ic);
        Grid.SetColumn(text, 1);
        grid.Children.Add(text);
        if (control != null)
        {
            if (control is FrameworkElement fe) { fe.VerticalAlignment = VerticalAlignment.Center; fe.Margin = new Thickness(16, 0, 0, 0); }
            if (control is System.Windows.Controls.Primitives.TextBoxBase or System.Windows.Controls.PasswordBox or ComboBox or ToggleSwitch)
                AutomationProperties.SetLabeledBy(control, head);
            Grid.SetColumn(control, 2);
            grid.Children.Add(control);
        }
        return new Card { Content = grid, Margin = new Thickness(0, 0, 0, 4), Padding = new Thickness(16, 12, 16, 12) };
    }

    public static StackPanel Hstack(params UIElement[] items)
    {
        var sp = new StackPanel { Orientation = Orientation.Horizontal };
        foreach (var i in items) sp.Children.Add(i);
        return sp;
    }

    public static string RandomSecret(int len = 24)
    {
        const string chars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";
        var b = System.Security.Cryptography.RandomNumberGenerator.GetBytes(len);
        return new string(b.Select(x => chars[x % chars.Length]).ToArray());
    }

    public static string RandomPassword()
    {
        var s = RandomSecret(12);
        return $"{s[..4]}-{s[4..8]}-{s[8..]}";
    }

    public static void Copy(string text)
    {
        try { Clipboard.SetText(text); _ = Toast("In die Zwischenablage kopiert"); }
        catch { _ = Toast("Die Zwischenablage ist gerade belegt – bitte noch einmal versuchen.", true); }
    }

    /// <summary>
    /// Kopiert ein Geheimnis (Passwort, Shared Secret) so, dass Windows es nicht im Zwischenablage-Verlauf
    /// speichert und nicht in die Cloud-Zwischenablage hochlädt.
    /// </summary>
    public static void CopySecret(string text)
    {
        try
        {
            var data = new DataObject();
            data.SetData(DataFormats.UnicodeText, text);
            data.SetData(DataFormats.Text, text);
            data.SetData("ExcludeClipboardContentFromMonitorProcessing", Zero());
            data.SetData("CanIncludeInClipboardHistory", Zero());
            data.SetData("CanUploadToCloudClipboard", Zero());
            Clipboard.SetDataObject(data, true);
            _ = Toast("In die Zwischenablage kopiert (nicht im Verlauf)");
        }
        catch { _ = Toast("Die Zwischenablage ist gerade belegt – bitte noch einmal versuchen.", true); }

        static MemoryStream Zero() => new(BitConverter.GetBytes(0));
    }
}

/// <summary>Dateien speichern und hochladen (CSV, Sicherung, Zertifikate).</summary>
public static class Files
{
    public static string? SaveAs(string fileName, string filter)
    {
        var d = new Microsoft.Win32.SaveFileDialog { FileName = fileName, Filter = filter };
        return d.ShowDialog() == true ? d.FileName : null;
    }

    public static string? Open(string title, string filter)
    {
        var d = new Microsoft.Win32.OpenFileDialog { Title = title, Filter = filter };
        return d.ShowDialog() == true ? d.FileName : null;
    }

    const string CsvFilter = "CSV-Datei (*.csv)|*.csv|Alle Dateien|*.*";

    /// <summary>Lädt eine CSV-Datei vom Server und speichert sie an einem gewählten Ort.</summary>
    public static async Task ExportCsv(string path, string fallbackName)
    {
        var (data, name) = await App.Api.Download(HttpMethod.Get, path);
        var file = SaveAs(name ?? fallbackName, CsvFilter);
        if (file == null) return;
        await File.WriteAllBytesAsync(file, data);
        await UI.Toast($"Gespeichert: {Path.GetFileName(file)}");
    }

    /// <summary>Lädt eine CSV-Datei hoch (Feld „file“) und meldet, wie viele Einträge neu bzw. geändert sind.</summary>
    public static async Task<bool> ImportCsv(string path, string what, string help)
    {
        // Erst erklären, welche Spalten erwartet werden, dann die Datei wählen.
        var intro = new ContentDialog(UI.RequireHost())
        {
            Title = $"{what} aus CSV importieren",
            Content = new TextBlock { Text = help, TextWrapping = TextWrapping.Wrap, MaxWidth = 520 },
            PrimaryButtonText = "Datei wählen …",
            CloseButtonText = "Abbrechen",
            DefaultButton = ContentDialogButton.Primary,
        };
        if (await intro.ShowAsync() != ContentDialogResult.Primary) return false;
        var file = Open($"{what} importieren (CSV)", CsvFilter);
        if (file == null) return false;
        var content = new MultipartFormDataContent { { new ByteArrayContent(await File.ReadAllBytesAsync(file)), "file", Path.GetFileName(file) } };
        var r = await App.Api.Call<ImportResult>(HttpMethod.Post, path, content);
        await UI.Toast($"Import abgeschlossen: {r.Created} neu, {r.Updated} aktualisiert");
        return true;
    }

    /// <summary>Entfernt Zeichen, die in Dateinamen nicht erlaubt sind.</summary>
    public static string SafeName(string name)
    {
        var bad = Path.GetInvalidFileNameChars();
        var s = new string(name.Select(c => bad.Contains(c) ? '_' : c).ToArray()).Trim();
        return s == "" ? "Datei" : s;
    }
}
