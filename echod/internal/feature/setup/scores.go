package setup

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
)

// The wake word scores: what each word the device listens for is scoring, so a threshold can be picked
// by watching the scores instead of reading the device's log over SSH. The numbers come from the engine
// (wakeword.Scores); nothing here is audio. The engine keeps the last few events it logged, and the
// scores from moment to moment only while a view is watching.

func init() { routes["/setup/scores"] = (*Feature).scores }

// readScores is the device's scores; a test has its own.
var readScores = func(after uint64, from int64, watch bool) wakeword.Scores {
	if wakeword.ReadScores == nil {
		return wakeword.Scores{}
	}
	return wakeword.ReadScores(after, from, watch)
}

// shownEvents is how many of the latest events the section lists.
const shownEvents = 20

// scores answers the live view's poll, for a let-in browser only. It is a read, so it takes no token,
// as the diagnostics do. It is not use of the page either (serve): a view left running in a tab must
// not keep the page open.
func (f *Feature) scores(w http.ResponseWriter, r *http.Request) {
	if _, in := f.session(r); !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "read the scores with GET", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	after, _ := strconv.ParseUint(q.Get("after"), 10, 64)
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	sc := readScores(after, from, true)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(sc)
}

// scoresSection is the words being listened for, their thresholds and what was last heard, drawn as a
// plain table that Refresh brings up to date. With script, Watch polls and draws each word's scores as
// they come. Reading it for the table does not start the engine keeping the peaks: only watching does.
func scoresSection(w http.ResponseWriter) {
	sc := readScores(0, 0, false)
	fmt.Fprint(w, `<fieldset id="scores"><legend>Wake word scores</legend>
	 <style>
	  table.scores{width:100%;border-collapse:collapse;font-variant-numeric:tabular-nums}
	  table.scores th,table.scores td{text-align:left;padding:.3rem .4rem;border-bottom:1px solid var(--line);vertical-align:middle}
	  table.scores th{color:var(--dimtext);font-weight:500;font-size:.85rem}
	  .scbar{position:relative;display:inline-block;width:9rem;height:.9rem;border-radius:999px;background:var(--field);vertical-align:middle;overflow:hidden}
	  .scbar b{position:absolute;left:0;top:0;bottom:0;background:var(--accent)}
	  .scbar b.over{background:var(--ok)}
	  .scbar i{position:absolute;top:0;bottom:0;width:2px;background:var(--text)}
	  ul.scevents{padding-left:1.2rem;margin:.4rem 0;font-variant-numeric:tabular-nums}
	 </style>
	 <p style="margin-top:0" class="note">What each word the device listens for is scoring, so you can pick a threshold by watching
	  instead of reading the device's log. Press <strong>Watch</strong> and say the word a few times from where people
	  say it: the bar is its highest score in the last two seconds, and the line on it is what it has to reach right
	  now. Real tries that peak just under the line mean the threshold can come down a step; something that isn't the
	  word peaking over it means it should go up. The command words rest while nothing plays, so they show up here once
	  music is playing. Scores just after a wake or an unmute, while the device holds detections back, don't count
	  toward the bar. Thresholds are set in Home Assistant or on the device's screen.
	  This is numbers only, never audio, kept only in memory: the last 64 things heard, and the scores from moment to
	  moment only while you watch. Watching doesn't keep this page open.</p>`)

	muted := " hidden"
	if sc.Muted {
		muted = ""
	}
	fmt.Fprintf(w, `<p class="bad" id="scmuted"%s>The microphones are cut, so nothing is being scored.</p>`, muted)
	if len(sc.Rows) == 0 {
		fmt.Fprint(w, `<p id="scnone">Nothing is being listened for right now. The volume and next song words listen only
		 while music plays, and the hang-up word only during a call.</p>`)
	}
	fmt.Fprint(w, `<div style="overflow-x:auto"><table class="scores"><thead><tr><th>Word</th><th>Model</th>
	 <th>In the log</th><th>Threshold</th><th>Judged against</th><th>Lately</th></tr></thead><tbody id="scrows">`)
	for _, row := range sc.Rows {
		scoreRow(w, row)
	}
	fmt.Fprint(w, `</tbody></table></div>`)

	var seen uint64
	for _, ev := range sc.Events {
		seen = max(seen, ev.Seq)
	}
	fmt.Fprintf(w, `<p style="margin-bottom:0"><strong>Last heard</strong></p><ul class="scevents" id="scevents" data-after="%d">`, seen)
	events := sc.Events
	if len(events) > shownEvents {
		events = events[len(events)-shownEvents:]
	}
	for i := len(events) - 1; i >= 0; i-- {
		fmt.Fprintf(w, `<li>%s</li>`, html.EscapeString(eventLine(events[i], sc.Rows)))
	}
	if len(events) == 0 {
		fmt.Fprint(w, `<li id="scquiet" class="note">Nothing yet.</li>`)
	}
	// Refresh differs from the page's own address in more than its fragment, so following it reloads.
	fmt.Fprintf(w, `</ul>
	 <div class="btns"><button type="button" id="scwatch">Watch</button><a href="/setup?tab=sound&amp;scores=%d#scores">Refresh</a>
	  <span id="scsays" class="note"></span></div>`, time.Now().Unix())
	fmt.Fprint(w, scoresScript)
	fmt.Fprint(w, `</fieldset>`)
}

