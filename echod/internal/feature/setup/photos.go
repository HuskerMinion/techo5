package setup

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// Photos for the slideshow, kept on the device (feature/home/photos.go). The browser shrinks each
// picture to the screen before it is sent - a phone's photo is megabytes and twelve million pixels,
// which this device takes seconds and a good share of its memory to decode - and sends them one at a
// time, so a hundred photos is a hundred small requests rather than one the device has to hold.

// maxPhoto is one upload, after the browser has shrunk it.
const maxPhoto = 8 << 20

func photosSection(w http.ResponseWriter, token string) {
	n := home.LocalPhotoCount()
	fieldsetOpen(w, "photos", "Photos on this device")
	fmt.Fprintf(w, `<p style="margin:0"><strong>%d</strong> kept, of at most %d. The slideshow shows them when it has no Home
	  Assistant photos to show; turn it on above, or on the device under Settings, Display, Slideshow.</p>
	 <label for="pics">Add photos</label>
	 <input id="pics" type="file" accept="image/*" multiple>
	 <p id="picsays" class="note">They are made screen-sized on this phone or computer before they are sent.</p>
	 <script>
	 document.getElementById('pics').addEventListener('change', async (ev) => {
	   const say = document.getElementById('picsays');
	   const files = [...ev.target.files];
	   let done = 0, failed = 0;
	   for (const f of files) {
	     say.textContent = 'Sending ' + (done + failed + 1) + ' of ' + files.length + '...';
	     try {
	       const bmp = await createImageBitmap(f, {imageOrientation: 'from-image'});
	       const scale = Math.min(1, 1600 / Math.max(bmp.width, bmp.height));
	       const c = document.createElement('canvas');
	       c.width = Math.round(bmp.width * scale); c.height = Math.round(bmp.height * scale);
	       c.getContext('2d').drawImage(bmp, 0, 0, c.width, c.height);
	       const blob = await new Promise(r => c.toBlob(r, 'image/jpeg', 0.85));
	       const fd = new FormData(); fd.append('token', %q); fd.append('photo', blob, 'photo.jpg');
	       const r = await fetch('/setup/photo', {method: 'POST', body: fd});
	       if (r.ok) { done++; } else { failed++; say.textContent = await r.text(); }
	     } catch (e) { failed++; }
	   }
	   say.textContent = done + ' added' + (failed ? ', ' + failed + ' could not be' : '') + '.';
	   setTimeout(() => location.reload(), 1500);
	 });
	 </script>`, n, home.MaxLocalPhotos, token)
	if n > 0 {
		fmt.Fprint(w, `<form method="post" action="/setup/save">`)
		hidden(w, token, "photos-remove")
		fmt.Fprint(w, `<p><label><input type="checkbox" name="sure" value="yes" style="width:auto"> Yes, remove them all</label></p>
		 <p><button type="submit" class="danger">Remove all photos</button></p></form>`)
	}
	fmt.Fprint(w, `</fieldset>`)
}

// photo takes one picture from the page's script.
func (f *Feature) photo(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post a photo", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhoto+64<<10)
	if err := r.ParseMultipartForm(maxPhoto); err != nil {
		http.Error(w, "that photo was too big", http.StatusBadRequest)
		return
	}
	// The form carries the session it came from, as every save does, so another site's page cannot
	// post a picture here with a browser's cookie.
	if !sameToken(r.FormValue("token"), token) {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	file, _, err := r.FormFile("photo")
	if err != nil {
		http.Error(w, "no photo was sent", http.StatusBadRequest)
		return
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, maxPhoto))
	if err != nil {
		http.Error(w, "the photo did not arrive whole", http.StatusBadRequest)
		return
	}
	if err := home.AddLocalPhoto(b); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.used()
	w.WriteHeader(http.StatusNoContent)
}

func removePhotos(r *http.Request) string {
	if r.PostFormValue("sure") != "yes" {
		return "tick the box to remove them all"
	}
	if err := home.RemoveLocalPhotos(); err != nil {
		return "could not remove them: " + err.Error()
	}
	slog.Info("setup page: the device's photos were removed")
	return ""
}
