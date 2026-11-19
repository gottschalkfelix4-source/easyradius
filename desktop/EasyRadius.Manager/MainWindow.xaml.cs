using Wpf.Ui.Appearance;
using Wpf.Ui.Controls;

namespace EasyRadius.Manager;

public partial class MainWindow : FluentWindow
{
    public MainWindow()
    {
        InitializeComponent();
        Bar.Title = $"EasyRadius – {App.Api.BaseUri.Authority}";
        Title = Bar.Title;
        Loaded += (_, _) =>
        {
            SystemThemeWatcher.Watch(this);
            Nav.Navigate(typeof(DashboardPage));
        };
    }

    public void Toast(string message, bool error = false)
    {
        var s = new Snackbar(Snackbars)
        {
            Title = error ? "Hinweis" : "Erledigt",
            Content = message,
            Appearance = error ? ControlAppearance.Danger : ControlAppearance.Secondary,
            Icon = new SymbolIcon(error ? SymbolRegular.ErrorCircle24 : SymbolRegular.CheckmarkCircle24),
            Timeout = TimeSpan.FromSeconds(error ? 6 : 3),
        };
        s.Show();
    }

    public void Go(Type page) => Nav.Navigate(page);
}
