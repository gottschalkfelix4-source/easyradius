package directory

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Domain prüft Anmeldungen über den Domänenbeitritt des Rechners (LSA/Netlogon),
// genau wie Microsoft NPS. Benötigt Ausführung als Dienst (LocalSystem).
type Domain struct {
	NetBIOS string // Name der Domäne, der der Rechner angehört

	once    sync.Once
	initErr error
	lsa     windows.Handle
	pkg     uint32
	mu      sync.Mutex
}

var (
	secur32                     = windows.NewLazySystemDLL("secur32.dll")
	procLsaRegisterLogonProc    = secur32.NewProc("LsaRegisterLogonProcess")
	procLsaLookupAuthPackage    = secur32.NewProc("LsaLookupAuthenticationPackage")
	procLsaLogonUser            = secur32.NewProc("LsaLogonUser")
	procLsaFreeReturnBuffer     = secur32.NewProc("LsaFreeReturnBuffer")
	advapi32                    = windows.NewLazySystemDLL("advapi32.dll")
	procLogonUserW              = advapi32.NewProc("LogonUserW")
	procAllocateLocallyUniqueId = advapi32.NewProc("AllocateLocallyUniqueId")
)

// JoinedDomain liefert den Domänennamen, falls der Rechner Mitglied einer Domäne ist.
func JoinedDomain() (string, bool) {
	var name *uint16
	var status uint32
	if err := windows.NetGetJoinInformation(nil, &name, &status); err != nil {
		return "", false
	}
	defer windows.NetApiBufferFree((*byte)(unsafe.Pointer(name)))
	if status != windows.NetSetupDomainName {
		return "", false
	}
	return windows.UTF16PtrToString(name), true
}

func NewDomain() (*Domain, error) {
	d, ok := JoinedDomain()
	if !ok {
		return nil, &Error{Msg: "Dieser Rechner ist kein Mitglied einer Domäne"}
	}
	return &Domain{NetBIOS: d}, nil
}

func (d *Domain) Describe() string { return "Domäne " + d.NetBIOS }

type lsaString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *byte
}

func newLsaString(s string) *lsaString {
	b := append([]byte(s), 0)
	return &lsaString{Length: uint16(len(s)), MaximumLength: uint16(len(b)), Buffer: &b[0]}
}

func (d *Domain) init() error {
	d.once.Do(func() {
		var mode uint32
		st, _, _ := procLsaRegisterLogonProc.Call(uintptr(unsafe.Pointer(newLsaString("EasyRadius"))),
			uintptr(unsafe.Pointer(&d.lsa)), uintptr(unsafe.Pointer(&mode)))
		if st != 0 {
			d.initErr = &Error{Msg: fmt.Sprintf("Anmeldung am Domänencontroller nicht möglich (LSA 0x%08X) – EasyRadius muss als Dienst laufen", uint32(st))}
			return
		}
		st, _, _ = procLsaLookupAuthPackage.Call(uintptr(d.lsa),
			uintptr(unsafe.Pointer(newLsaString("MICROSOFT_AUTHENTICATION_PACKAGE_V1_0"))), uintptr(unsafe.Pointer(&d.pkg)))
		if st != 0 {
			d.initErr = &Error{Msg: fmt.Sprintf("Authentifizierungspaket nicht gefunden (0x%08X)", uint32(st))}
		}
	})
	return d.initErr
}

// ntStatusError übersetzt typische Anmeldefehler.
func ntStatusError(status, sub uint32) error {
	code := status
	if status == 0xC000006E && sub != 0 { // ACCOUNT_RESTRICTION → Unterstatus ist aussagekräftiger
		code = sub
	}
	switch code {
	case 0xC000006D, 0xC000006A, 0xC0000064:
		return ErrBadCredentials
	case 0xC0000072:
		return &Error{Msg: "AD-Konto ist deaktiviert"}
	case 0xC0000234:
		return &Error{Msg: "AD-Konto ist gesperrt (zu viele Fehlversuche)"}
	case 0xC0000071:
		return &Error{Msg: "AD-Passwort ist abgelaufen"}
	case 0xC0000224:
		return &Error{Msg: "AD-Passwort muss geändert werden"}
	case 0xC0000193:
		return &Error{Msg: "AD-Konto ist abgelaufen"}
	case 0xC000006F:
		return &Error{Msg: "Anmeldung zu dieser Uhrzeit nicht erlaubt (AD-Anmeldezeiten)"}
	case 0xC0000070:
		return &Error{Msg: "Anmeldung von dieser Arbeitsstation nicht erlaubt (AD)"}
	case 0xC000005E:
		return &Error{Msg: "Kein Domänencontroller erreichbar"}
	case 0xC0000413:
		return &Error{Msg: "Anmeldung durch AD-Richtlinie (Authentication Policy) verweigert"}
	}
	return &Error{Msg: fmt.Sprintf("AD-Anmeldung fehlgeschlagen (Status 0x%08X)", code)}
}

