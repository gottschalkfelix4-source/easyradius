using System.Collections.ObjectModel;
using System.Windows;
using System.Windows.Automation;
using System.Windows.Controls;
using System.Windows.Data;
using System.Windows.Media;
using Wpf.Ui.Controls;
using Button = Wpf.Ui.Controls.Button;
using ListView = Wpf.Ui.Controls.ListView;
using GridView = Wpf.Ui.Controls.GridView;
using GridViewColumn = Wpf.Ui.Controls.GridViewColumn;
using TextBlock = System.Windows.Controls.TextBlock;
using TextBox = Wpf.Ui.Controls.TextBox;

namespace EasyRadius.Manager;

/// <summary>
/// Check- und Reply-Attribute nach dem Muster von TekRADIUS:
/// Liste „Attribut | Typ | Wert“ und darunter eine Eingabezeile mit Hinzufügen/Ändern und Löschen.
/// </summary>
public class AttributeEditor : UserControl
{
    // Je Serververbindung zwischengespeichert: nach einem Serverwechsel (neuer ApiClient) wird neu geladen.
    static readonly System.Runtime.CompilerServices.ConditionalWeakTable<ApiClient, AttrDictionary> dicts = new();

    public static async Task<AttrDictionary> Dictionary()
    {
        var api = App.Api;
        if (dicts.TryGetValue(api, out var cached)) return cached;
        var d = await api.Get<AttrDictionary>("/api/attributes");
        dicts.AddOrUpdate(api, d);
        return d;
    }

    readonly ObservableCollection<Attr> items;
    readonly AttrDictionary d;
    readonly ListView list = new() { Height = 190, SelectionMode = SelectionMode.Single };
    readonly ComboBox kind = new() { Width = 96, ItemsSource = new[] { "Check", "Reply" }, SelectedIndex = 1 };
    readonly ComboBox attr = new() { Width = 230 };
    readonly ComboBox value = new() { IsEditable = true, MinWidth = 160 };
    readonly TextBox vendor = new() { Width = 90, PlaceholderText = "Herst.-ID" };
    readonly TextBox code = new() { Width = 60, PlaceholderText = "Nr." };
    readonly ComboBox type = new() { Width = 96, ItemsSource = new[] { "string", "integer", "ipaddr", "octets" }, SelectedIndex = 0 };
    readonly TextBox tag = new() { Width = 54, PlaceholderText = "Tag", Text = "0" };
    readonly Button add = new() { Content = "Hinzufügen", Margin = new Thickness(6, 0, 0, 0) };
    readonly Button del = new() { Content = "Löschen", Margin = new Thickness(6, 0, 0, 0), IsEnabled = false };
    readonly TextBlock help = new() { Style = UI.Style("Muted"), Margin = new Thickness(0, 8, 0, 0) };
    readonly ComboBox presets = new() { Width = 300, Margin = new Thickness(0, 8, 0, 0), HorizontalAlignment = HorizontalAlignment.Left };

    const string CustomVsa = "Eigenes Herstellerattribut (VSA) …";
    const string CustomStd = "Eigenes Standardattribut …";

    public List<Attr> Result => items.ToList();