// scoreRow is one word's line in the table. The phrase and the model's id are the model's own text, so
// they are escaped.
func scoreRow(w http.ResponseWriter, row wakeword.ScoreRow) {
	ready := ""
	if !row.Ready {
		ready = ` <span class="note">(starting)</span>`
	}
	judged := fmt.Sprintf("%.2f", row.Cutoff)
	if row.Slack {
		judged += ` <span class="note">(lower while the speaker plays)</span>`
	}
	fmt.Fprintf(w, `<tr data-slot="%d" data-id="%s"><td class="scname">%s%s</td><td class="scmodel">%s <span class="note">%s</span></td><td>slot=%d</td>
	 <td class="scthr">%.2f</td><td class="sccut">%s</td><td class="sclate">%s</td></tr>`,
		row.Slot, html.EscapeString(row.ID), html.EscapeString(row.Role), ready, html.EscapeString(row.Phrase),
		html.EscapeString(row.ID), row.Slot, row.Threshold, judged, lately(row))
}

// lately is a row's highest score in what the read brought, leaving out what was heard while its
// detections were held back, or a dash for none.
func lately(row wakeword.ScoreRow) string {
	best := float32(-1)
	for i, b := range row.Best {
		if i < len(row.Settling) && row.Settling[i] {
			continue
		}
		best = max(best, b)
	}
	if best <= 0 {
		return "&mdash;"
	}
	return fmt.Sprintf("%.2f", best)
}

// eventLine is one event as a person reads it, with the word named as its row is.
func eventLine(ev wakeword.ScoreEvent, rows []wakeword.ScoreRow) string {
	who := fmt.Sprintf("slot=%d", ev.Slot)
	for _, row := range rows {
		if row.Slot == ev.Slot {
			who = row.Role
		}
	}
	at := ev.At.Local().Format("15:04:05")
	switch ev.What {
	case "detected":
		return fmt.Sprintf("%s %s: heard, peak %.2f, crossed %.2f against %.2f", at, who, ev.Peak, ev.Crossing, ev.Cutoff)
	case "near miss":
		return fmt.Sprintf("%s %s: near miss, peak %.2f against %.2f", at, who, ev.Peak, ev.Cutoff)
	case "unsure":
		return fmt.Sprintf("%s %s: unsure, peak %.2f, over the confirm threshold %.2f and under the %.2f that wakes", at,
			who, ev.Peak, ev.AskedFrom, ev.Cutoff)
	case "ready":
		return fmt.Sprintf("%s %s: %s ready", at, who, ev.ID)
	case "dropped":
		return fmt.Sprintf("%s %s: %s taken out, its model failed", at, who, ev.ID)
	}
	return fmt.Sprintf("%s %s: %s", at, who, ev.What)
}

