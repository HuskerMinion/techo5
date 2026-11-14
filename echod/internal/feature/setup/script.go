package setup

import (
	"html"
	"io"
)

// The page's one script of its own, written once at the foot of a page. Nothing on the page needs it:
// every form posts and comes back the same without it, which is what a browser with script turned off
// gets.

// busy marks a form whose answer takes a while, with what to say beside its button while it is on its
// way: joining a network holds the request for up to a minute, and a page that only sat there got its
// button pressed again. It is an attribute on the form's own tag and nothing else, so the form is the
// same form without the script.
func busy(say string) string {
	return ` data-busy="` + html.EscapeString(say) + `"`
}

// What the forms that take a while say.
const (
	busyWifi      = "Joining, up to a minute…"
	busyTailscale = "Joining, up to half a minute…"
	busyNetbird   = "Joining, up to two minutes…"
	busySound     = "Sending…"
	busyPack      = "Loading the pack…"
	busyInstall   = "Starting the install…"
	busyCheck     = "Checking, up to half a minute…"
)

// pageScript is the script. On the submit of a form marked busy it puts the form's words beside the
// button that was pressed, and then turns that button off: after the form's fields have been gathered,
// since a button turned off before then would not send its own name with them. A page come back to by
// the browser's Back button is shown as it was left, so the buttons are turned on again there.
//
// It also gives the Show box beside a secret (secret.go) its use: ticked, the field is shown as it is
// typed. The boxes are drawn hidden and brought out here, so there is none where it would do nothing.
// A field being shown is put back before its form is sent, so that the browser, which remembers what
// was typed into an ordinary field to offer it again, is not handed a password as one.
func pageScript(w io.Writer) {
	_, _ = io.WriteString(w, `<script>
function secretOf(c){return document.getElementById(c.getAttribute('data-show'))}
document.querySelectorAll('input[data-show]').forEach(function(c){
 var i=secretOf(c);if(!i)return;
 c.checked=false;c.parentNode.hidden=false;
 c.addEventListener('change',function(){i.type=c.checked?'text':'password'});
});
document.addEventListener('submit',function(e){
 var f=e.target;
 if(f.querySelectorAll)f.querySelectorAll('input[data-show]').forEach(function(c){
  var i=secretOf(c);if(i)i.type='password';c.checked=false;
 });
 var say=f.getAttribute&&f.getAttribute('data-busy');
 if(!say||e.defaultPrevented)return;
 var b=e.submitter||f.querySelector('button[type=submit]');
 if(!b||b.disabled)return;
 var s=document.createElement('span');
 s.className='note busy';s.setAttribute('role','status');s.textContent=say;
 b.insertAdjacentElement('afterend',s);
 setTimeout(function(){b.disabled=true},0);
});
window.addEventListener('pageshow',function(){
 document.querySelectorAll('span.busy').forEach(function(s){
  var b=s.previousElementSibling;if(b)b.disabled=false;s.remove();
 });
});
</script>`)
}
