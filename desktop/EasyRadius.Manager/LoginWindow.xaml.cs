using System.Net.Http;
using System.Windows;
using Wpf.Ui.Appearance;
using Wpf.Ui.Controls;
using TextBlock = System.Windows.Controls.TextBlock;

namespace EasyRadius.Manager;

public partial class LoginWindow : FluentWindow
{
    readonly SavedLogin saved = SavedLogin.Load();
    bool setupMode;

    public LoginWindow(string? reason, bool autoLogin = true, bool info = false)
    {
        InitializeComponent();
        MaxHeight = SystemParameters.WorkArea.Height;
        Server.Text = saved.Server;
        User.Text = saved.User;
        var pw = saved.Password;
        if (pw != null) { Pass.Password = pw; Remember.IsChecked = true; }
        if (reason != null)
        {
            ShowError(reason);
            if (info) { Error.Title = "Hinweis"; Error.Severity = InfoBarSeverity.Informational; }
        }
        Server.TextChanged += (_, _) => UpdateInsecure();
        UpdateInsecure();
        Loaded += async (_, _) =>
        {
            SystemThemeWatcher.Watch(this);
            // Automatisch anmelden, wenn das Passwort gespeichert ist – aber nie unverschlüsselt zu einem anderen Rechner.
            if (pw != null && reason == null && autoLogin && !IsInsecure(Server.Text)) await Login();
            else if (string.IsNullOrEmpty(User.Text)) User.Focus(); else Pass.Focus();
        };
    }

    static bool IsInsecure(string server)
    {
        try { return ApiClient.IsInsecureRemote(ApiClient.ParseServer(server)); } catch { return false; }
    }

    void UpdateInsecure()
    {
        Uri? u = null;
        try { u = ApiClient.ParseServer(Server.Text); } catch { }
        var insecure = u != null && ApiClient.IsInsecureRemote(u);
        Insecure.Message = insecure
            ? $"Passwort wird im Klartext übertragen. Besser https://{u!.IdnHost}:{ApiClient.HttpsPort} verwenden."
            : "";
        Insecure.IsOpen = insecure;
    }

    void ShowError(string? msg)
    {
        Error.Title = "Anmeldung nicht möglich";
        Error.Severity = InfoBarSeverity.Error;
        Error.Message = msg ?? "";
        Error.IsOpen = !string.IsNullOrEmpty(msg);
    }

    async void OnLogin(object sender, RoutedEventArgs e) => await Login();

    async Task Login()
    {
        ShowError(null);
        Go.IsEnabled = false;
        try
        {
            var api = new ApiClient(Server.Text.Trim());
            var state = await api.Get<StateInfo>("/api/state");
            if (state.Setup && !setupMode)
            {
                setupMode = true;
                Lead.Text = "Willkommen! Lege den ersten Administrator an.";
                RepeatBox.Visibility = Visibility.Visible;
                Go.Content = "Administrator anlegen";
                if (string.IsNullOrEmpty(User.Text)) User.Text = "admin";
                return;
            }
            if (setupMode)
            {
                if (Pass.Password != Pass2.Password) { ShowError("Die Passwörter stimmen nicht überein."); return; }
                await api.Call(HttpMethod.Post, "/api/setup", new { username = User.Text.Trim(), password = Pass.Password });
            }
            else
            {
                await api.Call(HttpMethod.Post, "/api/login", new { username = User.Text.Trim(), password = Pass.Password });
            }
            App.State = await api.Get<StateInfo>("/api/state");
            api.LoginUser = User.Text.Trim();
            App.Api = api;
            saved.Server = Server.Text.Trim();
            saved.User = User.Text.Trim();
            saved.Password = Remember.IsChecked == true ? Pass.Password : null;
            saved.Save();
            api.SessionExpired += () =>
            {
                if (App.Api == api) App.ShowLogin("Die Sitzung ist abgelaufen. Bitte erneut anmelden.", info: true);
            };
            App.ShowMain();
        }
        catch (CertificateException ex)
        {
            Go.IsEnabled = true;
            if (await AskTrust(ex)) { SavedLogin.SetPin(ex.HostKey, ex.Fingerprint); await Login(); }
            else ShowError(ex.Message);
        }
        catch (ApiException ex) { ShowError(ex.Message); }
        catch (UriFormatException) { ShowError("Serveradresse ungültig"); }
        catch (Exception ex) when (UI.IsExpected(ex)) { ShowError(UI.Describe(ex)); }
        finally { Go.IsEnabled = true; }
    }

    /// <summary>Zeigt den Fingerabdruck eines unbekannten (oder geänderten) Zertifikats und fragt, ob ihm vertraut werden soll.</summary>
    async Task<bool> AskTrust(CertificateException ex)
    {
        var text = ex.Changed
            ? $"ACHTUNG: Der Server {ex.HostKey} zeigt ein anderes Zertifikat als bei der letzten Verbindung.\n\n" +
              "Das ist normal, wenn das Zertifikat absichtlich neu erzeugt oder ausgetauscht wurde. Andernfalls könnte sich jemand " +
              "zwischen diesen PC und den Server geschaltet haben – dann auf keinen Fall vertrauen.\n\n"
            : $"Windows kennt das Zertifikat von {ex.HostKey} nicht (z. B. weil es von einer eigenen Zertifizierungsstelle stammt).\n\n";
        text += $"SHA-256-Fingerabdruck:\n{ex.Fingerprint}\n\n" +
                "Vergleiche ihn mit dem Fingerabdruck in EasyRadius auf dem Server (Seite „Zertifikat“). Nur bei Übereinstimmung vertrauen.";
        var box = new Wpf.Ui.Controls.MessageBox
        {
            Title = "Zertifikat prüfen",
            Content = new TextBlock { Text = text, TextWrapping = TextWrapping.Wrap, MaxWidth = 460 },
            PrimaryButtonText = ex.Changed ? "Neuem Zertifikat vertrauen" : "Vertrauen",
            PrimaryButtonAppearance = ex.Changed ? ControlAppearance.Danger : ControlAppearance.Primary,
            CloseButtonText = "Abbrechen",
            Owner = this,
        };
        return await box.ShowDialogAsync() == Wpf.Ui.Controls.MessageBoxResult.Primary;
    }
}
