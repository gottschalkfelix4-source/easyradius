// pack erzeugt ein tar.gz-Archiv mit festen Rechten und Besitzer root –
// unabhängig davon, auf welchem System gebaut wird.
//
//	go run ./tools/pack -o paket.tar.gz easyradius:0755 radtest:0755 LICENSE:0644
//
// Ein Eintrag ist „Quelle:Rechte“ oder „Quelle=NameImArchiv:Rechte“.
package main

import (
	"archive/tar"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	out := flag.String("o", "", "Zieldatei (.tar.gz)")
	flag.Parse()
	if *out == "" || flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := pack(*out, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "pack:", err)
		os.Exit(1)
	}
}

func pack(out string, entries []string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	tw := tar.NewWriter(gz)
	now := time.Now()
	for _, e := range entries {
		i := strings.LastIndex(e, ":")
		if i < 0 {
			return fmt.Errorf("Rechte fehlen bei %q (Beispiel: datei:0644)", e)
		}
		src, modeStr := e[:i], e[i+1:]
		name := filepath.Base(src)
		if j := strings.Index(src, "="); j >= 0 {
			src, name = src[:j], src[j+1:]
		}
		mode, err := strconv.ParseInt(modeStr, 8, 32)
		if err != nil {
			return fmt.Errorf("Rechte ungültig bei %q", e)
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		st, _ := in.Stat()
		hdr := &tar.Header{Name: name, Mode: mode, Size: st.Size(), ModTime: now, Uname: "root", Gname: "root", Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			in.Close()
			return err
		}
		if _, err := io.Copy(tw, in); err != nil {
			in.Close()
			return err
		}
		in.Close()
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
