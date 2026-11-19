using System.Net.Http;
using System.Windows;
using Wpf.Ui.Controls;

namespace EasyRadius.Manager;

public partial class LoginWindow : FluentWindow
{
    readonly SavedLogin saved = SavedLogin.Load();
    bool setupMode;

    public LoginWindow(string? reason)
    {
        InitializeComponent();
        Server.Text = saved.Server;
        User.Text = saved.User;
        var pw = saved.Password;
        if (pw != null) { Pass.Password = pw; Remember.IsChecked = true; }
        if (reason != null) ShowError(reason);
        Loaded += async (_, _) =>
        {
            // Automatisch anmelden, wenn das Passwort gespeichert ist.
            if (pw != null && reason == null) await Login();
            else if (string.IsNullOrEmpty(User.Text)) User.Focus(); else Pass.Focus();
        };
    }

    void ShowError(string? msg)
    {
        Error.Title = "Anmeldung nicht möglich";
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
            App.Api = api;
            saved.Server = Server.Text.Trim();
            saved.User = User.Text.Trim();
            saved.Password = Remember.IsChecked == true ? Pass.Password : null;
            saved.Save();
            api.SessionExpired += () => Dispatcher.Invoke(() => App.ShowLogin("Die Sitzung ist abgelaufen. Bitte erneut anmelden."));
            App.ShowMain();
        }
        catch (ApiException ex) { ShowError(ex.Message); }
        catch (UriFormatException) { ShowError("Serveradresse ungültig"); }
        finally { Go.IsEnabled = true; }
    }
}