    public AttributeEditor(AttrDictionary dictionary, IEnumerable<Attr>? initial)
    {
        d = dictionary;
        items = new(initial?.Select(a => a.Copy()) ?? []);

        // Liste
        var view = new GridView();
        view.Columns.Add(Col("Attribut", nameof(Attr.Name), 240, true));
        view.Columns.Add(Col("Typ", nameof(Attr.KindText), 80, true));
        view.Columns.Add(Col("Wert", nameof(Attr.Value), 260, false));
        list.View = view;
        list.ItemsSource = items;
        list.SelectionChanged += (_, _) => Load(list.SelectedItem as Attr);

        // Eingabezeile
        foreach (var (c, n) in new (FrameworkElement, string)[] { (list, "Attribute"), (kind, "Check oder Reply"), (attr, "Attribut"), (vendor, "Hersteller-ID"),
                     (code, "Attributnummer"), (type, "Datentyp"), (tag, "Tag"), (value, "Wert"), (presets, "Vorlage einfügen") })
            AutomationProperties.SetName(c, n);
        var bar = new WrapPanel { Margin = new Thickness(0, 8, 0, 0) };
        foreach (var c in new FrameworkElement[] { kind, attr, vendor, code, type, tag })
        {
            c.Margin = new Thickness(0, 0, 6, 6);
            bar.Children.Add(c);
        }
        value.Margin = new Thickness(0, 0, 0, 6);
        bar.Children.Add(value);
        add.Margin = new Thickness(6, 0, 0, 6);
        del.Margin = new Thickness(6, 0, 0, 6);
        bar.Children.Add(add);
        bar.Children.Add(del);

        presets.ItemsSource = new object[] { "Vorlage einfügen …" }.Concat(d.Presets).ToList();
        presets.SelectedIndex = 0;

        var root = new StackPanel();
        root.Children.Add(new Border
        {
            Child = list, BorderThickness = new Thickness(1), CornerRadius = new CornerRadius(6),
        }.Res(Border.BorderBrushProperty, "ControlStrokeColorDefaultBrush"));
        root.Children.Add(bar);
        root.Children.Add(help);
        root.Children.Add(presets);
        Content = root;

        kind.SelectionChanged += (_, _) => FillAttrs(attr.SelectedItem);
        attr.SelectionChanged += (_, _) => AttrChanged();
        add.Click += (_, _) => Commit();
        del.Click += (_, _) =>
        {
            if (list.SelectedItem is Attr a) items.Remove(a);
            Clear();
        };
        presets.SelectionChanged += (_, _) =>
        {
            if (presets.SelectedItem is not Preset p) return;
            foreach (var a in p.Attrs)
            {
                var x = a.Copy();
                if (string.IsNullOrEmpty(x.Kind)) x.Kind = "reply";
                // Nur dasselbe Attribut (Typ, Hersteller, Nummer und Tag) wird ersetzt, alles andere ergänzt.
                var i = items.ToList().FindIndex(y => y.Kind == x.Kind && y.VendorId == x.VendorId && y.Code == x.Code && y.Tag == x.Tag);
                if (i >= 0) items[i] = x; else items.Add(x);
            }
            presets.SelectedIndex = 0;
            _ = UI.Toast($"Vorlage „{p.Name}“ eingefügt – Werte bei Bedarf anpassen");
        };
        FillAttrs(null);
    }

    static GridViewColumn Col(string header, string path, double width, bool colored)
    {
        var f = new FrameworkElementFactory(typeof(TextBlock));
        f.SetBinding(TextBlock.TextProperty, new Binding(path));
        if (colored)
        {
            var style = new Style(typeof(TextBlock));
            var trig = new DataTrigger { Binding = new Binding(nameof(Attr.IsCheck)), Value = true };
            trig.Setters.Add(new Setter(TextBlock.ForegroundProperty, new DynamicResourceExtension("CheckBrush")));
            style.Triggers.Add(trig);
            style.Setters.Add(new Setter(TextBlock.ForegroundProperty, new DynamicResourceExtension("ReplyBrush")));
            f.SetValue(TextBlock.StyleProperty, style);
        }
        else f.SetValue(TextBlock.FontFamilyProperty, new FontFamily("Cascadia Mono, Consolas"));
        return new GridViewColumn { Header = header, Width = width, CellTemplate = new DataTemplate { VisualTree = f } };
    }

    string Kind => kind.SelectedIndex == 0 ? "check" : "reply";

    void FillAttrs(object? keep)
    {
        var defs = d.Dictionary.Where(x => x.Usable(Kind))
            .OrderBy(x => x.Vendor == "" ? "" : x.Vendor).ThenBy(x => x.Name).Cast<object>().ToList();
        defs.Add(CustomVsa);
        defs.Add(CustomStd);
        attr.ItemsSource = defs;
        attr.SelectedItem = keep is AttrDef k && defs.Contains(k) ? k : defs[0];
        AttrChanged();
    }

