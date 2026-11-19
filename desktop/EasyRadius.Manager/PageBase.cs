using System.Windows;
using System.Windows.Controls;
using Wpf.Ui.Controls;
using TextBlock = System.Windows.Controls.TextBlock;

namespace EasyRadius.Manager;

/// <summary>Gemeinsamer Aufbau aller Seiten: Titel, Untertitel, Aktionen rechts, Inhalt darunter.</summary>
public abstract class PageBase : Page
{
    protected readonly StackPanel Body = new() { Margin = new Thickness(0, 24, 0, 40) };
    readonly StackPanel actions = new() { Orientation = Orientation.Horizontal, VerticalAlignment = VerticalAlignment.Bottom };
    readonly TextBlock subtitle = new() { Style = UI.Style("SubTitle") };

    protected PageBase(string title, string sub)
    {
        var head = new Grid { Margin = new Thickness(0, 12, 0, 0) };
        head.ColumnDefinitions.Add(new ColumnDefinition());
        head.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var t = new StackPanel();
        t.Children.Add(new TextBlock { Text = title, Style = UI.Style("PageTitle") });
        subtitle.Text = sub;
        t.Children.Add(subtitle);
        head.Children.Add(t);
        Grid.SetColumn(actions, 1);
        head.Children.Add(actions);

        var root = new StackPanel { MaxWidth = 1200 };
        root.Children.Add(head);
        root.Children.Add(Body);
        Content = new ScrollViewer { Content = root, VerticalScrollBarVisibility = ScrollBarVisibility.Auto, Padding = new Thickness(0, 0, 16, 0) };
        Loaded += async (_, _) => await SafeReload();
    }

    protected void Subtitle(string s) => subtitle.Text = s;
    protected void Actions(params UIElement[] items)
    {
        actions.Children.Clear();
        foreach (var i in items) actions.Children.Add(i);
    }

    protected abstract Task Reload();

    public async Task SafeReload()
    {
        try { await Reload(); }
        catch (ApiException e) { await UI.Toast(e.Message, true); }
    }

    protected static FrameworkElement Section(string text) => new TextBlock { Text = text, Style = UI.Style("SectionTitle") };

    protected static Card CardOf(UIElement content, Thickness? pad = null) =>
        new() { Content = content, Padding = pad ?? new Thickness(20, 16, 20, 16), Margin = new Thickness(0, 0, 0, 8) };
}
