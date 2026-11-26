//go:build !panel

package setup

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
	"github.com/HuskerMinion/techo5/echod/internal/feature/diag"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The Add-ons card: what the catalog offers this device, what is installed, and what was put on it by
// hand (feature/addons). It is a manager, not a setting: nothing here is a choice the device keeps
// beyond which add-ons are installed and whether each is on.
//
// Installing is only possible here, behind the page's own let-in, never from Home Assistant, a voice
// command or the screen, so the warning below is always read first, and its box ticked, for every
// install whether or not the add-on is in the catalog. An update that asks for more than the version
// before (a new port, root) asks again.

func init() {
	findKeywords["addons"] = []string{"addon", "addons", "add-on", "plugin", "plex", "extension", "rc.local"}
	saves["addon-install"] = saveAddonInstall
	saves["addon-update"] = saveAddonUpdate
	saves["addon-start"] = saveAddonStart
	saves["addon-stop"] = saveAddonStop
	saves["addon-remove"] = saveAddonRemove
	saves["addon-check"] = saveAddonCheck
}

// addonWarning is the warning every install shows, word for word as it was agreed
// (TECHO5-ADDONS-PLAN.md), and what a hand-installed add-on is listed under, without the box.
const addonWarning = `<p><strong>Add-ons run on this device with access to its microphones, speaker, network and
 your Home Assistant connection.</strong> They are made and kept by their authors, not by TECHO5. Being listed here
 means it was looked over when it was added. It doesn't mean it's safe, that it will keep working, or that later
 versions were checked. Only install add-ons from people you trust.</p>`

// addonsBox is the box under the warning, naming the add-on and its author.
func addonsBox(name, author string) string {
	return fmt.Sprintf(`<p><label><input type="checkbox" name="understood" value="yes" required>
	 I understand the risk and want to install <strong>%s</strong> by <strong>%s</strong>.</label></p>`,
		html.EscapeString(name), html.EscapeString(author))
}

// addonsAsk is what a version asks of the device, as the card lists it before an install: with the
// extra files this device's build brings, so the owner sees everything that is downloaded.
func addonsAsk(w http.ResponseWriter, v catalog.Version, files []catalog.File, asRoot bool) {
	fmt.Fprint(w, `<ul class="note" style="margin:.3rem 0">`)
	if len(v.Ports) == 0 {
		fmt.Fprint(w, `<li>Network ports: none.</li>`)
	} else {
		var ps []string
		for _, p := range v.Ports {
			ps = append(ps, html.EscapeString(p.String()+" ("+p.Why+")"))
		}
		fmt.Fprintf(w, `<li>Network ports: %s.</li>`, strings.Join(ps, ", "))
	}
	if asRoot {
		if v.Root {
			fmt.Fprint(w, `<li><strong>Runs as root: it can do anything on this device.</strong></li>`)
		} else {
			fmt.Fprint(w, `<li><strong>Runs as root here: this device cannot run it as a user of its own, so it can do anything on this device.</strong></li>`)
		}
	} else {
		fmt.Fprint(w, `<li>Runs as a user of its own, not as root.</li>`)
	}
	if len(files) > 0 {
		var fs []string
		for _, f := range files {
			fs = append(fs, "<code>"+html.EscapeString(f.Name)+"</code> ("+fileSize(f.Size)+")")
		}
		fmt.Fprintf(w, `<li>Downloads beside its program, checked as the program is: %s.</li>`, strings.Join(fs, ", "))
	}
	fmt.Fprintf(w, `<li>Uses about %d MB of memory and %d MB of storage.</li></ul>`, v.MemoryMB, v.DiskMB)
}

// fileSize is a download's size as the card says it: in MB from one up, in kB below.
func fileSize(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d kB", max((n+1023)>>10, 1))
}

