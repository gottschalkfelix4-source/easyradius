using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Http.Json;
using System.Net.Security;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.Json.Serialization.Metadata;
using System.Text.RegularExpressions;

namespace EasyRadius.Manager;

/// <summary>Fehler mit verständlicher Meldung vom Server.</summary>
public class ApiException(string message, HttpStatusCode status = 0) : Exception(message)
{
    public HttpStatusCode Status { get; } = status;

    public const string Unsupported = "Diese Server-Version unterstützt das noch nicht.";

    /// <summary>404 ohne Fehlertext = die Adresse gibt es auf dem Server (noch) nicht.</summary>
    public bool IsMissingEndpoint => Status == HttpStatusCode.NotFound && Message == "Fehler 404";

    /// <summary>Führt einen Aufruf einer neueren Funktion aus und übersetzt „gibt es nicht“ in eine verständliche Meldung.</summary>
    public static async Task<T> NewFeature<T>(Func<Task<T>> call)
    {
        try { return await call(); }
        catch (ApiException e) when (e.IsMissingEndpoint) { throw new ApiException(Unsupported, HttpStatusCode.NotFound); }
    }

    public static async Task NewFeature(Func<Task> call)
    {
        try { await call(); }
        catch (ApiException e) when (e.IsMissingEndpoint) { throw new ApiException(Unsupported, HttpStatusCode.NotFound); }
    }
}

/// <summary>Das HTTPS-Zertifikat des Servers ist Windows nicht bekannt (neu) oder hat sich geändert.</summary>
public class CertificateException(string message, string hostKey, string fingerprint, bool changed) : ApiException(message)
{
    public string HostKey { get; } = hostKey;
    public string Fingerprint { get; } = fingerprint;
    public bool Changed { get; } = changed;
}

/// <summary>Spricht mit der JSON-API des EasyRadius-Dienstes (lokal oder auf einem anderen Rechner).</summary>
public partial class ApiClient
{
    public const int HttpPort = 8088;
    public const int HttpsPort = 8443;
    const string Unexpected = "Unerwartete Antwort vom Server – ist das ein EasyRadius?";

