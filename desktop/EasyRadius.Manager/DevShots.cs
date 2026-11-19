#if DEBUG
using System.IO;
using System.Net.Http;
using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;
using System.Windows.Media.Imaging;
using Wpf.Ui.Appearance;

namespace EasyRadius.Manager;

/// <summary>Nur in Testbuilds: meldet sich an, öffnet jede Seite und speichert Bildschirmfotos des Fensters.</summary>
public static class DevShots
{
    [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr h, out RECT r);
    [DllImport("user32.dll")] static extern bool SetForegroundWindow(IntPtr h);
    [DllImport("user32.dll")] static extern IntPtr GetDC(IntPtr h);
    [DllImport("user32.dll")] static extern int ReleaseDC(IntPtr h, IntPtr dc);
    [DllImport("gdi32.dll")] static extern IntPtr CreateCompatibleDC(IntPtr dc);
    [DllImport("gdi32.dll")] static extern IntPtr CreateCompatibleBitmap(IntPtr dc, int w, int h);
    [DllImport("gdi32.dll")] static extern IntPtr SelectObject(IntPtr dc, IntPtr o);
    [DllImport("gdi32.dll")] static extern bool BitBlt(IntPtr d, int x, int y, int w, int h, IntPtr s, int sx, int sy, int op);
    [DllImport("gdi32.dll")] static extern bool DeleteObject(IntPtr o);
    [DllImport("gdi32.dll")] static extern bool DeleteDC(IntPtr dc);
    [DllImport("dwmapi.dll")] static extern int DwmGetWindowAttribute(IntPtr h, int attr, out RECT r, int size);
    struct RECT { public int L, T, R, B; }

    public static bool Active => Environment.GetEnvironmentVariable("EASYRADIUS_SHOTS") != null;

    public static async Task Run()
    {
        var dir = Environment.GetEnvironmentVariable("EASYRADIUS_SHOTS")!;
        try { await RunInner(dir); }
        catch (Exception e) { Directory.CreateDirectory(dir); File.WriteAllText(Path.Combine(dir, "fehler.txt"), e.ToString()); Application.Current.Shutdown(); }
    }

