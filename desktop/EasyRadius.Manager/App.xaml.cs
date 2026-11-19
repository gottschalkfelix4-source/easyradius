using System.Windows;
using System.Windows.Threading;
using Wpf.Ui.Appearance;

namespace EasyRadius.Manager;

public partial class App : Application
{
    public static ApiClient Api { get; set; } = null!;
    public static StateInfo State { get; set; } = new();

    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);
        ApplicationThemeManager.Apply(ApplicationThemeManager.GetSystemTheme() == SystemTheme.Dark
            ? ApplicationTheme.Dark : ApplicationTheme.Light, Wpf.Ui.Controls.WindowBackdropType.Mica, true);
        DispatcherUnhandledException += OnError;
#if DEBUG
        if (DevShots.Active) { _ = DevShots.Run(); return; }
#endif
        ShowLogin();
    }

    void OnError(object sender, DispatcherUnhandledExceptionEventArgs e)
    {
        e.Handled = true;
        var msg = e.Exception is ApiException ? e.Exception.Message : e.Exception.ToString();
        if (MainWindow is MainWindow m) m.Toast(msg, true);
        else MessageBox.Show(msg, "EasyRadius", MessageBoxButton.OK, MessageBoxImage.Warning);
    }

    // Fensterwechsel: erst das neue Fenster zum Hauptfenster machen, dann das alte schließen.
    // Nur das Schließen des aktuellen Hauptfensters beendet die App.
    public static void ShowLogin(string? reason = null) => Swap(new LoginWindow(reason));

    public static void ShowMain() => Swap(new MainWindow());

    static void Swap(Window next)
    {
        var old = Current.Windows.OfType<Window>().Where(w => w != next).ToList();
        Current.MainWindow = next;
        next.Closed += (_, _) => { if (Current.MainWindow == next) Current.Shutdown(); };
        next.Show();
        foreach (var w in old) w.Close();
    }
}