    public static readonly JsonSerializerOptions Json = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.Never,
        // null vom Server überschreibt keine Standardwerte (leere Listen, leere Texte, Unterobjekte).
        TypeInfoResolver = new DefaultJsonTypeInfoResolver { Modifiers = { KeepDefaultsOnNull } },
    };

    static void KeepDefaultsOnNull(JsonTypeInfo info)
    {
        if (info.Kind != JsonTypeInfoKind.Object) return;
        foreach (var p in info.Properties)
        {
            if (p.Set is not { } set || (p.PropertyType.IsValueType && Nullable.GetUnderlyingType(p.PropertyType) == null)) continue;
            p.Set = (obj, value) => { if (value != null) set(obj, value); };
        }
    }

    readonly HttpClient http;
    readonly CookieContainer Cookies = new();
    public Uri BaseUri { get; }

    /// <summary>Benutzer der aktuellen Sitzung – für die stille Neuanmeldung mit dem gespeicherten Passwort.</summary>
    public string? LoginUser { get; set; }

    int session;
    int expired;
    readonly SemaphoreSlim reloginLock = new(1, 1);
    CertProblem? certProblem;

    record CertProblem(string HostKey, string Fingerprint, bool Changed);

    public ApiClient(string server)
    {
        BaseUri = ParseServer(server);
        http = new HttpClient(CreateHandler()) { BaseAddress = BaseUri, Timeout = TimeSpan.FromSeconds(30) };
        http.DefaultRequestHeaders.Add("X-EasyRadius", "1");
    }

    // ---------- Serveradresse ----------

    [GeneratedRegex(@"^(\[[^\]]+\]|[^:\[\]]+):(\d{1,5})$")]
    private static partial Regex HostPort();

    /// <summary>
    /// Ohne Schema: lokale Adressen → http://…:8088, alle anderen → https://…:8443.
    /// Angegebenes Schema und angegebener Port werden übernommen (alte Einträge wie „localhost:8088“ funktionieren weiter).
    /// </summary>
    public static Uri ParseServer(string input)
    {
        var s = (input ?? "").Trim();
        string? scheme = null;
        var i = s.IndexOf("://", StringComparison.Ordinal);
        if (i >= 0)
        {
            scheme = s[..i].ToLowerInvariant();
            s = s[(i + 3)..];
            if (scheme is not ("http" or "https")) throw new UriFormatException("Nur http:// oder https://");
        }
        var cut = s.IndexOfAny(['/', '?', '#']);
        var authority = cut >= 0 ? s[..cut] : s;
        var at = authority.LastIndexOf('@');
        if (at >= 0) authority = authority[(at + 1)..];

        string host = authority;
        int? port = null;
        var m = HostPort().Match(authority);
        if (m.Success)
        {
            host = m.Groups[1].Value;
            if (!int.TryParse(m.Groups[2].Value, out var p) || p is < 1 or > 65535) throw new UriFormatException("Ungültiger Port");
            port = p;
        }
        if (host.Contains(':') && !host.StartsWith('[')) host = $"[{host}]"; // IPv6 ohne Klammern
        if (host.Trim('[', ']') == "") throw new UriFormatException("Kein Servername");

        bool loopback = IsLoopbackHost(host);
        scheme ??= port switch
        {
            HttpPort => "http",
            HttpsPort => "https",
            null => loopback ? "http" : "https",
            _ => loopback ? "http" : "https",
        };
        port ??= scheme == "https" ? HttpsPort : HttpPort;
        return new Uri($"{scheme}://{host}:{port}/");
    }

    /// <summary>localhost, 127.x.x.x, ::1 (mit oder ohne eckige Klammern).</summary>
    public static bool IsLoopbackHost(string host)
    {
        var h = host.Trim().Trim('[', ']');
        if (h.Equals("localhost", StringComparison.OrdinalIgnoreCase) || h.EndsWith(".localhost", StringComparison.OrdinalIgnoreCase)) return true;
        return IPAddress.TryParse(h, out var ip) && IPAddress.IsLoopback(ip);
    }

    /// <summary>Unverschlüsselt (http) zu einem anderen Rechner – das Passwort ginge im Klartext übers Netz.</summary>
    public static bool IsInsecureRemote(Uri u) => u.Scheme == Uri.UriSchemeHttp && !IsLoopbackHost(u.IdnHost);

    public bool Insecure => IsInsecureRemote(BaseUri);

    public static string HostKey(Uri u)
    {
        var h = u.IdnHost.Trim('[', ']').ToLowerInvariant();
        return (h.Contains(':') ? $"[{h}]" : h) + ":" + u.Port;
    }

    public string Url(string path) => new Uri(BaseUri, path).ToString();

    // ---------- Zertifikat (Trust on first use) ----------

    HttpClientHandler CreateHandler() => new()
    {
        CookieContainer = Cookies,
        UseCookies = true,
        ServerCertificateCustomValidationCallback = ValidateCertificate,
    };

    bool ValidateCertificate(HttpRequestMessage req, X509Certificate2? cert, X509Chain? chain, SslPolicyErrors errors)
    {
        if (errors == SslPolicyErrors.None) return true;
        if (cert == null) return false;
        var key = HostKey(req.RequestUri ?? BaseUri);
        var fp = Fingerprint(cert);
        var pin = SavedLogin.GetPin(key);
        if (pin != null && string.Equals(pin, fp, StringComparison.OrdinalIgnoreCase)) return true;
        certProblem = new CertProblem(key, fp, pin != null);
        return false;
    }

    /// <summary>SHA-256 des Zertifikats im Format AA:BB:CC:…</summary>
    public static string Fingerprint(X509Certificate2 cert) =>
        string.Join(":", SHA256.HashData(cert.RawData).Select(b => b.ToString("X2")));

    Exception TranslateConnectError(HttpRequestException e)
    {
        var p = Interlocked.Exchange(ref certProblem, null);
        if (p == null) return new ApiException($"Server {BaseUri.Authority} nicht erreichbar ({e.Message})");
        return p.Changed
            ? new CertificateException($"Achtung: Das Zertifikat von {p.HostKey} hat sich geändert! Die Verbindung wurde abgelehnt, weil sich jemand dazwischengeschaltet haben könnte. " +
                $"Neuer Fingerabdruck (SHA-256): {p.Fingerprint}. Nur wenn das Zertifikat absichtlich erneuert wurde, kann ihm neu vertraut werden.", p.HostKey, p.Fingerprint, true)
            : new CertificateException($"Dem Zertifikat von {p.HostKey} vertraut Windows nicht (z. B. eigene Zertifizierungsstelle). Fingerabdruck (SHA-256): {p.Fingerprint}", p.HostKey, p.Fingerprint, false);
    }

    // ---------- Anfragen ----------

    static bool IsAuthPath(string path) =>
        path.StartsWith("/api/login", StringComparison.OrdinalIgnoreCase) || path.StartsWith("/api/setup", StringComparison.OrdinalIgnoreCase)
        || path.StartsWith("/api/logout", StringComparison.OrdinalIgnoreCase);

    async Task<HttpResponseMessage> SendOnce(HttpMethod method, string path, object? body)
    {
        var req = new HttpRequestMessage(method, path);
        if (body is HttpContent c) req.Content = c;
        else if (body != null) req.Content = JsonContent.Create(body, options: Json);
        try { return await http.SendAsync(req); }
        catch (HttpRequestException e) { throw TranslateConnectError(e); }
        catch (TaskCanceledException) { throw new ApiException($"Zeitüberschreitung bei {BaseUri.Authority}"); }
    }

    async Task<HttpResponseMessage> Send(HttpMethod method, string path, object? body)
    {
        var seen = session;
        var res = await SendOnce(method, path, body);
        if (res.StatusCode == HttpStatusCode.Unauthorized && !IsAuthPath(path) && await TryRelogin(seen))
        {
            res.Dispose();
            res = await SendOnce(method, path, body);
        }
        if (!res.IsSuccessStatusCode)
        {
            string msg = $"Fehler {(int)res.StatusCode}";
            try
            {
                var doc = await res.Content.ReadFromJsonAsync<JsonElement>(Json);
                if (doc.ValueKind == JsonValueKind.Object && doc.TryGetProperty("error", out var e) && e.ValueKind == JsonValueKind.String)
                    msg = e.GetString() ?? msg;
            }
            catch { }
            var status = res.StatusCode;
            res.Dispose();
            if (status == HttpStatusCode.Unauthorized && !IsAuthPath(path)) RaiseExpired();
            throw new ApiException(msg, status);
        }
        return res;
    }

    /// <summary>Meldet sich mit dem gespeicherten Passwort still neu an (einmal je abgelaufener Sitzung).</summary>
    async Task<bool> TryRelogin(int seenSession)
    {
        if (LoginUser == null || Insecure || Volatile.Read(ref expired) == 1) return false;
        await reloginLock.WaitAsync();
        try
        {
            if (session != seenSession) return true; // inzwischen von einer anderen Anfrage erneuert
            var saved = SavedLogin.Load();
            var pw = saved.Password;
            if (pw == null || !string.Equals(saved.User, LoginUser, StringComparison.Ordinal) || !SameServer(saved.Server)) return false;
            using var res = await SendOnce(HttpMethod.Post, "/api/login", new { username = LoginUser, password = pw });
            if (!res.IsSuccessStatusCode) return false;
            Interlocked.Increment(ref session);
            return true;
        }
        catch { return false; }
        finally { reloginLock.Release(); }
    }

    bool SameServer(string server)
    {
        try { return ParseServer(server) == BaseUri; } catch { return false; }
    }

    void RaiseExpired()
    {
        if (Interlocked.Exchange(ref expired, 1) == 1) return;
        System.Windows.Application.Current?.Dispatcher.BeginInvoke(() => SessionExpired?.Invoke());
    }

    /// <summary>Wird (einmal, im UI-Thread) ausgelöst, wenn die Sitzung abgelaufen ist und nicht erneuert werden konnte.</summary>
    public event Action? SessionExpired;

    async Task<T> Read<T>(HttpResponseMessage res)
    {
        using (res)
        {
            T? v;
            try { v = await res.Content.ReadFromJsonAsync<T>(Json); }
            catch (JsonException) { throw new ApiException(Unexpected); }
            catch (NotSupportedException) { throw new ApiException(Unexpected); }
            catch (InvalidOperationException) { throw new ApiException(Unexpected); }
            catch (HttpRequestException e) { throw new ApiException($"Verbindung zu {BaseUri.Authority} unterbrochen ({e.Message})"); }
            catch (IOException e) { throw new ApiException($"Verbindung zu {BaseUri.Authority} unterbrochen ({e.Message})"); }
            catch (TaskCanceledException) { throw new ApiException($"Zeitüberschreitung bei {BaseUri.Authority}"); }
            if (v != null) return v;
            // „null“ statt leerer Liste
            var t = typeof(T);
            if (t != typeof(string) && typeof(System.Collections.IEnumerable).IsAssignableFrom(t) && t.GetConstructor(Type.EmptyTypes) != null)
                return (T)Activator.CreateInstance(t)!;
            throw new ApiException(Unexpected);
        }
    }

    public async Task<T> Get<T>(string path) => await Read<T>(await Send(HttpMethod.Get, path, null));

    public async Task<T> Call<T>(HttpMethod method, string path, object? body = null) => await Read<T>(await Send(method, path, body));

    public async Task Call(HttpMethod method, string path, object? body = null) => (await Send(method, path, body)).Dispose();

    public async Task<byte[]> Bytes(string path)
    {
        using var res = await Send(HttpMethod.Get, path, null);
        try { return await res.Content.ReadAsByteArrayAsync(); }
        catch (Exception e) when (e is HttpRequestException or IOException) { throw new ApiException($"Verbindung zu {BaseUri.Authority} unterbrochen ({e.Message})"); }
        catch (TaskCanceledException) { throw new ApiException($"Zeitüberschreitung bei {BaseUri.Authority}"); }
    }

    /// <summary>Lädt eine Datei (CSV, Sicherung, Zertifikat) und liefert den vom Server vorgeschlagenen Dateinamen.</summary>
    public async Task<(byte[] Data, string? FileName)> Download(HttpMethod method, string path, object? body = null)
    {
        using var res = await Send(method, path, body);
        var cd = res.Content.Headers.ContentDisposition;
        var name = (cd?.FileNameStar ?? cd?.FileName)?.Trim('"');
        if (name != null) name = Path.GetFileName(name);
        try { return (await res.Content.ReadAsByteArrayAsync(), string.IsNullOrWhiteSpace(name) ? null : name); }
        catch (Exception e) when (e is HttpRequestException or IOException) { throw new ApiException($"Verbindung zu {BaseUri.Authority} unterbrochen ({e.Message})"); }
        catch (TaskCanceledException) { throw new ApiException($"Zeitüberschreitung bei {BaseUri.Authority}"); }
    }

    /// <summary>
    /// Liest den Live-Strom des Protokolls (Server-Sent Events). Kommt <paramref name="idle"/> lang keine Zeile
    /// (der Server schickt alle 25 s „: ping“), wird mit <see cref="TimeoutException"/> abgebrochen.
    /// Bei abgelaufener Sitzung: einmal still neu anmelden, sonst <see cref="ApiException"/> mit 401.
    /// </summary>
    public async Task Stream(string path, Action<string> onData, Action? onConnected, TimeSpan idle, CancellationToken ct)
    {
        for (int attempt = 0; ; attempt++)
        {
            var seen = session;
            using var watch = CancellationTokenSource.CreateLinkedTokenSource(ct);
            watch.CancelAfter(idle);
            using var client = new HttpClient(CreateHandler()) { BaseAddress = BaseUri, Timeout = Timeout.InfiniteTimeSpan };
            client.DefaultRequestHeaders.Add("X-EasyRadius", "1");
            client.DefaultRequestHeaders.Accept.ParseAdd("text/event-stream");
            HttpResponseMessage res;
            try { res = await client.GetAsync(path, HttpCompletionOption.ResponseHeadersRead, watch.Token); }
            catch (OperationCanceledException) when (!ct.IsCancellationRequested) { throw new TimeoutException("Keine Antwort vom Server"); }
            catch (HttpRequestException e) { throw TranslateConnectError(e); }
            using (res)
            {
                if (res.StatusCode == HttpStatusCode.Unauthorized)
                {
                    if (attempt == 0 && await TryRelogin(seen)) continue;
                    RaiseExpired();
                    throw new ApiException("Die Sitzung ist abgelaufen.", HttpStatusCode.Unauthorized);
                }
                if (!res.IsSuccessStatusCode) throw new ApiException($"Fehler {(int)res.StatusCode}", res.StatusCode);
                onConnected?.Invoke();
                using var reader = new StreamReader(await res.Content.ReadAsStreamAsync(watch.Token));
                while (true)
                {
                    watch.CancelAfter(idle);
                    string? line;
                    try { line = await reader.ReadLineAsync(watch.Token); }
                    catch (OperationCanceledException) when (!ct.IsCancellationRequested) { throw new TimeoutException("Keine Daten vom Server"); }
                    if (line == null) return;
                    if (line.StartsWith("data:", StringComparison.Ordinal))
                    {
                        var v = line[5..];
                        onData(v.StartsWith(' ') ? v[1..] : v);
                    }
                }
            }
        }
    }
}