func (d *Domain) domainFor(username string) (user, domain string) {
	u, dom, upn := SplitName(username)
	if upn {
		return u + "@" + dom, "" // UPN: Domäne bleibt leer
	}
	if dom == "" {
		dom = d.NetBIOS
	}
	return u, dom
}

// AuthPAP: Netzwerk-Anmeldung mit Klartextpasswort.
func (d *Domain) AuthPAP(username, password string) (*User, error) {
	user, dom := d.domainFor(username)
	var domPtr *uint16
	if dom != "" {
		domPtr, _ = windows.UTF16PtrFromString(dom)
	}
	u16, _ := windows.UTF16PtrFromString(user)
	p16, _ := windows.UTF16PtrFromString(password)
	var tok windows.Token
	r, _, e := procLogonUserW.Call(uintptr(unsafe.Pointer(u16)), uintptr(unsafe.Pointer(domPtr)), uintptr(unsafe.Pointer(p16)),
		3 /* LOGON32_LOGON_NETWORK */, 0, uintptr(unsafe.Pointer(&tok)))
	if r == 0 {
		if errno, ok := e.(windows.Errno); ok {
			switch errno {
			case windows.ERROR_LOGON_FAILURE:
				return nil, ErrBadCredentials
			case 1331: // ERROR_ACCOUNT_DISABLED
				return nil, ntStatusError(0xC0000072, 0)
			case 1909: // ERROR_ACCOUNT_LOCKED_OUT
				return nil, ntStatusError(0xC0000234, 0)
			case 1330: // ERROR_PASSWORD_EXPIRED
				return nil, ntStatusError(0xC0000071, 0)
			case 1907: // ERROR_PASSWORD_MUST_CHANGE
				return nil, ntStatusError(0xC0000224, 0)
			case 1793: // ERROR_ACCOUNT_EXPIRED
				return nil, ntStatusError(0xC0000193, 0)
			case 1311: // ERROR_NO_LOGON_SERVERS
				return nil, ntStatusError(0xC000005E, 0)
			}
		}
		return nil, &Error{Msg: "AD-Anmeldung fehlgeschlagen: " + e.Error()}
	}
	defer tok.Close()
	return userFromToken(tok, username)
}

