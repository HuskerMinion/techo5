package dlna

import (
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// shownURI is the address of s as the renderer reports it to the controller at host, in its answers
// and its events. The controller that set it gets it whole. Any other gets it with the values of
// query parameters that look like credentials blanked: an address often carries the token of the
// server it comes from (a Plex server's X-Plex-Token), and any device on the network may ask what is
// playing.
func shownURI(s song, host string) string {
	if s.uri == "" || sameHost(s.from, host) {
		return s.uri
	}
	return withoutSecrets(s.uri)
}

// sameHost is whether two hosts are the same address (an IPv4 address and its IPv6-mapped form are).
func sameHost(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	return ia != nil && ib != nil && ia.Equal(ib)
}

// shownMeta is the description of s (its DIDL-Lite) as the renderer reports it to the controller at
// host. The controller that set it gets it as it sent it. Any other gets every address in it shown as
// shownURI shows the song's own: a res's address is the song's, and a cover's (albumArtURI) often
// carries the same token. A description that cannot be read is not shown to another at all.
func shownMeta(s song, host string) string {
	if s.meta == "" || sameHost(s.from, host) {
		return s.meta
	}
	return metaWithoutSecrets(s.meta)
}

// metaWithoutSecrets is a DIDL-Lite description with each text and attribute value that is an address
// carrying a query, a fragment or a user (where a credential would be) passed through withoutSecrets.
// It is read and written again token by token, so whatever else it holds stays as it was, its
// prefixes too, and nothing in it can come out as markup it was not. One that is not well-formed (a
// controller that escaped a bare & only once, an HTML entity) is kept as it came, with each address in
// it passed through withoutSecrets (laxWithoutSecrets): its title and artist still reach the others.
func metaWithoutSecrets(meta string) string {
	if out, ok := strictWithoutSecrets(meta); ok {
		return out
	}
	return laxWithoutSecrets(meta)
}

// anAddressInMeta is an address in a description that is not read as XML: up to the first space,
// quote or angle bracket, so the tag after it is not taken in.
var anAddressInMeta = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)

// laxWithoutSecrets is a description that could not be read as XML, each address in it passed through
// withoutSecrets. Its query is read as it stands, so a value after an escaped "&amp;" is still found
// by its name's ending (secretParam).
func laxWithoutSecrets(meta string) string {
	return anAddressInMeta.ReplaceAllStringFunc(meta, secretless)
}

// strictWithoutSecrets is metaWithoutSecrets for a well-formed description; false if it is not one.
func strictWithoutSecrets(meta string) (string, bool) {
	d := xml.NewDecoder(strings.NewReader(meta))
	var b strings.Builder
	var open []xml.Name
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			open = append(open, t.Name)
			b.WriteString("<" + rawName(t.Name))
			for _, a := range t.Attr {
				b.WriteString(" " + rawName(a.Name) + `="`)
				escapeInto(&b, secretless(a.Value))
				b.WriteString(`"`)
			}
			b.WriteString(">")
		case xml.EndElement:
			// RawToken does not match end tags with their starts: done here, so what is written is as
			// well-formed as a decoder that checks would have taken.
			if len(open) == 0 || open[len(open)-1] != t.Name {
				return "", false
			}
			open = open[:len(open)-1]
			b.WriteString("</" + rawName(t.Name) + ">")
		case xml.CharData:
			escapeInto(&b, secretless(string(t)))
		case xml.ProcInst:
			if t.Target == "xml" {
				b.WriteString("<?xml " + string(t.Inst) + "?>")
			}
		}
		// A comment or a directive is dropped: neither is anything a controller reads.
	}
	if len(open) != 0 {
		return "", false
	}
	return b.String(), true
}

// rawName is a name as RawToken gives it: its prefix, not its namespace, which is how it was written.
func rawName(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// escapeInto writes v escaped for a text or a quoted attribute value.
func escapeInto(b *strings.Builder, v string) {
	_ = xml.EscapeText(b, []byte(v))
}

// secretless is v through withoutSecrets when it is an http address that could carry a credential,
// and v as it is otherwise: a title or a date is left alone.
func secretless(v string) string {
	t := strings.TrimSpace(v)
	low := strings.ToLower(t)
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		return v
	}
	if p, err := url.Parse(t); err == nil && p.RawQuery == "" && p.Fragment == "" && p.User == nil {
		return v
	}
	return withoutSecrets(t)
}

// secretParam is whether a query parameter's name looks like a credential.
func secretParam(name string) bool {
	n := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(name))
	for _, s := range []string{"token", "apikey", "password", "passwd", "secret", "auth", "sig", "signature", "key", "credential"} {
		if n == s || strings.HasSuffix(n, s) {
			return true
		}
	}
	return false
}

// withoutSecrets is u with the values of its credential-like query parameters blanked, and any user,
// password and fragment in it dropped. An address that cannot be read is not shown at all. A Subsonic
// server's address (Navidrome's, say) names its user as u and carries the password as p, or a salted
// hash of it as t with its salt as s: those are blanked when u is there, since on their own they are
// ordinary names. An S3 address signed for a while carries its key's id and signature
// (X-Amz-Credential, X-Amz-Signature).
func withoutSecrets(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	p.User = nil
	p.Fragment, p.RawFragment = "", ""
	if p.RawQuery == "" {
		return p.String()
	}
	parts := strings.Split(p.RawQuery, "&")
	names := make([]string, len(parts))
	subsonic := false
	for i, kv := range parts {
		name, _, _ := strings.Cut(kv, "=")
		if dec, err := url.QueryUnescape(name); err == nil {
			name = dec
		}
		names[i] = name
		subsonic = subsonic || name == "u"
	}
	for i, kv := range parts {
		name := names[i]
		if secretParam(name) || (subsonic && (name == "p" || name == "t" || name == "s")) {
			parts[i] = strings.SplitN(kv, "=", 2)[0] + "="
		}
	}
	p.RawQuery = strings.Join(parts, "&")
	return p.String()
}

// anAddress is an http address in an error's text, up to the quote or space that ends it.
var anAddress = regexp.MustCompile(`(?i)\bhttps?://[^\s'"]+`)

// quietErr is err with any address in its text shown as withoutSecrets shows it, for the log: an
// error from fetching a song (a *url.Error) names the whole address, token and all, and the log is
// sent with a bug report. The error underneath is kept, so what it is (a context ended, say) can still
// be told.
func quietErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	quiet := anAddress.ReplaceAllStringFunc(msg, func(u string) string {
		if w := withoutSecrets(u); w != "" {
			return w
		}
		return "the address"
	})
	if quiet == msg {
		return err
	}
	return quietError{msg: quiet, err: err}
}

type quietError struct {
	msg string
	err error
}

func (e quietError) Error() string { return e.msg }
func (e quietError) Unwrap() error { return e.err }
