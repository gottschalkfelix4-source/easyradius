using System.IO;
using System.Net;
using System.Windows;
using System.Windows.Media;
using System.Windows.Threading;
using Wpf.Ui.Appearance;

namespace EasyRadius.Manager;

public partial class App : Application
{
    public static ApiClient Api { get; set; } = null!;
    public static StateInfo State { get; set; } = new();

    /// <summary>Darf das angemeldete Konto ändern? („Nur lesen“-Konten nicht.)</summary>
    public static bool CanWrite => !State.IsViewer;

    public static string LogPath => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "EasyRadius", "verwaltung.log");

    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);
        DispatcherUnhandledException += OnError;
        AppDomain.CurrentDomain.UnhandledException += (_, a) => { if (a.ExceptionObject is Exception ex) Log(ex, "Unbehandelter Fehler"); };
        TaskScheduler.UnobservedTaskException += (_, a) => { Log(a.Exception, "Nicht beobachteter Fehler"); a.SetObserved(); };
        ApplicationThemeManager.Changed += (theme, _) =>
        {
            if (Dispatcher.CheckAccess()) ApplyThemeColors(theme);
            else Dispatcher.BeginInvoke(() => ApplyThemeColors(theme));
        };
        ApplicationThemeManager.Apply(ApplicationThemeManager.GetSystemTheme() == SystemTheme.Dark
            ? ApplicationTheme.Dark : ApplicationTheme.Light, Wpf.Ui.Controls.WindowBackdropType.Mica, true);
        ApplyThemeColors(ApplicationThemeManager.GetAppTheme());
#if DEBUG
        if (DevShots.Active) { _ = DevShots.Run(); return; }
#endif
        ShowLogin();
    }

    // Eigene Farben passend zum hellen bzw. dunklen Design. Die Pinsel werden verändert statt ersetzt,
    // damit auch bereits angezeigte Elemente (z. B. Statusspalten) sofort mitwechseln.
    static readonly (string Key, Color Light, Color Dark)[] ThemeColors =
    [
        ("CheckBrush", Color.FromRgb(0xC4, 0x2B, 0x1C), Color.FromRgb(0xFF, 0x99, 0xA4)),
        ("ReplyBrush", Color.FromRgb(0x00, 0x5F, 0xB8), Color.FromRgb(0x60, 0xCD, 0xFF)),
        ("StatusOkBrush", Color.FromRgb(0x0F, 0x7B, 0x0F), Color.FromRgb(0x6C, 0xCB, 0x5F)),
        ("StatusErrorBrush", Color.FromRgb(0xC4, 0x2B, 0x1C), Color.FromRgb(0xFF, 0x99, 0xA4)),
        ("StatusWarnBrush", Color.FromRgb(0x9D, 0x5D, 0x00), Color.FromRgb(0xFC, 0xE1, 0x00)),
    ];

    static void ApplyThemeColors(ApplicationTheme theme)
    {
        if (Current == null) return;
        bool dark = theme == ApplicationTheme.Dark;
        foreach (var (key, light, darkColor) in ThemeColors)
        {
            var c = dark ? darkColor : light;
            if (Current.Resources[key] is SolidColorBrush b && !b.IsFrozen) b.Color = c;
            else Current.Resources[key] = new SolidColorBrush(c);
        }
    }

    void OnError(object sender, DispatcherUnhandledExceptionEventArgs e)
    {
        e.Handled = true;
        var ex = e.Exception;
        if (ex is ApiException { Status: HttpStatusCode.Unauthorized }) return; // Anmeldefenster kommt von selbst
        string msg;
        if (UI.IsExpected(ex)) msg = UI.Describe(ex);
        else
        {
            Log(ex, "Unbehandelter Fehler");
            msg = $"Ein unerwarteter Fehler ist aufgetreten: {ex.Message}\nEinzelheiten stehen in {LogPath}";
        }
        if (Windows.OfType<MainWindow>().Any()) _ = UI.Toast(msg, true);
        else MessageBox.Show(msg, "EasyRadius", MessageBoxButton.OK, MessageBoxImage.Warning);
    }

    /// <summary>Schreibt Fehlerdetails in %LOCALAPPDATA%\EasyRadius\verwaltung.log (max. ca. 1 MB, danach .old).</summary>
    public static void Log(Exception ex, string context)
    {
        try
        {
            var file = LogPath;
            Directory.CreateDirectory(Path.GetDirectoryName(file)!);
            if (File.Exists(file) && new FileInfo(file).Length > 1_000_000) File.Move(file, file + ".old", true);
            File.AppendAllText(file, $"[{DateTime.Now:yyyy-MM-dd HH:mm:ss}] {context}{Environment.NewLine}{ex}{Environment.NewLine}{Environment.NewLine}");
        }
        catch { }
    }

    // Fensterwechsel: erst das neue Fenster zum Hauptfenster machen, dann das alte schließen.
    // Nur das Schließen des aktuellen Hauptfensters beendet die App.
    public static void ShowLogin(string? reason = null, bool autoLogin = true, bool info = false) => Swap(new LoginWindow(reason, autoLogin, info));

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
