package notify

import (
	"strings"
	"testing"
)

// The size reads large and huge in any letter case and is normal for anything else, so a call that
// leaves it out, or says something else, is the notification it always was.
func TestParseSize(t *testing.T) {
	for in, want := range map[string]Size{
		"": SizeNormal, "normal": SizeNormal, "Normal": SizeNormal, "tiny": SizeNormal, "0": SizeNormal,
		"large": SizeLarge, " LARGE ": SizeLarge, "Huge": SizeHuge, "huge": SizeHuge,
	} {
		if got := ParseSize(in); got != want {
			t.Errorf("ParseSize(%q) = %q, want %q", in, got, want)
		}
	}
}

// The sized actions are the plain ones with a size last, which the plain ones do not take (so no
// automation that works today stops working), and a notification keeps its size for the screen.
func TestActionsTakeASize(t *testing.T) {
	f := newRig(t, true).f
	args := map[string][]string{}
	for _, a := range f.Actions() {
		for _, arg := range a.Args {
			args[a.Name] = append(args[a.Name], arg.Name)
		}
	}
	for _, name := range []string{"notify", "notify_picture"} {
		plain, sized := args[name], args[name+"_sized"]
		if len(sized) != len(plain)+1 || sized[len(sized)-1] != "size" || strings.Join(sized[:len(plain)], ",") != strings.Join(plain, ",") {
			t.Errorf("%s(%v) and %s_sized(%v)", name, plain, name, sized)
		}
	}
	if err := f.Notify("Disk full", "Server", "", "", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.Card(); n.Size != SizeNormal {
		t.Errorf("a plain Notify has size %q", n.Size)
	}
	if err := f.NotifySized("Disk full", "Server", "", "off", "", "", 0, "HUGE"); err != nil {
		t.Fatal(err)
	}
	if n, ok := f.Card(); !ok || n.Size != SizeHuge {
		t.Errorf("a huge notification has size %q (shown %v)", n.Size, ok)
	}
}

// A picture notification carries its size to the camera page's caption.
func TestPictureSizeReachesTheCaption(t *testing.T) {
	r := newRig(t, true)
	f, pics := r.f, r.pics
	if err := f.NotifyPictureSized("Door open", "Front", "camera.front", "off", "", "off", "", 0, "large"); err != nil {
		t.Fatal(err)
	}
	if len(pics.shown) != 1 || pics.shown[0].Size != "large" {
		t.Errorf("caption %+v", pics.shown)
	}
}
