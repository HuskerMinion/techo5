package security

import (
	"os"
	"strings"
	"testing"
)

func TestParseKeys(t *testing.T) {
	ed := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHD8TFGO3hxbn85EQV6PpKWtoA9r2RMDQwp1Z1MiR7KV someone@desk"
	// No comment on the second one, so keyLabel has to fall back to the type. Both are real keys:
	// what is inside a key is checked now, and a made-up body would be refused as it should be.
	bare := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDekQK/lzJW2ohfEa2o8Sp7RjfCeYDVryU67akoQN5vZ"

	keys, err := parseKeys("\r\n  " + ed + "  \r\n\n" + bare + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != ed || keys[1] != bare {
		t.Fatalf("keys = %q", keys)
	}
	if got := keyLabel(keys[0]); got != "someone@desk" {
		t.Errorf("label = %q", got)
	}
	if got := keyLabel(keys[1]); got != "ssh-ed25519" {
		t.Errorf("label without a comment = %q", got)
	}

	if keys, err := parseKeys("  \n"); err != nil || len(keys) != 0 {
		t.Errorf("empty = %q, %v", keys, err)
	}

	for _, bad := range []string{
		`command="rm -rf /" ` + ed, // options in front of a key run things
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"ssh-ed25519",
		"hello world",
	} {
		if _, err := parseKeys(ed + "\n" + bad); err == nil {
			t.Errorf("accepted %q", bad)
		} else if strings.Contains(err.Error(), "AAAA") && strings.Contains(bad, "PRIVATE") {
			t.Errorf("error echoes key material: %v", err)
		}
	}
}

// A public key names its type twice: once in front and once inside the base64, and the one inside is
// what a server reads. A line that gets those two wrong is stored, reported as installed, and then
// silently refused at every login - which is exactly what happened when a key was pasted onto a line
// that already carried its type, and cost an afternoon to find.
func TestAKeyWhoseInsideDoesNotMatchIsRefused(t *testing.T) {
	const good = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIClsDs7ssckQ2RBTOA+QAPp4hEpK2YfeUD4HRcOwDbUT someone@desk"

	if keys, err := parseKeys(good); err != nil || len(keys) != 1 {
		t.Fatalf("a real key was refused: %v (%d kept)", err, len(keys))
	}

	for what, line := range map[string]string{
		"the type pasted in twice":     "ssh-ed25519 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIClsDs7ssckQ2RBTOA+QAPp4hEpK2YfeUD4HRcOwDbUT someone@desk",
		"a type that is not the key's": "ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAIClsDs7ssckQ2RBTOA+QAPp4hEpK2YfeUD4HRcOwDbUT someone@desk",
		"not base64 at all":            "ssh-ed25519 not-a-key someone@desk",
		"too short to name a type":     "ssh-ed25519 AAAA someone@desk",
	} {
		keys, err := parseKeys(line)
		if err == nil {
			t.Errorf("%s was accepted, and nothing could have logged in with it", what)
			continue
		}
		if len(keys) != 0 {
			t.Errorf("%s: refused but %d keys came back", what, len(keys))
		}
		if strings.Contains(err.Error(), "someone@desk") && !strings.Contains(err.Error(), "…") {
			t.Errorf("%s: the error repeats the whole line: %v", what, err)
		}
	}
}

// A file already on a device can hold a line that is not a key - one was written here before the
// checks got stricter. Reading must not answer that with nothing: settleSSH reads no keys as "nobody
// can log in" and leaves the server down, so one bad line would take SSH off a device that still has
// a good key sitting next to it, during an update, silently.
func TestOneBadLineDoesNotCostTheGoodOnes(t *testing.T) {
	dir := t.TempDir()
	old := KeysDir
	KeysDir = dir
	t.Cleanup(func() { KeysDir = old })

	good := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHD8TFGO3hxbn85EQV6PpKWtoA9r2RMDQwp1Z1MiR7KV someone@desk"
	bad := "ssh-ed25519 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHD8TFGO3hxbn85EQV6PpKWtoA9r2RMDQwp1Z1MiR7KV someone@desk"
	if err := os.WriteFile(keysFile(), []byte(bad+"\n"+good+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	keys := readKeys()
	if len(keys) != 1 || keys[0] != good {
		t.Fatalf("readKeys = %q, want the one key that works", keys)
	}
}
