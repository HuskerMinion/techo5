package wifi

import (
	"errors"
	"strings"
	"testing"
)

// The codes phones and routers show are read into the network they name, whatever the order of the
// fields, the case of their names, and whatever the name and the passphrase hold.
func TestAWifiQRCodeIsRead(t *testing.T) {
	for _, c := range []struct {
		name, code string
		want       QR
	}{
		{"plain", "WIFI:T:WPA;S:Home;P:hunter2hunter;;", QR{SSID: "Home", Passphrase: "hunter2hunter"}},
		{"another order", "WIFI:S:Home;P:hunter2hunter;T:WPA;H:false;;", QR{SSID: "Home", Passphrase: "hunter2hunter"}},
		{"one semicolon at the end", "WIFI:T:WPA;S:Home;P:hunter2hunter;", QR{SSID: "Home", Passphrase: "hunter2hunter"}},
		{"lower case", "wifi:t:wpa;s:Home;p:hunter2hunter;;", QR{SSID: "Home", Passphrase: "hunter2hunter"}},
		{"WPA2", "WIFI:T:WPA2;S:Home;P:hunter2hunter;;", QR{SSID: "Home", Passphrase: "hunter2hunter"}},
		{"hidden", "WIFI:T:WPA;S:Attic;P:hunter2hunter;H:true;;", QR{SSID: "Attic", Passphrase: "hunter2hunter", Hidden: true}},
		{"hidden in capitals", "WIFI:T:WPA;S:Attic;P:hunter2hunter;H:TRUE;;", QR{SSID: "Attic", Passphrase: "hunter2hunter", Hidden: true}},
		{"open", "WIFI:T:nopass;S:Cafe;;", QR{SSID: "Cafe"}},
		{"open with an empty passphrase", "WIFI:T:nopass;S:Cafe;P:;;", QR{SSID: "Cafe"}},
		{"open ignores a passphrase", "WIFI:T:nopass;S:Cafe;P:whatever1;;", QR{SSID: "Cafe"}},
		{"open without a type", "WIFI:S:Cafe;;", QR{SSID: "Cafe"}},
		{"no type but a passphrase", "WIFI:S:Home;P:hunter2hunter;;", QR{SSID: "Home", Passphrase: "hunter2hunter"}},
		{"hidden and open", "WIFI:T:nopass;S:Shed;H:true;;", QR{SSID: "Shed", Hidden: true}},
		{"WPA3 beside WPA2 (transition)", "WIFI:T:WPA;S:Home;P:hunter2hunter;R:1;;", QR{SSID: "Home", Passphrase: "hunter2hunter"}},
		{"unknown fields are skipped", "WIFI:T:WPA;S:Home;X:what;P:hunter2hunter;;", QR{SSID: "Home", Passphrase: "hunter2hunter"}},

		{`escaped ;`, `WIFI:T:WPA;S:a\;b;P:pass\;word;;`, QR{SSID: "a;b", Passphrase: "pass;word"}},
		{`escaped :`, `WIFI:T:WPA;S:a\:b;P:pass\:word;;`, QR{SSID: "a:b", Passphrase: "pass:word"}},
		{`escaped \`, `WIFI:T:WPA;S:a\\b;P:pass\\word;;`, QR{SSID: `a\b`, Passphrase: `pass\word`}},
		{`escaped ,`, `WIFI:T:WPA;S:a\,b;P:pass\,word;;`, QR{SSID: "a,b", Passphrase: "pass,word"}},
		{`escaped "`, `WIFI:T:WPA;S:\"Home\";P:hunter2hunter;;`, QR{SSID: `"Home"`, Passphrase: "hunter2hunter"}},
		{`all of them`, `WIFI:S:\;\:\\\,\";T:WPA;P:\\\;\,\:\"abc;;`, QR{SSID: `;:\,"`, Passphrase: `\;,:"abc`}},
		{`a backslash at the end of a value`, `WIFI:T:WPA;S:Home\\;P:hunter2hunter\\;;`, QR{SSID: `Home\`, Passphrase: `hunter2hunter\`}},
		{"an unescaped colon in a value", "WIFI:T:WPA;S:a:b;P:pass:word;;", QR{SSID: "a:b", Passphrase: "pass:word"}},

		{"quoted", `WIFI:T:WPA;S:"Home";P:"hunter2hunter";;`, QR{SSID: "Home", Passphrase: "hunter2hunter"}},
		{"quoted hex-looking name", `WIFI:T:WPA;S:"cafe1234";P:hunter2hunter;;`, QR{SSID: "cafe1234", Passphrase: "hunter2hunter"}},
		{"unquoted hex-looking name is a name", `WIFI:T:WPA;S:cafe1234;P:hunter2hunter;;`, QR{SSID: "cafe1234", Passphrase: "hunter2hunter"}},
		{"a quote only at one end stays", `WIFI:T:WPA;S:"Home;P:hunter2hunter;;`, QR{SSID: `"Home`, Passphrase: "hunter2hunter"}},
		{"an escaped closing quote stays", `WIFI:T:WPA;S:"Home\";P:hunter2hunter;;`, QR{SSID: `"Home"`, Passphrase: "hunter2hunter"}},

		{"spaces and unicode", "WIFI:T:WPA;S:Café Wi-Fi ☕;P:pass word ünïcode;;", QR{SSID: "Café Wi-Fi ☕", Passphrase: "pass word ünïcode"}},
		{"shortest passphrase", "WIFI:T:WPA;S:Home;P:12345678;;", QR{SSID: "Home", Passphrase: "12345678"}},
		{"longest passphrase", "WIFI:T:WPA;S:Home;P:" + strings.Repeat("x", 63) + ";;", QR{SSID: "Home", Passphrase: strings.Repeat("x", 63)}},
		{"longest name", "WIFI:T:WPA;S:" + strings.Repeat("n", 32) + ";P:hunter2hunter;;", QR{SSID: strings.Repeat("n", 32), Passphrase: "hunter2hunter"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseQR(c.code)
			if err != nil {
				t.Fatalf("ParseQR(%q): %v", c.code, err)
			}
			if got != c.want {
				t.Errorf("ParseQR(%q) = %+v, want %+v", c.code, got, c.want)
			}
		})
	}
}

// A code that is not a Wi-Fi code, or one the device will not join, is refused with the reason the
// screen says.
func TestAWifiQRCodeIsRefused(t *testing.T) {
	for _, c := range []struct {
		name, code string
		want       error // one of the ErrQR errors, or nil for Join's own reason
	}{
		{"empty", "", ErrQRNotWifi},
		{"a web address", "https://example.com/", ErrQRNotWifi},
		{"a Matter code", "MT:Y.K9042C00KA0648G00", ErrQRNotWifi},
		{"almost", "WIFI", ErrQRNotWifi},
		{"WIFI: in the middle", "see WIFI:S:Home;;", ErrQRNotWifi},

		{"nothing after the prefix", "WIFI:", ErrQRInvalid},
		{"only semicolons", "WIFI:;;", ErrQRInvalid},
		{"no name", "WIFI:T:WPA;P:hunter2hunter;;", ErrQRInvalid},
		{"empty name", "WIFI:T:WPA;S:;P:hunter2hunter;;", ErrQRInvalid},
		{"a quoted empty name", `WIFI:T:WPA;S:"";P:hunter2hunter;;`, ErrQRInvalid},
		{"cut short", "WIFI:T:WPA;S:Home;P:hunter2hun", ErrQRInvalid},
		{"cut short after an escape", `WIFI:T:WPA;S:Home;P:hunter2\;`, ErrQRInvalid},
		{"ends in an escape", `WIFI:T:WPA;S:Home;P:hunter2hunter\`, ErrQRInvalid},
		{"a field without a colon", "WIFI:T:WPA;Home;P:hunter2hunter;;", ErrQRInvalid},
		{"a field without a name", "WIFI::Home;;", ErrQRInvalid},
		{"a name given twice", "WIFI:S:Home;S:Other;P:hunter2hunter;;", ErrQRInvalid},
		{"a passphrase given twice", "WIFI:S:Home;P:hunter2hunter;p:other-one;;", ErrQRInvalid},
		{"WPA without a passphrase", "WIFI:T:WPA;S:Home;;", ErrQRInvalid},
		{"unknown security", "WIFI:T:WPA4;S:Home;P:hunter2hunter;;", ErrQRInvalid},
		{"hidden neither true nor false", "WIFI:T:WPA;S:Home;P:hunter2hunter;H:maybe;;", ErrQRInvalid},

		{"WPA3 only", "WIFI:T:SAE;S:Home;P:hunter2hunter;;", ErrQRWPA3},
		{"WPA3 only, said so", "WIFI:T:WPA3;S:Home;P:hunter2hunter;;", ErrQRWPA3},
		{"enterprise", "WIFI:T:WPA2-EAP;S:Campus;E:PEAP;PH2:MSCHAPV2;I:me;P:secret;;", ErrQREnterprise},
		{"enterprise WPA3", "WIFI:T:WPA3-EAP;S:Campus;E:TTLS;I:me;P:secret;;", ErrQREnterprise},
		{"enterprise by its method alone", "WIFI:T:WPA;S:Campus;E:PEAP;I:me;P:secret12;;", ErrQREnterprise},
		{"WEP", "WIFI:T:WEP;S:Old;P:abcde;;", ErrQRWEP},

		{"passphrase too short", "WIFI:T:WPA;S:Home;P:1234567;;", nil},
		{"passphrase too long", "WIFI:T:WPA;S:Home;P:" + strings.Repeat("x", 64) + ";;", nil},
		{"name too long", "WIFI:T:WPA;S:" + strings.Repeat("n", 33) + ";P:hunter2hunter;;", nil},
		{"a line ending in the name", "WIFI:T:WPA;S:Ho\nme;P:hunter2hunter;;", nil},
		{"a line ending in the passphrase", "WIFI:T:WPA;S:Home;P:hunter2\nhunter;;", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseQR(c.code)
			if err == nil {
				t.Fatalf("ParseQR(%q) = %+v, want it refused", c.code, got)
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("ParseQR(%q): %v, want %v", c.code, err, c.want)
			}
			if c.want == nil {
				for _, e := range []error{ErrQRNotWifi, ErrQRInvalid, ErrQRWPA3, ErrQREnterprise, ErrQRWEP} {
					if errors.Is(err, e) {
						t.Errorf("ParseQR(%q): %v, want Join's own reason", c.code, err)
					}
				}
			}
		})
	}
}

// A hidden network is joined as any other: every network is written so that the supplicant asks for
// it by name.
func TestAHiddenNetworkIsAskedForByName(t *testing.T) {
	q, err := ParseQR("WIFI:T:WPA;S:Attic;P:hunter2hunter;H:true;;")
	if err != nil || !q.Hidden {
		t.Fatalf("ParseQR = %+v, %v", q, err)
	}
	if b := block(q.SSID, q.Passphrase); !strings.Contains(b, "\tscan_ssid=1\n") {
		t.Errorf("hidden network:\n%s\nwant scan_ssid=1", b)
	}
}
