//go:build !dot && !spot && !panel

package layout

import (
	"strings"
	"testing"
)

// The ThinkSmart View is read from its device tree, whatever the command line and the mute driver
// say, and a name that only starts the same is not it.
func TestTheThinkSmartViewIsReadFromTheDeviceTree(t *testing.T) {
	const gone = "/nonexistent/amazon-gating"
	for _, c := range []struct {
		name       string
		compatible []string
		want       string
	}{
		{"the mainline tree", []string{"lenovo,cd-18781y", "qcom,apq8053", "qcom,msm8953"}, boardThinkSmart},
		{"another board on the same SoC", []string{"xiaomi,mido", "qcom,msm8953"}, boardCronos},
		{"a name that only starts the same", []string{"lenovo,cd-18781yz"}, boardCronos},
	} {
		if got := showBoard("quiet loglevel=0", gone, c.compatible); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A unit's MAC comes from its eMMC CID: the same for the same CID, another for another, locally
// administered and unicast, and nothing for what is not a CID.
func TestTheThinkSmartMACComesFromTheCID(t *testing.T) {
	const cid = "150100444436384d420112345678a500" // made up
	a, ok := thinkSmartMAC(cid + "\n")
	if !ok {
		t.Fatalf("thinkSmartMAC(%q) said no", cid)
	}
	if b, _ := thinkSmartMAC(strings.ToUpper(cid)); b != a {
		t.Errorf("the same CID in capitals gave %q, want %q", b, a)
	}
	if b, _ := thinkSmartMAC("150100444436384d420187654321a500"); b == a {
		t.Errorf("another CID gave the same MAC %q", a)
	}
	if first := a[:2]; first[1] != '2' && first[1] != '6' && first[1] != 'a' && first[1] != 'e' {
		t.Errorf("%q is not locally administered and unicast", a)
	}
	if sky, _ := macFromCID(cid); sky == a {
		t.Errorf("the Skylight's MAC for the same CID is the same: %q", a)
	}
	for _, bad := range []string{"", "1501", cid + "0", strings.Repeat("g", 32)} {
		if _, ok := thinkSmartMAC(bad); ok {
			t.Errorf("thinkSmartMAC(%q) said yes", bad)
		}
	}
}

func TestEveryBoardHasADefaultName(t *testing.T) {
	for board, want := range map[string]string{
		boardCronos: "Echo Show", boardCheckers: "Echo Show", boardCrown: "Echo Show", boardAthena: "Echo Show",
		boardSkylight: "Skylight Calendar", boardThinkSmart: "ThinkSmart View",
	} {
		if got := defaultName(board); got != want {
			t.Errorf("defaultName(%q) = %q, want %q", board, got, want)
		}
	}
}
