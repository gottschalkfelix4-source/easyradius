using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Http.Json;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace EasyRadius.Manager;

/// <summary>Fehler mit verständlicher Meldung vom Server.</summary>
public class ApiException(string message, HttpStatusCode status = 0) : Exception(message)
{
    public HttpStatusCode Status { get; } = status;
}

/// <summary>Spricht mit der JSON-API des EasyRadius-Dienstes (lokal oder auf einem anderen Rechner).</summary>
public class ApiClient
{
    public static readonly JsonSerializerOptions Json = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.Never,
    };

    readonly HttpClient http;
    readonly CookieContainer Cookies = new();
    public Uri BaseUri { get; }

    public ApiClient(string server)
    {
        if (!server.Contains("://")) server = "http://" + server;
        var u = new UriBuilder(server.TrimEnd('/'));
        if (u.Port <= 0 || (u.Port == 80 && !server.Contains(":80"))) u.Port = 8088;
        BaseUri = u.Uri;
        var handler = new HttpClientHandler { CookieContainer = Cookies, UseCookies = true };
        http = new HttpClient(handler) { BaseAddress = BaseUri, Timeout = TimeSpan.FromSeconds(30) };
        http.DefaultRequestHeaders.Add("X-EasyRadius", "1");
    }

    public string Url(string path) => new Uri(BaseUri, path).ToString();

    async Task<HttpResponseMessage> Send(HttpMethod method, string path, object? body)
    {
        var req = new HttpRequestMessage(method, path);
        if (body is HttpContent c) req.Content = c;
        else if (body != null) req.Content = JsonContent.Create(body, options: Json);
        HttpResponseMessage res;
        try { res = await http.SendAsync(req); }
        catch (HttpRequestException e) { throw new ApiException($"Server {BaseUri.Authority} nicht erreichbar ({e.Message})"); }
        catch (TaskCanceledException) { throw new ApiException($"Zeitüberschreitung bei {BaseUri.Authority}"); }
        if (!res.IsSuccessStatusCode)
        {
            string msg = $"Fehler {(int)res.StatusCode}";
            try
            {
                var doc = await res.Content.ReadFromJsonAsync<JsonElement>(Json);
                if (doc.TryGetProperty("error", out var e)) msg = e.GetString() ?? msg;
            }
            catch { }
            if (res.StatusCode == HttpStatusCode.Unauthorized) SessionExpired?.Invoke();
            throw new ApiException(msg, res.StatusCode);
        }
        return res;
    }

    public event Action? SessionExpired;

    public async Task<T> Get<T>(string path) =>
        (await (await Send(HttpMethod.Get, path, null)).Content.ReadFromJsonAsync<T>(Json))!;

    public async Task<T> Call<T>(HttpMethod method, string path, object? body = null) =>
        (await (await Send(method, path, body)).Content.ReadFromJsonAsync<T>(Json))!;

    public async Task Call(HttpMethod method, string path, object? body = null) => await Send(method, path, body);

    public async Task<byte[]> Bytes(string path) => await (await Send(HttpMethod.Get, path, null)).Content.ReadAsByteArrayAsync();

    /// <summary>Liest den Live-Strom des Protokolls (Server-Sent Events).</summary>
    public async Task Stream(string path, Action<string> onData, CancellationToken ct)
    {
        using var client = new HttpClient(new HttpClientHandler { CookieContainer = Cookies, UseCookies = true })
        { BaseAddress = BaseUri, Timeout = Timeout.InfiniteTimeSpan };
        using var res = await client.GetAsync(path, HttpCompletionOption.ResponseHeadersRead, ct);
        res.EnsureSuccessStatusCode();
        using var reader = new StreamReader(await res.Content.ReadAsStreamAsync(ct));
        while (!ct.IsCancellationRequested)
        {
            var line = await reader.ReadLineAsync(ct);
            if (line == null) break;
            if (line.StartsWith("data: ")) onData(line[6..]);
        }
    }

}

/// <summary>Gespeicherte Verbindung (Passwort per DPAPI nur für diesen Windows-Benutzer lesbar).</summary>
public class SavedLogin
{
    public string Server { get; set; } = "localhost:8088";
    public string User { get; set; } = "admin";
    public string? Secret { get; set; }

    static string FilePath => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "EasyRadius", "verwaltung.json");

    public static SavedLogin Load()
    {
        try { return JsonSerializer.Deserialize<SavedLogin>(File.ReadAllText(FilePath)) ?? new(); }
        catch { return new(); }
    }

    public void Save()
    {
        Directory.CreateDirectory(Path.GetDirectoryName(FilePath)!);
        File.WriteAllText(FilePath, JsonSerializer.Serialize(this));
    }

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