// AuthMSCHAPv2: Challenge/Response-Prüfung beim Domänencontroller (MSV1_0, MSCHAPv2 erlaubt).
func (d *Domain) AuthMSCHAPv2(username string, challenge8, ntResponse []byte) (*User, []byte, error) {
	if err := d.init(); err != nil {
		return nil, nil, err
	}
	user, dom := d.domainFor(username)
	if len(challenge8) != 8 || len(ntResponse) != 24 {
		return nil, nil, &Error{Msg: "Ungültige MS-CHAPv2-Daten"}
	}

	// MSV1_0_LM20_LOGON (x64) mit angehängten Zeichenketten in einem Puffer.
	const hdr = 104
	userU := utf16Bytes(user)
	domU := utf16Bytes(dom)
	wsU := utf16Bytes("EASYRADIUS")
	size := hdr + len(userU) + len(domU) + len(wsU) + len(ntResponse)
	mem, err := windows.LocalAlloc(windows.LPTR, uint32(size))
	if err != nil {
		return nil, nil, err
	}
	defer windows.LocalFree(windows.Handle(mem))
	buf := unsafe.Slice((*byte)(unsafe.Pointer(mem)), size)
	le := binary.LittleEndian
	le.PutUint32(buf[0:], 4) // MsV1_0NetworkLogon
	off := hdr
	putStr := func(at int, data []byte) {
		copy(buf[off:], data)
		le.PutUint16(buf[at:], uint16(len(data)))
		le.PutUint16(buf[at+2:], uint16(len(data)))
		le.PutUint64(buf[at+8:], uint64(mem)+uint64(off))
		off += len(data)
	}
	putStr(8, domU)   // LogonDomainName
	putStr(24, userU) // UserName
	putStr(40, wsU)   // Workstation
	copy(buf[56:64], challenge8)
	putStr(64, ntResponse) // CaseSensitiveChallengeResponse (NT-Response)
	// CaseInsensitiveChallengeResponse (80) bleibt leer
	le.PutUint32(buf[96:], 0x00010000|0x00000020|0x00000800) // ALLOW_MSVCHAPV2 | Trust-Accounts

	var src struct {
		Name [8]byte
		ID   windows.LUID
	}
	copy(src.Name[:], "EasyRad")
	procAllocateLocallyUniqueId.Call(uintptr(unsafe.Pointer(&src.ID)))

	var (
		profile    uintptr
		profileLen uint32
		logonID    windows.LUID
		tok        windows.Token
		quotas     [6]uint64
		sub        uint32
	)
	d.mu.Lock()
	st, _, _ := procLsaLogonUser.Call(uintptr(d.lsa), uintptr(unsafe.Pointer(newLsaString("EasyRadius"))),
		3 /* Network */, uintptr(d.pkg), mem, uintptr(size), 0, uintptr(unsafe.Pointer(&src)),
		uintptr(unsafe.Pointer(&profile)), uintptr(unsafe.Pointer(&profileLen)), uintptr(unsafe.Pointer(&logonID)),
		uintptr(unsafe.Pointer(&tok)), uintptr(unsafe.Pointer(&quotas)), uintptr(unsafe.Pointer(&sub)))
	d.mu.Unlock()
	if st != 0 {
		return nil, nil, ntStatusError(uint32(st), sub)
	}
	defer tok.Close()
	if profile != 0 {
		defer procLsaFreeReturnBuffer.Call(profile)
	}
	// MSV1_0_LM20_LOGON_PROFILE: UserSessionKey liegt bei Offset 28 (= MD4 des NT-Hashes).
	if profile == 0 || profileLen < 44 {
		return nil, nil, &Error{Msg: "Domänencontroller lieferte keinen Sitzungsschlüssel"}
	}
	key := append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(profile+28)), 16)...)
	if allZero(key) {
		return nil, nil, &Error{Msg: "Domänencontroller lieferte keinen Sitzungsschlüssel"}
	}
	u, err := userFromToken(tok, username)
	return u, key, err
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

func utf16Bytes(s string) []byte {
	u, _ := windows.UTF16FromString(s)
	u = u[:len(u)-1]
	b := make([]byte, len(u)*2)
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

// userFromToken liest Benutzer und alle Gruppen (inkl. verschachtelter) aus dem Anmeldetoken.
func userFromToken(tok windows.Token, username string) (*User, error) {
	name, _, _ := SplitName(username)
	u := &User{Name: name}
	if tu, err := tok.GetTokenUser(); err == nil {
		if acc, dom, _, err := tu.User.Sid.LookupAccount(""); err == nil {
			u.Name, u.Domain = acc, dom
		}
	}
	groups, err := tok.GetTokenGroups()
	if err != nil {
		return u, nil
	}
	for _, g := range groups.AllGroups() {
		if g.Attributes&windows.SE_GROUP_LOGON_ID != 0 {
			continue
		}
		acc, dom, typ, err := g.Sid.LookupAccount("")
		if err != nil || acc == "" {
			continue
		}
		if typ != windows.SidTypeGroup && typ != windows.SidTypeAlias && typ != windows.SidTypeWellKnownGroup {
			continue
		}
		if strings.EqualFold(dom, "NT AUTHORITY") || strings.EqualFold(dom, "BUILTIN") || dom == "" {
			continue
		}
		u.Groups = append(u.Groups, dom+`\`+acc)
	}
	return u, nil
}
