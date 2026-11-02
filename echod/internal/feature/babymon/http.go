package babymon

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
)

// The pages, on the device's web port. Both answer as if not there unless the switch is on and the
// address carries the link's key (?k=), and neither is in the port's index.
const (
	PagePath  = "/baby"
	AudioPath = "/baby/audio"
)

// writeWait is how long a browser gets to take a packet: one that stops reading is let go, rather than
// holding the microphones' frames up.
const writeWait = time.Second

func (f *Feature) handle() {
	on := func() bool { return f.dev.switchedOn() }
	web.Handle(PagePath, "", on, f.page)
	web.Handle(AudioPath, "", on, f.audio)
}

// admit is whether a request may have a page: the switch is on and the key is the link's. Anything else
// is answered as a page that is not there, so a wrong key learns nothing.
func (f *Feature) admit(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet || !f.dev.switchedOn() || !f.KeyOK(r.URL.Query().Get("k")) {
		web.Refuse(w, "404 page not found", http.StatusNotFound)
		return false
	}
	return true
}

func (f *Feature) page(w http.ResponseWriter, r *http.Request) {
	if !f.admit(w, r) {
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self' ws: wss:")
	_, _ = w.Write([]byte(pageHTML))
}

func (f *Feature) audio(w http.ResponseWriter, r *http.Request) {
	if !f.admit(w, r) {
		return
	}
	if err := f.refused(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if f.Count() >= Max {
		http.Error(w, ErrFull.Error(), http.StatusServiceUnavailable)
		return
	}
	// The default origin check: a page on another site cannot open this one.
	up := websocket.Upgrader{ReadBufferSize: 512, WriteBufferSize: 4096}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Nothing is wanted from the browser; reading is how its closing is heard.
	conn.SetReadLimit(512)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	err = f.Serve(done, func(b []byte) error {
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(websocket.BinaryMessage, b)
	})
	slog.Info("baby monitor: a listener left", "why", reason(err))
	msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason(err))
	_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(writeWait))
}

// reason is what a browser is told when its listening ends.
func reason(err error) string {
	if errors.Is(err, ErrOff) || errors.Is(err, ErrMuted) || errors.Is(err, ErrFull) || errors.Is(err, ErrNewKey) {
		return err.Error()
	}
	return "Stopped"
}

// pageHTML is the page: one button, since a browser starts sound only from a tap. The key is read from
// the address it was opened with, so the page itself holds nothing secret. RATE is mic.Rate.
const pageHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Baby monitor</title>
<style>
body{font-family:system-ui,sans-serif;background:#111;color:#eee;margin:0;padding:24px 16px;text-align:center}
button{font:inherit;font-size:1.3em;padding:.8em 1.6em;border:0;border-radius:.6em;background:#2d7ff9;color:#fff}
#s{margin:1.2em 0;min-height:1.5em}
p{color:#aaa;font-size:.9em;max-width:28em;margin:1em auto}
</style></head><body>
<h1>Baby monitor</h1>
<button id="go">Tap to listen</button>
<div id="s" role="status"></div>
<p>The device shows that someone is listening. On an iPhone, turn the ringer switch on and the volume up.</p>
<script>
var RATE=16000,ctx,ws,next=0,lock,want=false,retry;
var go=document.getElementById('go'),st=document.getElementById('s');
go.onclick=function(){
  var AC=window.AudioContext||window.webkitAudioContext;
  if(!ctx)ctx=new AC();
  ctx.resume();
  want=true;
  try{if(navigator.wakeLock)navigator.wakeLock.request('screen').then(function(l){lock=l},function(){});}catch(e){}
  start();
};
function start(){
  clearTimeout(retry);
  if(ws){ws.onclose=null;ws.close();}
  var k=new URLSearchParams(location.search).get('k')||'';
  ws=new WebSocket((location.protocol==='https:'?'wss://':'ws://')+location.host+'/baby/audio?k='+encodeURIComponent(k));
  ws.binaryType='arraybuffer';
  next=0;
  st.textContent='Connecting...';
  go.hidden=true;
  ws.onmessage=function(e){if(typeof e.data!=='string'){st.textContent='Listening';play(e.data);}};
  ws.onclose=function(e){
    st.textContent=(e.reason||'Not connected')+(want?', trying again':'');
    go.textContent='Tap to listen again';go.hidden=false;
    if(want&&document.visibilityState==='visible')retry=setTimeout(start,3000);
  };
}
// Back on the page after the phone slept or the tab was left: sound on again, and the stream back.
document.addEventListener('visibilitychange',function(){
  if(document.visibilityState!=='visible'||!want)return;
  if(ctx)ctx.resume();
  if(!ws||ws.readyState>1)start();
});
function play(buf){
  var s=new Int16Array(buf),f=new Float32Array(s.length);
  for(var i=0;i<s.length;i++)f[i]=s[i]/32768;
  var b=ctx.createBuffer(1,f.length,RATE);
  b.getChannelData(0).set(f);
  var src=ctx.createBufferSource();
  src.buffer=b;src.connect(ctx.destination);
  var now=ctx.currentTime;
  if(next<now+0.05)next=now+0.2;
  else if(next>now+0.9)return;
  src.start(next);next+=b.duration;
}
</script></body></html>`
