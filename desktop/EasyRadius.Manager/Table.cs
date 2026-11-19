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

    /// <summary>Menüeinträge mit diesen Namen löschen etwas – sie werden auch über die Entf-Taste ausgelöst.</summary>
    static readonly string[] DeleteActions = ["Löschen", "Entfernen"];

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
                if (DeleteActions.Contains(text)) mi.InputGestureText = "Entf";
                mi.Click += async (_, _) => { if (lv.SelectedItem is T t) await Run(() => act(t)); };
                cm.Items.Add(mi);
            }
            lv.ContextMenu = cm;
        }
        var delete = menu.FirstOrDefault(m => DeleteActions.Contains(m.text)).act;
        lv.KeyDown += async (_, e) =>
        {
            if (lv.SelectedItem is not T t) return;
            if (e.Key == Key.Enter && onOpen != null) { e.Handled = true; await Run(() => onOpen(t)); }
            else if (e.Key == Key.Delete && delete != null) { e.Handled = true; await Run(() => delete(t)); }
        };
        return lv;
    }

    /// <summary>Menüeinträge, die etwas ändern – für „Nur lesen“-Konten entfallen sie.</summary>
    public static (string text, Func<T, Task> act)[] Writable<T>(params (string text, Func<T, Task> act)[] menu) => App.CanWrite ? menu : [];

    /// <summary>Schaltet einen Kontextmenü-Eintrag der Tabelle ein oder aus.</summary>
    public static void EnableMenu(System.Windows.Controls.ListView lv, string text, bool enabled, string? hint = null)
    {
        foreach (var mi in lv.ContextMenu?.Items.OfType<System.Windows.Controls.MenuItem>() ?? [])
            if (Equals(mi.Header, text))
            {
                mi.IsEnabled = enabled;
                mi.ToolTip = enabled ? null : hint;
                ToolTipService.SetShowOnDisabled(mi, true);
            }
    }

    static async Task Run(Func<Task> f)
    {
        try { await f(); }
        catch (Exception e) when (UI.IsExpected(e)) { UI.ReportError(e); }
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

    /// <summary>
    /// Farbe für Status-Texte (Aktiv grün, sonst rot bzw. gelb). Liefert die gemeinsamen Pinsel aus den App-Ressourcen,
    /// deren Farbe sich beim Designwechsel ändert.
    /// </summary>
    public static Brush StatusBrush(bool ok, bool warn = false) =>
        (Brush)Application.Current.Resources[ok ? "StatusOkBrush" : warn ? "StatusWarnBrush" : "StatusErrorBrush"];
}
