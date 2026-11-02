package setup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/android/logd"
	"github.com/HuskerMinion/techo5/echod/internal/feature/diag"
)

// The log view: the last of the daemon's log, then each line as it is written, in the browser. It is
// the same log as the diagnostics download has, with the same parts taken out of it (diag.Redactor)
// before a line leaves the device, and it is behind the same press: nothing is served to a browser that
// has not been let in, and a stream ends when its browser stops being let in.
//
// The lines come from logd's live log, a small ring in memory. The log file is not read. Following costs
// the logger nothing: a follower that falls behind loses its oldest lines (logd.Follower) and is told.

func init() {
	routes["/setup/log"] = (*Feature).logPage
	routes["/setup/log/stream"] = (*Feature).logStream
}

const (
	// maxLogViewers is how many browsers can follow the log at once: one watching, one spare.
	maxLogViewers = 2

	// logBacklog is how many lines a stream starts with.
	logBacklog = 300

	// logStreamFor is the longest one stream runs; the browser asks again and carries on from the last
	// line it had.
	logStreamFor = 30 * time.Minute

	logKeepAlive    = 15 * time.Second
	logCheckEvery   = 2 * time.Second
	logWriteTimeout = 10 * time.Second

	// logRedactEvery is how often a stream learns again what to take out.
	logRedactEvery = time.Minute
)

var logViewers atomic.Int32

// newLogRedactor takes the private parts out of a line; a variable for the tests.
var newLogRedactor = func() func(string) string { return diag.Redactor().Text }

// logEntry is one line as the browser gets it.
type logEntry struct {
	N uint64 `json:"n"`
	L string `json:"l"` // I, W or E
	T string `json:"t"`
}

func (f *Feature) logPage(w http.ResponseWriter, r *http.Request) {
	if _, in := f.session(r); !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	head(w)
	fmt.Fprint(w, logPageHTML)
}

// logStream is the log as server-sent events: the last lines first, then each as it comes. A browser that
// reconnects says the last line it had (Last-Event-ID) and gets only what came after.
func (f *Feature) logStream(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "follow the log with GET", http.StatusMethodNotAllowed)
		return
	}
	flush, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "this connection cannot stream", http.StatusInternalServerError)
		return
	}
	if logViewers.Add(1) > maxLogViewers {
		logViewers.Add(-1)
		http.Error(w, "the log is being followed twice already", http.StatusTooManyRequests)
		return
	}
	defer logViewers.Add(-1)

	redact, redactedAt := newLogRedactor(), time.Now()
	backlog, follow := logd.Live(logBacklog)
	defer follow.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	// A reader that stops reading ends this stream instead of holding it: the logger never waits on it
	// (the follower drops), so the cost of a stuck browser is one goroutine until the deadline.
	rc := http.NewResponseController(w)
	write := func(s string) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(logWriteTimeout))
		_, err := fmt.Fprint(w, s)
		return err == nil
	}
	send := func(e logd.Entry) bool {
		b, _ := json.Marshal(logEntry{N: e.Seq, L: logd.Letter(e.Level), T: redact(e.Text)})
		return write("id: " + strconv.FormatUint(e.Seq, 10) + "\ndata: " + string(b) + "\n\n")
	}

	// The daemon starting again counts from 1, so an id from the future is from the last run: start over.
	var after uint64
	if id, err := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64); err == nil {
		if len(backlog) == 0 || id <= backlog[len(backlog)-1].Seq {
			after = id
		}
	}
	for _, e := range backlog {
		if e.Seq > after && !send(e) {
			return
		}
	}
	flush.Flush()

	end := time.After(logStreamFor)
	tick := time.NewTicker(logCheckEvery)
	defer tick.Stop()
	quietSince := time.Now()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-end:
			write("event: end\ndata: the stream ran its course\n\n")
			flush.Flush()
			return
		case e := <-follow.C:
			if e.Seq <= after {
				continue
			}
			if n := follow.Lost(); n > 0 {
				if !write("event: lost\ndata: " + strconv.FormatUint(n, 10) + "\n\n") {
					return
				}
			}
			if time.Since(redactedAt) >= logRedactEvery {
				redact, redactedAt = newLogRedactor(), time.Now()
			}
			if !send(e) {
				return
			}
			quietSince = time.Now()
			// Flush once the burst is out rather than after every line.
			if len(follow.C) == 0 {
				flush.Flush()
			}
		case <-tick.C:
			// The page closed, or this browser's session ended: stop giving it the log.
			if !f.letIn(token) {
				return
			}
			if time.Since(quietSince) >= logKeepAlive {
				if !write(": still here\n\n") {
					return
				}
				quietSince = time.Now()
				flush.Flush()
			}
		}
	}
}

