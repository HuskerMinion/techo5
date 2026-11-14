package setup

import (
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
)

// Fonts of the owner's own (lib/fonts/own.go): font files the screen draws the characters from that
// its own fonts have no glyph for, Korean, Chinese and Japanese among them. Sent from the page one
// file at a time and written to the device as they arrive, never held in memory whole, since such a
// font is many megabytes. Only on a device with a screen: the display hands in how (ScreenChoices).

// OwnFont is one of the fonts folder's files as the page lists it.
type OwnFont struct {
	File    string
	Size    int64
	Name    string
	Problem string // why it is not used, or ""
}

// ownFontsSection lists the fonts of one's own, with a Remove button each, and takes new ones.
func ownFontsSection(w http.ResponseWriter, token string) {
	s := screen.Load()
	if s == nil || s.OwnFonts == nil {
		return
	}
	fieldsetOpen(w, "fonts", "Fonts of your own")
	fmt.Fprint(w, `<p class="note" style="margin-top:0">For characters the screen's fonts do not have, such as Korean, Chinese
	  and Japanese: the screen draws any character its font and Go lack from these, in the order of their
	  file names. They are never the font for everything; that is Font, above.</p>`)
	own := s.OwnFonts()
	if len(own) == 0 {
		fmt.Fprint(w, `<p>None yet.</p>`)
	}
	for _, o := range own {
		about := o.Name
		if o.Problem != "" {
			about = "not used: " + o.Problem
		}
		fmt.Fprintf(w, `<form method="post" action="/setup/save" style="margin:0">`)
		hidden(w, token, "font-remove")
		fmt.Fprintf(w, `<input type="hidden" name="file" value="%s">
		 <p style="margin:.3em 0"><strong>%s</strong>, %.1f MB<br><span class="note">%s</span>
		 <button type="submit" class="quiet danger" style="margin-left:.5em">Remove</button></p></form>`,
			html.EscapeString(o.File), html.EscapeString(o.File), float64(o.Size)/(1<<20), html.EscapeString(about))
	}
	fmt.Fprintf(w, `<label for="fontfile">Add a font</label>
	 <input id="fontfile" type="file" accept=".ttf,.otf,.ttc">
	 <p id="fontsays" class="note">A .ttf, .otf or .ttc file of at most %d MB. Noto Sans CJK, from Google Fonts, has
	  all three languages.</p>
	 <script>
	 document.getElementById('fontfile').addEventListener('change', async (ev) => {
	   const say = document.getElementById('fontsays');
	   const f = ev.target.files[0];
	   if (!f) return;
	   say.textContent = 'Sending ' + f.name + '...';
	   const fd = new FormData(); fd.append('token', %q); fd.append('font', f, f.name);
	   try {
	     const r = await fetch('/setup/font', {method: 'POST', body: fd});
	     say.textContent = r.ok ? f.name + ' added.' : await r.text();
	     if (r.ok) setTimeout(() => location.reload(), 1200);
	   } catch (e) { say.textContent = 'It could not be sent.'; }
	 });
	 </script></fieldset>`, s.MaxFont>>20, token)
}

// font takes one font file from the page's script. The form's token comes first and is checked
// before anything of the file is read, which is then written to the device as it arrives.
func (f *Feature) font(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post a font", http.StatusMethodNotAllowed)
		return
	}
	s := screen.Load()
	if s == nil || s.AddFont == nil {
		http.Error(w, "this device has no screen", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxFont+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "that was not a font sent from this page", http.StatusBadRequest)
		return
	}
	// The form carries the session it came from, as every save does, so another site's page cannot
	// post a font here with a browser's cookie.
	p, err := mr.NextPart()
	if err != nil || p.FormName() != "token" {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	got, err := io.ReadAll(io.LimitReader(p, 256))
	if err != nil || !sameToken(string(got), token) {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	p, err = mr.NextPart()
	if err != nil || p.FormName() != "font" {
		http.Error(w, "no font was sent", http.StatusBadRequest)
		return
	}
	if err := s.AddFont(p.FileName(), p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.used()
	w.WriteHeader(http.StatusNoContent)
}

func removeFont(r *http.Request) string {
	s := screen.Load()
	if s == nil || s.RemoveFont == nil {
		return "this device has no screen"
	}
	file := r.PostFormValue("file")
	if err := s.RemoveFont(file); err != nil {
		return err.Error()
	}
	slog.Info("setup page: a font of one's own was removed", "file", file)
	return ""
}
