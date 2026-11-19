using System.Windows;
using System.Windows.Controls;
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

    public FormDialog(string title, UIElement body, string primary = "Speichern", ContentDialogHost? host = null) : base(host ?? UI.Host)
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
    }

    public void ShowError(string? msg)
    {
        error.Message = msg ?? "";
        error.Title = "Fehler";
        error.IsOpen = !string.IsNullOrEmpty(msg);
    }

    protected override async void OnButtonClick(ContentDialogButton button)
    {
        if (button != ContentDialogButton.Primary || OnSave == null) { base.OnButtonClick(button); return; }
        if (busy) return;
        busy = true;
        try
        {
            await OnSave();
            base.OnButtonClick(button);
        }
        catch (ApiException e) { ShowError(e.Message); }
        catch (InvalidOperationException e) { ShowError(e.Message); }
        finally { busy = false; }
    }
}

/// <summary>Baut einfache Formulare (Beschriftung, Feld, Hinweis).</summary>
public class Form
{
    public StackPanel Panel { get; } = new();

    public T Add<T>(string label, T control, string? help = null) where T : UIElement
    {
        if (!string.IsNullOrEmpty(label))
            Panel.Children.Add(new TextBlock { Text = label, Margin = new Thickness(0, 12, 0, 6), Foreground = UI.Brush("TextFillColorPrimaryBrush") });
        Panel.Children.Add(control);
        if (!string.IsNullOrEmpty(help))
            Panel.Children.Add(new TextBlock { Text = help, Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 0) });
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
        if (help != null) Panel.Children.Add(new TextBlock { Text = help, Style = UI.Style("Muted"), Margin = new Thickness(0, 4, 0, 0) });
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
            sp.Children.Add(new TextBlock { Text = cells[i].label, Margin = new Thickness(0, 12, 0, 6), Foreground = UI.Brush("TextFillColorPrimaryBrush") });
            sp.Children.Add(cells[i].control);
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
    public static ContentDialogHost Host => ((MainWindow)Application.Current.MainWindow).DialogHost;

    public static Brush Brush(string key) => (Brush)Application.Current.FindResource(key);
    public static Style Style(string key) => (Style)Application.Current.FindResource(key);

    public static Task Toast(string msg, bool error = false)
    {
        ((MainWindow)Application.Current.MainWindow).Toast(msg, error);
        return Task.CompletedTask;
    }

    public static async Task<bool> Confirm(string title, string text, string action)
    {
        var d = new ContentDialog(Host)
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
        var d = new ContentDialog(Host) { Title = title, Content = content, CloseButtonText = "Schließen", DialogMaxWidth = 640 };
        await d.ShowAsync();
    }

    public static Button Btn(string text, SymbolRegular icon, Func<Task> onClick, ControlAppearance look = ControlAppearance.Secondary)
    {
        var b = new Button { Content = text, Icon = new SymbolIcon(icon), Appearance = look, Margin = new Thickness(0, 0, 8, 0) };
        b.Click += async (_, _) =>
        {
            b.IsEnabled = false;
            try { await onClick(); }
            catch (ApiException e) { await Toast(e.Message, true); }
            finally { b.IsEnabled = true; }
        };
        return b;
    }

    /// <summary>Karte im Stil der Windows-Einstellungen: Symbol, Titel, Beschreibung, Steuerelement rechts.</summary>
    public static FrameworkElement SettingCard(SymbolRegular icon, string title, string? desc, UIElement? control)
    {
        var grid = new Grid();
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        grid.ColumnDefinitions.Add(new ColumnDefinition());
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var ic = new SymbolIcon(icon) { FontSize = 20, Margin = new Thickness(0, 0, 16, 0), VerticalAlignment = VerticalAlignment.Center };
        var text = new StackPanel { VerticalAlignment = VerticalAlignment.Center };
        text.Children.Add(new TextBlock { Text = title, Foreground = Brush("TextFillColorPrimaryBrush"), TextWrapping = TextWrapping.Wrap });
        if (!string.IsNullOrEmpty(desc)) text.Children.Add(new TextBlock { Text = desc, Style = Style("Muted") });
        grid.Children.Add(ic);
        Grid.SetColumn(text, 1);
        grid.Children.Add(text);
        if (control != null)
        {
            if (control is FrameworkElement fe) { fe.VerticalAlignment = VerticalAlignment.Center; fe.Margin = new Thickness(16, 0, 0, 0); }
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
        catch { }
    }
}