func addonsSection(w http.ResponseWriter, token string) {
	f := addons.Get()
	if !addons.Supported() {
		sectionOpen(w, "addons", "Add-ons", "Not on Fire OS")
		fmt.Fprint(w, `<p class="note" style="margin:0">Add-ons run on TECHO5's Linux, and this device is running Fire OS.</p>`)
		sectionClose(w)
		return
	}
	installed := f.Installed()
	available := f.Available()
	free := 0
	for _, l := range available {
		if !l.Installed && l.Runs {
			free++
		}
	}
	summary := []string{count(len(installed), "installed", "installed")}
	if free > 0 {
		summary = append(summary, count(free, "more available", "more available"))
	}
	sectionOpen(w, "addons", "Add-ons", summary...)
	fmt.Fprint(w, `<p class="note" style="margin-top:0">Programs others have made for TECHO5, from TECHO5's catalog of
	 add-ons it has looked over. Each runs from the device's storage, outlasts updates, and can be removed again
	 here. <a href="https://github.com/HuskerMinion/techo5/blob/main/docs/setup.md#add-ons">About add-ons</a>.</p>`)

	if j, on := f.Busy(); on {
		what := map[string]string{"checking": "Checking", "downloading": "Downloading", "installing": "Installing",
			"switching": "Switching to", "trying": "Trying", "removing": "Removing"}[j.What]
		if what == "" {
			what = "Working on"
		}
		fmt.Fprintf(w, `<div class="banner">%s <strong>%s %s</strong>`, what, html.EscapeString(j.ID), html.EscapeString(j.Version))
		if j.What == "downloading" {
			fmt.Fprintf(w, `: %d%%`, int(j.At*100))
		}
		if j.What == "trying" {
			fmt.Fprint(w, `: it has to stay up for a minute, or the version before goes back`)
		}
		fmt.Fprint(w, `.</div><script>setTimeout(()=>location.reload(),3000)</script>`)
	} else if p := f.LastProblem(); p != "" {
		fmt.Fprintf(w, `<div class="banner bad">The last change did not go through: %s</div>`, html.EscapeString(strings.TrimPrefix(p, "addons: ")))
	}

	addonsInstalled(w, token, f, installed)
	addonsAvailable(w, token, available)
	addonsFound(w, f)
	addonsCatalogLine(w, token, f)
	sectionClose(w)
}

