package security

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestDigest(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	d := &Digest{Realm: "r"}
	h := func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }
	auth := func(user, pass, nonce string) string {
		resp := h(h(user+":r:"+pass) + ":" + nonce + ":" + h("GET:/camera.jpg"))
		return fmt.Sprintf(`Digest username="%s", realm="r", nonce="%s", uri="/camera.jpg", response="%s"`, user, nonce, resp)
	}
	if d.Check(auth("camera", "", d.current()), "GET") {
		t.Fatal("in with no password set")
	}
	if err := CheckLogin("u", "short"); err == nil {
		t.Fatal("a short password taken")
	}
	if err := config.Set().CamStream().Login("u", "password-one"); err != nil {
		t.Fatal(err)
	}
	if !d.Check(auth("u", "password-one", d.current()), "GET") {
		t.Fatal("the login refused")
	}
	if d.Check(auth("u", "password-two", d.current()), "GET") || d.Check(auth("u", "password-one", "stale"), "GET") {
		t.Fatal("a wrong password or nonce taken")
	}
}
