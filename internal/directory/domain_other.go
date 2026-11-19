//go:build !windows

package directory

// Domänenmodus (Prüfung über LSA/Netlogon) gibt es nur unter Windows.
// Unter Linux steht für Active Directory der LDAP-Modus zur Verfügung.

type Domain struct{ NetBIOS string }

var errNoDomain = &Error{Msg: "Der Domänenmodus ist nur verfügbar, wenn EasyRadius unter Windows läuft – unter Linux bitte LDAP verwenden"}

// JoinedDomain: Unter Linux gibt es keinen Windows-Domänenbeitritt.
func JoinedDomain() (string, bool) { return "", false }

func NewDomain() (*Domain, error) { return nil, errNoDomain }

func (d *Domain) Describe() string                      { return "Domäne" }
func (d *Domain) AuthPAP(string, string) (*User, error) { return nil, errNoDomain }
func (d *Domain) AuthMSCHAPv2(string, []byte, []byte) (*User, []byte, error) {
	return nil, nil, errNoDomain
}