// addonsInstalled is each installed add-on: whether it runs, Start or Stop, its log, Update when the
// catalog has a newer version, Remove, and its own page if it has one.
func addonsInstalled(w http.ResponseWriter, token string, f *addons.Feature, installed []addons.Status) {
	if len(installed) == 0 {
		return
	}
	fmt.Fprint(w, `<h3>Installed</h3>`)
	for _, s := range installed {
		fmt.Fprintf(w, `<h4>%s <span class="note">%s, by %s</span></h4>`, html.EscapeString(s.Name),
			html.EscapeString(s.Version), html.EscapeString(s.Author))
		state := s.State
		if !s.Enabled && state == addons.StateStopped {
			state = "stopped; it stays off until Start"
		}
		cls := ""
		switch s.State {
		case addons.StateRunning:
			cls = ` class="ok"`
		case addons.StateGaveUp, addons.StateFailed, addons.StateNoMemory, addons.StateTooBig:
			cls = ` class="bad"`
		}
		fmt.Fprintf(w, `<p style="margin:.2rem 0"><span%s>%s</span>`, cls, html.EscapeString(capital(state)))
		if s.Problem != "" {
			fmt.Fprintf(w, `: %s`, html.EscapeString(s.Problem))
		}
		fmt.Fprint(w, `.`)
		if s.AsRoot {
			fmt.Fprint(w, ` <strong>Runs as root.</strong>`)
		}
		if page := s.Page; page != "" && s.State == addons.StateRunning {
			link := strings.ReplaceAll(page, "{device}", ownAddress())
			fmt.Fprintf(w, ` <a href="%s" data-addon-page="%s">Its page</a>.`, html.EscapeString(link), html.EscapeString(page))
		}
		fmt.Fprint(w, `</p>`)
		if s.Firewall != "" {
			fmt.Fprintf(w, `<p class="note bad">Its ports may not be reachable from the network: %s</p>`, html.EscapeString(s.Firewall))
		}
		if s.Withdrawn != "" {
			fmt.Fprintf(w, `<div class="banner bad">No longer listed in TECHO5's catalog: %s. Removing it is suggested; it
			 is not removed by itself.</div>`, html.EscapeString(s.Withdrawn))
		}
		if s.RolledBack != "" {
			fmt.Fprintf(w, `<p class="note">%s did not stay up on this device, so %s was put back.</p>`,
				html.EscapeString(s.RolledBack), html.EscapeString(s.Version))
		}

		fmt.Fprint(w, `<div class="deckrow">`)
		do := "addon-stop"
		label := "Stop"
		if !s.Enabled || s.State == addons.StateGaveUp || s.State == addons.StateNoMemory || s.State == addons.StateFailed || s.State == addons.StateTooBig {
			do, label = "addon-start", "Start"
		}
		fmt.Fprint(w, `<form method="post" action="/setup/save">`)
		hidden(w, token, do)
		fmt.Fprintf(w, `<input type="hidden" name="id" value="%s"><button type="submit" class="quiet">%s</button></form></div>`,
			html.EscapeString(s.ID), label)

		if u := s.Update; u != nil {
			fmt.Fprintf(w, `<form method="post" action="/setup/save"%s>`, busy(busyInstall))
			hidden(w, token, "addon-update")
			fmt.Fprintf(w, `<input type="hidden" name="id" value="%s"><input type="hidden" name="shown" value="%s">`,
				html.EscapeString(s.ID), html.EscapeString(u.Shown))
			fmt.Fprintf(w, `<p style="margin:.6rem 0 .2rem"><strong>%s</strong> is ready.`, html.EscapeString(u.Version))
			switch {
			case u.Problem != "":
				fmt.Fprintf(w, ` It cannot be installed here: %s.</p></form>`, html.EscapeString(u.Problem))
			case len(u.More) > 0:
				fmt.Fprintf(w, ` It asks for more than the version installed: %s.</p>`, html.EscapeString(strings.Join(u.More, ", ")))
				fmt.Fprint(w, `<div class="banner bad">`+addonWarning+addonsBox(s.Name, s.Author)+`</div>`)
				fmt.Fprint(w, `<p><button type="submit">Update</button></p></form>`)
			default:
				fmt.Fprint(w, ` It asks for nothing more than the version installed.</p><p><button type="submit">Update</button></p></form>`)
			}
		}

		if lines := f.Tail(s.ID, 20); len(lines) > 0 {
			r := diag.Redactor()
			fmt.Fprint(w, `<details><summary class="note">Its log, the last lines</summary><pre style="white-space:pre-wrap;font-size:.8rem">`)
			for _, l := range lines {
				fmt.Fprintln(w, html.EscapeString(r.Text(l)))
			}
			fmt.Fprint(w, `</pre></details>`)
		}

		fmt.Fprint(w, `<form method="post" action="/setup/save">`)
		hidden(w, token, "addon-remove")
		fmt.Fprintf(w, `<input type="hidden" name="id" value="%s">
		 <p><label><input type="checkbox" name="deletestate" value="yes"> Also delete what it keeps (its settings and
		  any account it was linked to); without this they are kept for installing it again</label></p>
		 <p><button type="submit" class="danger">Remove %s</button></p></form>`, html.EscapeString(s.ID), html.EscapeString(s.Name))
	}
}