/// <summary>Gespeicherte Verbindung (Passwort per DPAPI nur für diesen Windows-Benutzer lesbar).</summary>
public class SavedLogin
{
    public string Server { get; set; } = "localhost";
    public string User { get; set; } = "admin";
    public string? Secret { get; set; }
    /// <summary>Vertraute Server-Zertifikate: „host:port“ → SHA-256-Fingerabdruck.</summary>
    public Dictionary<string, string> Pins { get; set; } = new(StringComparer.OrdinalIgnoreCase);

    static readonly object fileLock = new();
    static string FilePath => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "EasyRadius", "verwaltung.json");

    public static SavedLogin Load()
    {
        lock (fileLock)
        {
            SavedLogin s;
            try { s = JsonSerializer.Deserialize<SavedLogin>(File.ReadAllText(FilePath)) ?? new(); }
            catch { s = new(); }
            s.Server ??= "localhost";
            s.User ??= "";
            s.Pins = new Dictionary<string, string>(s.Pins ?? [], StringComparer.OrdinalIgnoreCase);
            return s;
        }
    }

    /// <summary>Speichert; vertraute Zertifikate werden nur über <see cref="SetPin"/> geändert und bleiben erhalten.</summary>
    public void Save()
    {
        lock (fileLock)
        {
            var disk = Load();
            Pins = disk.Pins;
            Write();
        }
    }

    void Write()
    {
        Directory.CreateDirectory(Path.GetDirectoryName(FilePath)!);
        File.WriteAllText(FilePath, JsonSerializer.Serialize(this));
    }

    public static string? GetPin(string hostKey) => Load().Pins.TryGetValue(hostKey, out var fp) ? fp : null;

    public static void SetPin(string hostKey, string fingerprint)
    {
        lock (fileLock)
        {
            var s = Load();
            s.Pins[hostKey] = fingerprint;
            s.Write();
        }
    }

    [JsonIgnore]
    public string? Password
    {
        get
        {
            if (string.IsNullOrEmpty(Secret)) return null;
            try { return Encoding.UTF8.GetString(ProtectedData.Unprotect(Convert.FromBase64String(Secret), null, DataProtectionScope.CurrentUser)); }
            catch { return null; }
        }
        set => Secret = string.IsNullOrEmpty(value) ? null
            : Convert.ToBase64String(ProtectedData.Protect(Encoding.UTF8.GetBytes(value), null, DataProtectionScope.CurrentUser));
    }
}