    void AttrChanged()
    {
        bool custom = attr.SelectedItem is string;
        vendor.Visibility = Equals(attr.SelectedItem, CustomVsa) ? Visibility.Visible : Visibility.Collapsed;
        code.Visibility = type.Visibility = custom ? Visibility.Visible : Visibility.Collapsed;
        var def = attr.SelectedItem as AttrDef;
        tag.Visibility = def?.Tagged == true ? Visibility.Visible : Visibility.Collapsed;
        value.ItemsSource = def?.Values?.Keys.OrderBy(x => x).ToList();
        var how = Kind == "check"
            ? "Check: muss in der Anfrage des Geräts vorkommen und passen – sonst wird abgelehnt. Erlaubt: * und ? als Platzhalter, Netze wie 10.0.0.0/24, ! am Anfang = darf nicht."
            : "Reply: wird bei erfolgreicher Anmeldung an das Gerät zurückgeschickt.";
        help.Text = (def?.Help is { Length: > 0 } h ? $"{def.Name}: {h}\n" : "") + how;
    }

    void Load(Attr? a)
    {
        if (a == null) { add.Content = "Hinzufügen"; del.IsEnabled = false; return; }
        kind.SelectedIndex = a.IsCheck ? 0 : 1;
        var def = d.Dictionary.FirstOrDefault(x => x.VendorId == a.VendorId && x.Code == a.Code && x.Usable(a.Kind));
        if (def != null) FillAttrs(def);
        else
        {
            FillAttrs(null);
            attr.SelectedItem = a.VendorId != 0 ? CustomVsa : CustomStd;
            vendor.Text = a.VendorId.ToString();
            code.Text = a.Code.ToString();
            type.SelectedItem = a.Type;
        }
        value.Text = a.Value;
        tag.Text = a.Tag.ToString();
        add.Content = "Ändern";
        del.IsEnabled = true;
    }

    void Clear()
    {
        list.SelectedItem = null;
        value.Text = "";
        add.Content = "Hinzufügen";
        del.IsEnabled = false;
    }

    /// <summary>Übernimmt die Eingabezeile (Hinzufügen bzw. Ändern).</summary>
    public bool Commit(bool silent = false)
    {
        var val = value.Text.Trim();
        if (val == "") { if (!silent) _ = UI.Toast("Bitte einen Wert eintragen", true); return false; }
        Attr a;
        if (attr.SelectedItem is AttrDef def)
            a = new Attr { Name = def.Name, VendorId = def.VendorId, Code = def.Code, Type = def.Type, Tagged = def.Tagged };
        else
        {
            uint.TryParse(vendor.Text, out var vid);
            byte.TryParse(code.Text, out var c);
            bool vsa = Equals(attr.SelectedItem, CustomVsa);
            if (c == 0 || (vsa && vid == 0)) { if (!silent) _ = UI.Toast("Bitte Hersteller-ID und Nummer angeben", true); return false; }
            if (!vsa) vid = 0;
            a = new Attr { Name = vsa ? $"VSA {vid}/{c}" : $"Attribut {c}", VendorId = vid, Code = c, Type = (string)type.SelectedItem };
        }
        a.Kind = Kind;
        a.Value = val;
        if (a.Tagged) { byte.TryParse(tag.Text, out var t); a.Tag = t; }

        // „Ändern“ ersetzt die gewählte Zeile; „Hinzufügen“ hängt an – auch mehrfach dasselbe Attribut
        // (z. B. mehrere Cisco-AVPair). Nur ein völlig gleicher Eintrag wird nicht doppelt angelegt.
        if (list.SelectedItem is Attr sel && items.IndexOf(sel) is var idx and >= 0) items[idx] = a;
        else if (items.Any(x => x.Kind == a.Kind && x.VendorId == a.VendorId && x.Code == a.Code && x.Tag == a.Tag && x.Value == a.Value))
        {
            if (!silent) _ = UI.Toast("Dieses Attribut mit diesem Wert ist bereits vorhanden", true);
        }
        else items.Add(a);
        Clear();
        return true;
    }

    /// <summary>Liefert alle Attribute; ein noch nicht übernommener Wert wird mitgenommen.</summary>
    public List<Attr> Collect()
    {
        if (value.Text.Trim() != "") Commit(true);
        return Result;
    }
}