// addonsAvailable is the catalog's add-ons not installed here: each with what it asks for, and the
// warning and its box; or grayed out with the reason when it does not run here.
func addonsAvailable(w http.ResponseWriter, token string, available []addons.Listing) {
	var list []addons.Listing
	for _, l := range available {
		if !l.Installed {
			list = append(list, l)
		}
	}
	if len(list) == 0 {
		return
	}
	fmt.Fprint(w, `<h3>Available</h3>`)
	for _, l := range list {
		a, v := l.Addon, l.Version
		style := ""
		if !l.Runs {
			style = ` style="opacity:.6"`
		}
		fmt.Fprintf(w, `<div%s><h4>%s <span class="note">%s, by %s</span></h4><p style="margin:.2rem 0">%s</p>`, style,
			html.EscapeString(a.Name), html.EscapeString(v.Version), html.EscapeString(a.Author), html.EscapeString(a.Summary))
		page := a.Page
		if page == "" {
			page = a.Repo
		}
		if l.NeedsNewer {
			// Nothing of the version was read but its number, so there is nothing of what it asks to show:
			// only that a newer TECHO5 lists it in full.
			fmt.Fprintf(w, `<p class="note">Not on this device: %s. <a href="%s">Its page</a>.</p></div>`,
				html.EscapeString(l.Why), html.EscapeString(page))
			continue
		}
		needs := []string{}
		for _, fam := range catalog.Families {
			if min, ok := v.MinTECHO5[fam]; ok {
				needs = append(needs, map[string]string{"show": "Echo Show", "spot": "Echo Spot", "dot": "Echo Dot"}[fam]+" "+min)
			}
		}
		fmt.Fprintf(w, `<p class="note" style="margin:.2rem 0">Needs TECHO5 %s or later. <a href="%s">Its page</a>.</p>`,
			html.EscapeString(strings.Join(needs, ", ")), html.EscapeString(page))
		addonsAsk(w, v, l.Files, l.AsRoot)
		if !l.Runs {
			fmt.Fprintf(w, `<p class="note">Not on this device: %s.</p></div>`, html.EscapeString(l.Why))
			continue
		}
		fmt.Fprintf(w, `<form method="post" action="/setup/save"%s>`, busy(busyInstall))
		hidden(w, token, "addon-install")
		// What the card showed goes back with the form, so the install is of what was read (catalog.Shown).
		fmt.Fprintf(w, `<input type="hidden" name="id" value="%s"><input type="hidden" name="version" value="%s"><input type="hidden" name="shown" value="%s">`,
			html.EscapeString(a.ID), html.EscapeString(v.Version), html.EscapeString(l.Shown))
		fmt.Fprint(w, `<div class="banner bad">`+addonWarning+addonsBox(a.Name, a.Author)+`</div>`)
		fmt.Fprintf(w, `<p><button type="submit">Install %s</button></p></form></div>`, html.EscapeString(a.Name))
	}
}

// addonsFound is what was put on the device by hand, with the warning and no box.
func addonsFound(w http.ResponseWriter, f *addons.Feature) {
	found := f.FindHandInstalled()
	if len(found) == 0 {
		return
	}
	fmt.Fprint(w, `<h3>Found on this device, not from the catalog</h3><ul>`)
	for _, x := range found {
		var how []string
		if x.RCLocal {
			how = append(how, "started from rc.local")
		}
		if x.Running {
			how = append(how, "running now")
		}
		fmt.Fprintf(w, `<li><code>%s</code> <span class="note">(%s)</span></li>`, html.EscapeString(x.Path), strings.Join(how, ", "))
	}
	fmt.Fprint(w, `</ul><p class="note">TECHO5 didn't install these, and can't update or remove them; they keep working as
	 they did. It can see only the programs on the device's storage that rc.local starts, and those running from it now,
	 and shows their names, not what they were started with.</p>`)
	fmt.Fprint(w, `<div class="banner bad">`+addonWarning+`</div>`)
}

// addonsCatalogLine says when the catalog was last fetched, with a button to fetch it now.
func addonsCatalogLine(w http.ResponseWriter, token string, f *addons.Feature) {
	at, problem, have, expires := f.CatalogState()
	fmt.Fprintf(w, `<form method="post" action="/setup/save"%s>`, busy(busyCheck))
	hidden(w, token, "addon-check")
	switch {
	case !at.IsZero():
		fmt.Fprintf(w, `<p class="note">The catalog was last checked at %s.`, at.Local().Format("15:04 on Jan 2"))
	case have:
		fmt.Fprint(w, `<p class="note">The catalog shown is the one this device last fetched.`)
	default:
		fmt.Fprint(w, `<p class="note">The catalog has not been fetched yet; it is fetched once the device's clock is set and it is online.`)
	}
	if problem != "" {
		fmt.Fprintf(w, ` The last check did not work: %s.`, html.EscapeString(strings.TrimPrefix(problem, "addons: ")))
	}
	// A catalog past its expiry is not installed from: the add-ons installed keep running, and the card
	// says why nothing is offered.
	if have && !time.Now().Before(expires) {
		fmt.Fprint(w, ` <strong>The catalog this device has is out of date</strong>, so nothing is installed or updated from
		 it until a fresh one is fetched; the add-ons installed keep running.`)
	}
	fmt.Fprint(w, `</p><p><button type="submit" class="quiet">Check the catalog now</button></p></form>`)
	// A page's link names the device as the browser reached it, which is not always the address the
	// device knows itself by (a name, or the tailnet).
	fmt.Fprint(w, `<script>document.querySelectorAll('a[data-addon-page]').forEach(function(a){
	 a.href=a.getAttribute('data-addon-page').split('{device}').join(location.hostname)})</script>`)
}