// logPageHTML is the view. The script only ever sets textContent, so nothing in the log can become markup.
const logPageHTML = `<div class="wrap"><h1>Log</h1>
<p class="sub"><a href="/setup?tab=general">Back to setup</a></p>
<style>
 .logbar{display:flex;gap:.6rem;flex-wrap:wrap;align-items:center;margin:0 0 .6rem}
 .logbar select,.logbar input{width:auto}
 .logbar input[type=search]{flex:1;min-width:8rem}
 #logbox{height:60vh;overflow:auto;border:1px solid var(--line);border-radius:10px;background:var(--drawer);padding:.5rem;
  font:.8rem/1.35 ui-monospace,Menlo,Consolas,monospace}
 #logbox div{white-space:pre-wrap;overflow-wrap:anywhere}
 #logbox .W{color:var(--bad);opacity:.8} #logbox .E{color:var(--bad);font-weight:600}
</style>
<div class="logbar">
 <button type="button" id="logfollow" class="quiet">Pause</button>
 <label for="loglevel" class="note" style="margin:0">Show</label>
 <select id="loglevel"><option value="I">Info and up</option><option value="W">Warnings and up</option><option value="E">Errors only</option></select>
 <input id="logfind" type="search" placeholder="Filter text" aria-label="Filter text">
 <button type="button" id="logsave" class="quiet">Download</button>
 <span id="logstate" class="note"></span>
</div>
<div id="logbox" role="log" aria-live="off"></div>
<p class="note">The last lines of the device's log, then each new one as it is written. Addresses, network names, keys and
 serial numbers are replaced before a line reaches this page, as in the diagnostics download. Names of rooms and
 devices can still show, so look before you share it. Download saves the lines shown, with the filter applied.
 This view needs script, and watching does not keep the page open.</p>
<script>
(()=>{
 const box=document.getElementById('logbox'),state=document.getElementById('logstate'),
  btn=document.getElementById('logfollow'),lvl=document.getElementById('loglevel'),find=document.getElementById('logfind');
 const rank={I:0,W:1,E:2},keep=1000;
 let lines=[],follow=true,held=[];
 const shown=l=>rank[l.l]>=rank[lvl.value]&&(!find.value||l.t.toLowerCase().includes(find.value.toLowerCase()));
 const text=l=>l.l+' '+l.t;
 const row=l=>{const d=document.createElement('div');d.className=l.l;d.textContent=text(l);return d};
 function draw(){
  box.textContent='';
  const f=document.createDocumentFragment();
  for(const l of lines)if(shown(l))f.appendChild(row(l));
  box.appendChild(f);
  if(follow)box.scrollTop=box.scrollHeight;
 }
 function add(l){
  lines.push(l);
  if(lines.length>keep){lines.shift();draw();return}
  if(shown(l)){box.appendChild(row(l));if(follow)box.scrollTop=box.scrollHeight}
 }
 btn.onclick=()=>{follow=!follow;btn.textContent=follow?'Pause':'Follow';if(follow){for(const l of held)add(l);held=[];box.scrollTop=box.scrollHeight}};
 lvl.onchange=find.oninput=draw;
 document.getElementById('logsave').onclick=()=>{
  const body=lines.filter(shown).map(text).join('\n')+'\n';
  const a=document.createElement('a');
  a.href=URL.createObjectURL(new Blob([body],{type:'text/plain'}));
  a.download='techo5-log.txt';document.body.appendChild(a);a.click();a.remove();
 };
 const es=new EventSource('/setup/log/stream');
 es.onopen=()=>{state.textContent='Following'};
 es.onmessage=e=>{try{const l=JSON.parse(e.data);if(follow)add(l);else{held.push(l);if(held.length>keep)held.shift()}}catch(x){}};
 es.addEventListener('lost',e=>{state.textContent=e.data+' lines were missed'});
 es.addEventListener('end',()=>{state.textContent='Stream ended; reload to follow again';es.close()});
 es.onerror=()=>{state.textContent=es.readyState===2?'Not following (locked or too many viewers)':'Reconnecting...'};
})();
</script></div>`