    static async Task RunInner(string dir)
    {
        Directory.CreateDirectory(dir);
        if (Environment.GetEnvironmentVariable("EASYRADIUS_THEME") is "dark" or "light")
            ApplicationThemeManager.Apply(Environment.GetEnvironmentVariable("EASYRADIUS_THEME") == "dark" ? ApplicationTheme.Dark : ApplicationTheme.Light, Wpf.Ui.Controls.WindowBackdropType.Mica, true);
        var login = new LoginWindow(null, autoLogin: false);
        Application.Current.MainWindow = login;
        login.Show();
        await Task.Delay(1500);
        Shot(login, Path.Combine(dir, "anmeldung.png"));
        var api = new ApiClient(Environment.GetEnvironmentVariable("EASYRADIUS_SERVER")!);
        await api.Call(HttpMethod.Post, "/api/login", new { username = Environment.GetEnvironmentVariable("EASYRADIUS_USER"), password = Environment.GetEnvironmentVariable("EASYRADIUS_PASS") });
        App.Api = api;
        App.State = await api.Get<StateInfo>("/api/state");
        App.ShowMain();
        var w = (MainWindow)Application.Current.MainWindow;
        w.Width = 1280; w.Height = 820;
        w.Left = 40; w.Top = 40;
        await Task.Delay(2500);
        var pages = new (string file, Type page)[]
        {
            ("uebersicht", typeof(DashboardPage)), ("benutzer", typeof(UsersPage)), ("gruppen", typeof(GroupsPage)),
            ("mac-adressen", typeof(MacsPage)), ("geraete", typeof(ClientsPage)), ("sitzungen", typeof(SessionsPage)),
            ("protokoll", typeof(LogPage)), ("zertifikat", typeof(CertPage)), ("ad", typeof(AdPage)),
            ("konten", typeof(AccountsPage)), ("einstellungen", typeof(SettingsPage)),
        };
        foreach (var (file, page) in pages)
        {
            w.Go(page);
            await Task.Delay(2200);
            Shot(w, Path.Combine(dir, file + ".png"));
        }
        // Dialog mit Attribut-Editor
        w.Go(typeof(GroupsPage));
        await Task.Delay(1500);
        var groups = await api.Get<List<Group>>("/api/groups");
        var g = groups.FirstOrDefault(x => x.Name == "Netzwerk-Admins") ?? groups.FirstOrDefault();
        if (g != null)
        {
            _ = GroupsPage.Edit(g);
            await Task.Delay(2500);
            Shot(w, Path.Combine(dir, "gruppe-dialog.png"));
        }
        // Schreibvorgänge über die App-Modelle prüfen.
        var log = new List<string>();
        var gs = await api.Get<List<Group>>("/api/groups");
        var gg = gs.FirstOrDefault(x => x.Name == "Gäste");
        var demo = await api.Get<List<User>>("/api/users");
        if (gg == null || !demo.Any(x => x.Username == "lena.koch")) { Application.Current.Shutdown(); return; } // andere Demodaten: nur Screenshots
        gg.Enabled = false;
        await api.Call(HttpMethod.Put, "/api/groups/" + gg.Id, gg);
        var after = (await api.Get<List<Group>>("/api/groups")).First(x => x.Id == gg.Id);
        log.Add($"Gruppe gesperrt: {!after.Enabled}, Attribute erhalten: {after.Attributes?.Count == gg.Attributes?.Count}");
        after.Enabled = true;
        await api.Call(HttpMethod.Put, "/api/groups/" + gg.Id, after);
        var us = await api.Get<List<User>>("/api/users");
        var u = us.First(x => x.Username == "lena.koch");
        var body = new { username = u.Username, fullName = u.FullName, password = "", groupId = u.GroupId, vlan = u.Vlan, expiresAt = u.ExpiresAt, note = u.Note, enabled = u.Enabled,
            attributes = new List<Attr> { new() { Kind = "reply", Name = "Session-Timeout", Code = 27, Type = "integer", Value = "3600" }, new() { Kind = "check", Name = "Calling-Station-Id", Code = 31, Type = "string", Value = "02-00-*" } } };
        await api.Call(HttpMethod.Put, "/api/users/" + u.Id, body);
        var u2 = (await api.Get<List<User>>("/api/users")).First(x => x.Id == u.Id);
        log.Add($"Benutzer-Attribute: {string.Join("; ", u2.Attributes!.Select(a => $"{a.KindText} {a.Name}={a.Value}"))}");
        File.WriteAllLines(Path.Combine(dir, "pruefung.txt"), log);
        Application.Current.Shutdown();
    }

    static void Shot(Window w, string file)
    {
        var h = new WindowInteropHelper(w).Handle;
        SetForegroundWindow(h);
        if (DwmGetWindowAttribute(h, 9, out var r, Marshal.SizeOf<RECT>()) != 0) GetWindowRect(h, out r);
        int width = r.R - r.L, height = r.B - r.T;
        var screen = GetDC(IntPtr.Zero);
        var mem = CreateCompatibleDC(screen);
        var bmp = CreateCompatibleBitmap(screen, width, height);
        var old = SelectObject(mem, bmp);
        BitBlt(mem, 0, 0, width, height, screen, r.L, r.T, 0x00CC0020);
        SelectObject(mem, old);
        var src = Imaging.CreateBitmapSourceFromHBitmap(bmp, IntPtr.Zero, Int32Rect.Empty, BitmapSizeOptions.FromEmptyOptions());
        DeleteObject(bmp); DeleteDC(mem); ReleaseDC(IntPtr.Zero, screen);
        var enc = new PngBitmapEncoder();
        enc.Frames.Add(BitmapFrame.Create(src));
        using var fs = File.Create(file);
        enc.Save(fs);
    }
}
#endif