// ownAddress is the device's Wi-Fi address, for an add-on's page link before the page's script has put
// the address the browser used in its place.
func ownAddress() string {
	i, err := net.InterfaceByName("wlan0")
	if err == nil {
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				return ipn.IP.String()
			}
		}
	}
	return "localhost"
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// addonID is the add-on a form names, when it is one an add-on could be.
func addonID(r *http.Request) (string, string) {
	id := r.PostFormValue("id")
	if !catalog.ValidID(id) {
		return "", "that is not an add-on; open the page again and try once more"
	}
	return id, ""
}

func saveAddonInstall(r *http.Request) string {
	id, problem := addonID(r)
	if problem != "" {
		return problem
	}
	// The box is checked here, not only in the browser: a form sent without it installs nothing.
	if r.PostFormValue("understood") != "yes" {
		return "tick the box to say you understand the risk; nothing was installed"
	}
	version, shown := r.PostFormValue("version"), r.PostFormValue("shown")
	slog.Info("setup page: installing an add-on", "id", id, "version", version)
	safe.Go("add-on install", func() {
		if err := addons.Get().Install(context.Background(), id, version, shown, true); err != nil {
			slog.Warn("setup page: the add-on was not installed", "id", id, "err", err)
		}
	})
	return ""
}

func saveAddonUpdate(r *http.Request) string {
	id, problem := addonID(r)
	if problem != "" {
		return problem
	}
	agreed, shown := r.PostFormValue("understood") == "yes", r.PostFormValue("shown")
	for _, s := range addons.Get().Installed() {
		if s.ID == id && s.Update != nil && len(s.Update.More) > 0 && !agreed {
			return "this update asks for more than the version installed: tick the box to say you understand the risk"
		}
	}
	slog.Info("setup page: updating an add-on", "id", id)
	safe.Go("add-on update", func() {
		if err := addons.Get().Update(context.Background(), id, shown, agreed, false); err != nil {
			slog.Warn("setup page: the add-on was not updated", "id", id, "err", err)
		}
	})
	return ""
}

func saveAddonStart(r *http.Request) string {
	id, problem := addonID(r)
	if problem != "" {
		return problem
	}
	return plainErr(addons.Get().Start(id))
}

func saveAddonStop(r *http.Request) string {
	id, problem := addonID(r)
	if problem != "" {
		return problem
	}
	return plainErr(addons.Get().Stop(id))
}

func saveAddonRemove(r *http.Request) string {
	id, problem := addonID(r)
	if problem != "" {
		return problem
	}
	keep := r.PostFormValue("deletestate") != "yes"
	slog.Info("setup page: removing an add-on", "id", id, "keep_state", keep)
	return plainErr(addons.Get().Remove(id, keep))
}

func saveAddonCheck(r *http.Request) string {
	ctx, cancel := context.WithTimeout(r.Context(), catalogWait)
	defer cancel()
	err := addons.Get().Refresh(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return plainErr(err)
	}
	return ""
}

// catalogWait is how long Check the catalog now waits for the catalog before the page comes back.
const catalogWait = 30 * time.Second

// plainErr is an error as the page says it: without the package's prefix.
func plainErr(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimPrefix(err.Error(), "addons: ")
}