// scoresScript polls while Watch is on and draws what comes back: each row's bar from the last two
// seconds of peaks, and the events as they arrive. Everything that came from a model is put in as text.
// Each Watch is a poll loop of its own, numbered, so a loop from before a Stop finds itself stale and
// ends instead of running beside the new one.
const scoresScript = `<script>
(() => {
  const btn = document.getElementById('scwatch'), says = document.getElementById('scsays');
  const rowsEl = document.getElementById('scrows'), evEl = document.getElementById('scevents');
  const mutedEl = document.getElementById('scmuted');
  let gen = 0, on = false, timer = null, failed = 0;
  let after = Number(evEl.dataset.after || 0), from = 0, peaks = {}, roles = {};
  const window2s = 8;
  btn.addEventListener('click', () => { if (on) stop('Stopped.'); else start(); });
  function start() {
    on = true; failed = 0; btn.textContent = 'Stop watching'; says.textContent = 'Watching...';
    poll(++gen);
  }
  function stop(why) {
    on = false; gen++; if (timer) clearTimeout(timer); timer = null;
    btn.textContent = 'Watch'; says.textContent = why;
  }
  function again(g, ms) { if (on && g === gen) timer = setTimeout(() => poll(g), ms); }
  async function poll(g) {
    let s;
    try {
      const r = await fetch('/setup/scores?after=' + after + '&from=' + from, {cache: 'no-store'});
      if (!on || g !== gen) return;
      if (r.status === 403) { stop('This browser is no longer let in. Reload the page to ask again.'); return; }
      if (r.status === 404) { stop('The setup page has closed.'); return; }
      if (!r.ok) throw new Error(String(r.status));
      s = await r.json();
    } catch (e) {
      if (!on || g !== gen) return;
      if (++failed >= 5) { stop('The device stopped answering. The setup page may have closed.'); return; }
      says.textContent = 'Could not read the scores. Trying again.';
      again(g, 2000);
      return;
    }
    if (!on || g !== gen) return;
    failed = 0;
    draw(s);
    says.textContent = 'Watching...';
    again(g, 500);
  }
  function cell(tr, cls) { return tr.querySelector('.' + cls); }
  function rowFor(row) {
    let tr = rowsEl.querySelector('tr[data-slot="' + row.slot + '"]');
    if (tr) return tr;
    tr = document.createElement('tr');
    tr.dataset.slot = row.slot;
    for (const cls of ['scname', 'scmodel', 'sclog', 'scthr', 'sccut', 'sclate']) {
      const td = document.createElement('td'); td.className = cls; tr.append(td);
    }
    cell(tr, 'sclog').textContent = 'slot=' + row.slot;
    rowsEl.append(tr);
    const none = document.getElementById('scnone'); if (none) none.remove();
    return tr;
  }
  function draw(s) {
    if (s.lost) peaks = {};
    mutedEl.hidden = !s.muted;
    const seen = new Set();
    for (const row of (s.rows || [])) {
      seen.add(String(row.slot));
      roles[row.slot] = row.role;
      const tr = rowFor(row);
      if (tr.dataset.id !== row.id) { peaks[row.slot] = {}; tr.dataset.id = row.id; }
      cell(tr, 'scname').textContent = row.role + (row.ready ? '' : ' (starting)');
      cell(tr, 'scmodel').textContent = row.phrase + ' (' + row.id + ')';
      cell(tr, 'scthr').textContent = row.threshold.toFixed(2);
      cell(tr, 'sccut').textContent = row.cutoff.toFixed(2) + (row.slack ? ' (lower while the speaker plays)' : '');
      const p = peaks[row.slot] || (peaks[row.slot] = {});
      (row.best || []).forEach((b, i) => { p[s.from + i] = {b: b, held: !!(row.settling || [])[i]}; });
      for (const k of Object.keys(p)) if (k < s.bucket - 120) delete p[k];
      let best = 0, held = 0;
      for (let k = s.bucket - window2s + 1; k <= s.bucket; k++) {
        const v = p[k]; if (!v) continue;
        if (v.held) held = Math.max(held, v.b); else best = Math.max(best, v.b);
      }
      const late = cell(tr, 'sclate');
      late.textContent = '';
      const bar = document.createElement('span'); bar.className = 'scbar';
      const fill = document.createElement('b'); fill.style.width = (best * 100).toFixed(1) + '%';
      if (best >= row.cutoff) fill.className = 'over';
      const line = document.createElement('i'); line.style.left = (row.cutoff * 100).toFixed(1) + '%';
      bar.append(fill, line);
      late.append(bar, ' ' + best.toFixed(2) + (held > best ? ' (held back ' + held.toFixed(2) + ')' : ''));
    }
    for (const tr of rowsEl.querySelectorAll('tr[data-slot]')) {
      if (!seen.has(tr.dataset.slot)) { const late = cell(tr, 'sclate'); if (late) late.textContent = 'not listening now'; }
    }
    from = s.bucket;
    for (const ev of (s.events || [])) {
      after = Math.max(after, ev.seq);
      const quiet = document.getElementById('scquiet'); if (quiet) quiet.remove();
      const li = document.createElement('li');
      li.textContent = line(ev);
      evEl.prepend(li);
      while (evEl.children.length > 20) evEl.lastElementChild.remove();
    }
  }
  function line(ev) {
    const who = roles[ev.slot] || ('slot=' + ev.slot);
    const at = new Date(ev.at).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false});
    const n = (v) => (v || 0).toFixed(2);
    switch (ev.what) {
      case 'detected': return at + ' ' + who + ': heard, peak ' + n(ev.peak) + ', crossed ' + n(ev.crossing) + ' against ' + n(ev.cutoff);
      case 'near miss': return at + ' ' + who + ': near miss, peak ' + n(ev.peak) + ' against ' + n(ev.cutoff);
      case 'unsure': return at + ' ' + who + ': unsure, peak ' + n(ev.peak) + ', over the confirm threshold ' + n(ev.asked_from) + ' and under the ' + n(ev.cutoff) + ' that wakes';
      case 'ready': return at + ' ' + who + ': ' + ev.id + ' ready';
      case 'dropped': return at + ' ' + who + ': ' + ev.id + ' taken out, its model failed';
    }
    return at + ' ' + who + ': ' + ev.what;
  }
})();
</script>`
