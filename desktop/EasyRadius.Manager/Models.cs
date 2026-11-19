using System.Text.Json.Serialization;
using System.Windows.Media;

namespace EasyRadius.Manager;

public class StateInfo { public bool Setup { get; set; } public string User { get; set; } = ""; public string Version { get; set; } = ""; public string Server { get; set; } = ""; public string Platform { get; set; } = "windows"; }

public class Attr
{
    public string Kind { get; set; } = "reply";
    public string Name { get; set; } = "";
    public uint VendorId { get; set; }
    public byte Code { get; set; }
    public string Type { get; set; } = "string";
    public bool Tagged { get; set; }
    public byte Tag { get; set; }
    public string Value { get; set; } = "";

    [JsonIgnore] public bool IsCheck => Kind == "check";
    [JsonIgnore] public string KindText => IsCheck ? "Check" : "Reply";
    [JsonIgnore] public string Where => VendorId != 0 ? $"{VendorId}/{Code}" : "";
    public Attr Copy() => (Attr)MemberwiseClone();
}

public class AttrDef
{
    public string Name { get; set; } = "";
    public string Vendor { get; set; } = "";
    public uint VendorId { get; set; }
    public byte Code { get; set; }
    public string Type { get; set; } = "";
    public bool Tagged { get; set; }
    public Dictionary<string, int>? Values { get; set; }
    public string? Help { get; set; }
    public string Use { get; set; } = "reply";
    public bool Usable(string kind) => Use == "both" || Use == kind;
    public override string ToString() => Name;
}

public class Preset { public string Name { get; set; } = ""; public string Help { get; set; } = ""; public List<Attr> Attrs { get; set; } = []; public override string ToString() => Name; }
public class AttrDictionary { public List<AttrDef> Dictionary { get; set; } = []; public List<Preset> Presets { get; set; } = []; public List<string> Vendors { get; set; } = []; }

public class User
{
    public string Id { get; set; } = "";
    public string Username { get; set; } = "";
    public string FullName { get; set; } = "";
    public bool Enabled { get; set; } = true;
    public string Vlan { get; set; } = "";
    public string GroupId { get; set; } = "";
    public List<Attr>? Attributes { get; set; } = [];
    public string Note { get; set; } = "";
    public string ExpiresAt { get; set; } = "";
    public DateTime LastLoginAt { get; set; }
    public bool Expired { get; set; }

    // Anzeige
    [JsonIgnore] public string GroupName { get; set; } = "";
    [JsonIgnore] public string StatusText => !Enabled ? "Deaktiviert" : Expired ? "Abgelaufen" : "Aktiv";
    [JsonIgnore] public Brush StatusColor => Table.StatusBrush(Enabled && !Expired, !Enabled);
    [JsonIgnore] public string VlanText { get; set; } = "";
    [JsonIgnore] public string LastLoginText => Fmt.Rel(LastLoginAt);
    [JsonIgnore] public string ExpiresText => string.IsNullOrEmpty(ExpiresAt) ? "nie" : Fmt.Date(ExpiresAt);
    [JsonIgnore] public string SubText => string.Join(" · ", new[] { FullName, Note }.Where(s => !string.IsNullOrEmpty(s)));
    [JsonIgnore] public string AttrText => Attributes is { Count: > 0 } a ? $"+{a.Count} Attribut{(a.Count > 1 ? "e" : "")}" : "";
}

public class Group
{
    public string Id { get; set; } = "";
    public string Name { get; set; } = "";
    public string Description { get; set; } = "";
    public bool Enabled { get; set; } = true;
    public string Vlan { get; set; } = "";
    public string ExpiresAt { get; set; } = "";
    public List<string> AllowedClients { get; set; } = [];
    public List<string> AllowedSsids { get; set; } = [];
    public List<string> AdGroups { get; set; } = [];
    public List<Attr>? Attributes { get; set; } = [];
    public int Members { get; set; }
    public bool Expired { get; set; }

    [JsonIgnore] public string StatusText => !Enabled ? "Gesperrt" : Expired ? "Abgelaufen" : "Aktiv";
    [JsonIgnore] public Brush StatusColor => Table.StatusBrush(Enabled && !Expired);
    [JsonIgnore] public string MembersText => Members.ToString();
    [JsonIgnore] public string VlanText => string.IsNullOrEmpty(Vlan) ? "Standard" : "VLAN " + Vlan;
    [JsonIgnore] public string LimitsText { get; set; } = "";
    public override string ToString() => string.IsNullOrEmpty(Vlan) ? Name : $"{Name} (VLAN {Vlan})";
}

public class Client
{
    public string Id { get; set; } = "";
    public string Name { get; set; } = "";
    public string Address { get; set; } = "";
    public string Secret { get; set; } = "";
    public string Note { get; set; } = "";
    public DateTime LastSeen { get; set; }
    [JsonIgnore] public string LastSeenText => LastSeen.Year < 2000 ? "noch nie" : Fmt.Rel(LastSeen);
}

public class Event
{
    public DateTime Time { get; set; }
    public string Kind { get; set; } = "";
    public string? User { get; set; }
    public string? Client { get; set; }
    public string? ClientIp { get; set; }
    public string? Station { get; set; }
    public string? Method { get; set; }
    public string Message { get; set; } = "";

