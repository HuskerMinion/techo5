package boot

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

func listenAddr() string { return fmt.Sprintf(":%d", layout.Port) }

func name() string {
	if b, err := os.ReadFile(layout.NamePath); err == nil {
		if recorded := strings.TrimSpace(readableName(b)); recorded != "" {
			return recorded
		}
	}

	mac, err := layout.FactoryMAC()
	if err != nil {
		return layout.DefaultName
	}
	return layout.NameFromMAC(mac)
}

// readableName is the name file as text. An installer run on Windows wrote it in the Windows code page,
// so "Küche" arrived as one 0xFC byte rather than UTF-8. Go's protobuf will not send a string that is not
// UTF-8, so the device could not tell Home Assistant its name and every connection dropped after the
// handshake; D-Bus refuses it too. Such a name is read as Windows-1252, the code page of the western
// languages: Latin-1 but for 0x80 to 0x9F, where it keeps letters like Š and Œ.
func readableName(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
		if c >= 0x80 && c < 0xa0 && cp1252[c-0x80] != 0 {
			r[i] = cp1252[c-0x80]
		}
	}
	slog.Warn("the device name is not UTF-8; reading it as Windows-1252", "name", string(r))
	return string(r)
}

// cp1252 is what Windows-1252 has at 0x80 to 0x9F; 0 where it has nothing, kept as the Latin-1 control.
var cp1252 = [32]rune{
	'€', 0, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0, 'Ž', 0,
	0, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0, 'ž', 'Ÿ',
}
