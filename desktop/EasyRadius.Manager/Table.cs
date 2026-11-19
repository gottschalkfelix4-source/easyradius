using System.Windows;
using System.Windows.Controls;
using System.Windows.Data;
using System.Windows.Input;
using System.Windows.Media;
using Wpf.Ui.Controls;
using ListView = Wpf.Ui.Controls.ListView;
using GridView = Wpf.Ui.Controls.GridView;
using GridViewColumn = Wpf.Ui.Controls.GridViewColumn;
using TextBlock = System.Windows.Controls.TextBlock;

namespace EasyRadius.Manager;

/// <summary>Tabelle (ListView mit Spalten) im Windows-Stil mit Doppelklick und Kontextmenü.</summary>
public static class Table
{
    public record Column(string Header, string Path, double Width, string? Sub = null, bool Mono = false, string? ColorPath = null, bool Bold = false);

    public static ListView Create<T>(IEnumerable<Column> cols, Func<T, Task>? onOpen = null, params (string text, Func<T, Task> act)[] menu) where T : class
    {
        var view = new GridView();
        foreach (var c in cols)
            view.Columns.Add(new GridViewColumn { Header = c.Header, Width = c.Width, CellTemplate = Cell(c) });
        var lv = new ListView { View = view, SelectionMode = SelectionMode.Single, MinHeight = 120 };
        if (onOpen != null)
            lv.MouseDoubleClick += async (_, e) =>
            {
                if ((e.OriginalSource as DependencyObject)?.FindParent<System.Windows.Controls.ListViewItem>() != null && lv.SelectedItem is T t)
                    await Run(() => onOpen(t));
            };
        if (menu.Length > 0)
        {
            var cm = new ContextMenu();
            foreach (var (text, act) in menu)
            {
                var mi = new System.Windows.Controls.MenuItem { Header = text };
                mi.Click += async (_, _) => { if (lv.SelectedItem is T t) await Run(() => act(t)); };
                cm.Items.Add(mi);
            }
            lv.ContextMenu = cm;
        }
        lv.KeyDown += async (_, e) =>
        {
            if (e.Key == Key.Enter && onOpen != null && lv.SelectedItem is T t) await Run(() => onOpen(t));
        };
        return lv;
    }

    static async Task Run(Func<Task> f)
    {
        try { await f(); }
        catch (ApiException e) { await UI.Toast(e.Message, true); }
    }

    static DataTemplate Cell(Column c)
    {
        var sp = new FrameworkElementFactory(typeof(StackPanel));
        sp.SetValue(FrameworkElement.MarginProperty, new Thickness(0, 4, 0, 4));
        var main = new FrameworkElementFactory(typeof(TextBlock));
        main.SetBinding(TextBlock.TextProperty, new Binding(c.Path));
        main.SetValue(TextBlock.TextTrimmingProperty, TextTrimming.CharacterEllipsis);
        if (c.Mono) main.SetValue(TextBlock.FontFamilyProperty, new FontFamily("Cascadia Mono, Consolas"));
        if (c.ColorPath != null) main.SetBinding(TextBlock.ForegroundProperty, new Binding(c.ColorPath));
        if (c.Bold) main.SetValue(TextBlock.FontWeightProperty, FontWeights.SemiBold);
        sp.AppendChild(main);
        if (c.Sub != null)
        {
            var sub = new FrameworkElementFactory(typeof(TextBlock));
            sub.SetBinding(TextBlock.TextProperty, new Binding(c.Sub));
            sub.SetValue(TextBlock.FontSizeProperty, 12.0);
            sub.SetValue(TextBlock.TextTrimmingProperty, TextTrimming.CharacterEllipsis);
            sub.SetResourceReference(TextBlock.ForegroundProperty, "TextFillColorSecondaryBrush");
            sp.AppendChild(sub);
        }
        return new DataTemplate { VisualTree = sp };
    }

    public static T? FindParent<T>(this DependencyObject? d) where T : DependencyObject
    {
        while (d != null && d is not T) d = d is Visual ? VisualTreeHelper.GetParent(d) : LogicalTreeHelper.GetParent(d);
        return d as T;
    }

    /// <summary>Farbe für Status-Texte (Aktiv grün, sonst rot/grau).</summary>
    public static Brush StatusBrush(bool ok, bool warn = false)
    {
        bool dark = Wpf.Ui.Appearance.ApplicationThemeManager.GetAppTheme() == Wpf.Ui.Appearance.ApplicationTheme.Dark;
        var c = ok ? (dark ? Color.FromRgb(0x6C, 0xCB, 0x5F) : Color.FromRgb(0x0F, 0x7B, 0x0F))
            : warn ? (dark ? Color.FromRgb(0xFC, 0xE1, 0x00) : Color.FromRgb(0x9D, 0x5D, 0x00))
            : (dark ? Color.FromRgb(0xFF, 0x99, 0xA4) : Color.FromRgb(0xC4, 0x2B, 0x1C));
        return new SolidColorBrush(c);
    }
}