    [JsonIgnore] public string Title => string.IsNullOrEmpty(User) ? Message : $"{User} · {Message}";
    [JsonIgnore] public string Meta => string.Join(" · ", new[] { Client is { Length: > 0 } c ? "über " + c : null, Station is { Length: > 0 } s ? "Gerät " + s : null, Method }.Where(x => !string.IsNullOrEmpty(x)));
    [JsonIgnore] public string TimeText => Time.ToLocalTime().ToString("dd.MM.yyyy HH:mm:ss");
    [JsonIgnore] public string RelText => Fmt.Rel(Time);
    [JsonIgnore] public string Glyph => Kind switch { "accept" => "", "reject" => "", "warning" => "", "acct" => "", _ => "" };
}

public class Hour { public DateTime Start { get; set; } public int Accepted { get; set; } public int Rejected { get; set; } }
public class Stats { public int Accepted24h { get; set; } public int Rejected24h { get; set; } public List<Hour> Hours { get; set; } = []; }
public class ServerStatus { public bool Running { get; set; } public DateTime StartedAt { get; set; } public string Error { get; set; } = ""; public int AuthPort { get; set; } public int AcctPort { get; set; } }
public class Online { public string User { get; set; } = ""; public string Client { get; set; } = ""; public string Station { get; set; } = ""; public string Ip { get; set; } = ""; public DateTime Started { get; set; } public string Meta => string.Join(" · ", new[] { Station, Ip, Client }.Where(x => !string.IsNullOrEmpty(x))); public string StartedText => Fmt.Rel(Started); }
public class Unknown { public string Ip { get; set; } = ""; public DateTime LastSeen { get; set; } }

public class CertInfo
{
    public string Subject { get; set; } = "";
    public string Issuer { get; set; } = "";
    public DateTime NotAfter { get; set; }
    public string Fingerprint { get; set; } = "";
    public bool SelfSigned { get; set; }
    public bool HasCa { get; set; }
    public bool Public { get; set; }
}

public class Dashboard
{
    public ServerStatus Status { get; set; } = new();
    public List<string> Ips { get; set; } = [];
    public int Users { get; set; }
    public int ActiveUsers { get; set; }
    public int Clients { get; set; }
    public int Groups { get; set; }
    public Stats Stats { get; set; } = new();
    public List<Online> Online { get; set; } = [];
    public List<Unknown> Unknown { get; set; } = [];
    public List<Event> Recent { get; set; } = [];
    public CertInfo Cert { get; set; } = new();
}

public class AcmeInfo
{
    public bool Enabled { get; set; }
    public string Domain { get; set; } = "";
    public string Email { get; set; } = "";
    public bool Staging { get; set; }
    public string Method { get; set; } = "dns";
    public string Provider { get; set; } = "";
    public List<string> SavedCredentials { get; set; } = [];
    public string AcmeDnsUrl { get; set; } = "";
    public string DefaultAcmeDnsUrl { get; set; } = "";
    public string CnameName { get; set; } = "";
    public string CnameTarget { get; set; } = "";
    public DateTime LastSuccess { get; set; }
    public string LastError { get; set; } = "";
    public bool Running { get; set; }
}

public class ProviderField { public string Env { get; set; } = ""; public string Help { get; set; } = ""; }
public class DnsProvider { public string Code { get; set; } = ""; public string Name { get; set; } = ""; public string Url { get; set; } = ""; public List<ProviderField> Fields { get; set; } = []; public override string ToString() => Name; }

public class AdInfo
{
    public string Mode { get; set; } = "off";
    public bool RequireGroup { get; set; }
    public string LdapUrl { get; set; } = "";
    public bool StartTls { get; set; }
    public bool SkipVerify { get; set; }
    public string BaseDn { get; set; } = "";
    public string BindUser { get; set; } = "";
    public bool HasBindPassword { get; set; }
    public string UserAttribute { get; set; } = "";
    public bool Joined { get; set; }
    public string Domain { get; set; } = "";
    public string Status { get; set; } = "";
    public string Platform { get; set; } = "windows";
}

public class SettingsInfo { public int AuthPort { get; set; } public int AcctPort { get; set; } public string WebAddr { get; set; } = ""; public string ServerName { get; set; } = ""; public string AdminUser { get; set; } = ""; }

public class TestResult
{
    public bool Ok { get; set; }
    public string Message { get; set; } = "";
    public string Source { get; set; } = "";
    public List<string>? Groups { get; set; }
    public string Group { get; set; } = "";
    public string Vlan { get; set; } = "";
    public bool WlanOk { get; set; }
    public string WlanNote { get; set; } = "";
}

public static class Fmt
{
    public static string Rel(DateTime t)
    {
        if (t.Year < 2000) return "nie";
        var d = DateTime.UtcNow - t.ToUniversalTime();
        if (d.TotalSeconds < 60) return "gerade eben";
        if (d.TotalMinutes < 60) return $"vor {(int)d.TotalMinutes} Min.";
        if (d.TotalHours < 24) return $"vor {(int)d.TotalHours} Std.";
        if (d.TotalDays < 7) return $"vor {(int)d.TotalDays} Tag{((int)d.TotalDays > 1 ? "en" : "")}";
        return t.ToLocalTime().ToString("dd.MM.yyyy");
    }

    public static string Date(string ymd) => DateTime.TryParse(ymd, out var d) ? d.ToString("dd.MM.yyyy") : ymd;
}
