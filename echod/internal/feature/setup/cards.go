package setup

import (
	"fmt"
	"html"
	"io"
	"strconv"
)

// What goes around a tab's cards (section.go): the list of them under the tab in the rail, the links
// that open or shut them all, and the small script that keeps open what a browser left open.

// toc is the current tab's sections, under the tab in the rail, each a link that opens its card and
// goes to it. It is made from the cards the tab drew, in the order it drew them, once each.
func toc(w io.Writer, tab string, cards []card) {
	if len(cards) == 0 {
		return
	}
	seen := map[string]bool{}
	fmt.Fprint(w, `<ul class="toc">`)
	for _, c := range cards {
		if seen[c.name] {
			continue
		}
		seen[c.name] = true
		fmt.Fprintf(w, `<li><a href="/setup?tab=%s&amp;open=%s#sec-%s">%s</a></li>`, tab, c.name, c.name, html.EscapeString(c.title))
	}
	fmt.Fprint(w, `</ul>`)
}

// cardBar is the links that open every card on the tab, or shut them all and forget the ones this
// browser kept open (open=none, which the script reads).
func cardBar(w io.Writer, tab string) {
	fmt.Fprintf(w, `<p class="cardbar"><a href="/setup?tab=%s&amp;open=all">Open all</a><a href="/setup?tab=%s&amp;open=none">Close all</a></p>`,
		tab, tab)
}

// cardScript keeps, in this browser only, which cards were opened or shut on the tab by hand, and opens
// them again the next time the tab is drawn. It only ever opens: a card the page drew open stays
// open. A card the address goes to (#sec-name) is opened too. Without the script, or without storage,
// the cards are as the page drew them. The tab is one of the page's own names (tabOf).
func cardScript(w io.Writer, tab string) {
	fmt.Fprintf(w, `<script>(function(){var k=%s;
function get(){try{var a=JSON.parse(localStorage.getItem(k)||'[]');return Array.isArray(a)?a:[]}catch(e){return []}}
function put(a){try{if(a.length)localStorage.setItem(k,JSON.stringify(a));else localStorage.removeItem(k)}catch(e){}}
var cards=document.querySelectorAll('details.card'),name=function(d){return d.id.slice(4)};
if(/[?&]open=none(&|$)/.test(location.search)){put([]);
 try{history.replaceState(null,'',location.pathname+location.search.replace(/([?&])open=none(&|$)/,'$1').replace(/[?&]$/,'')+location.hash)}catch(e){}}
else{var o=get();cards.forEach(function(d){if(o.indexOf(name(d))>=0)d.open=true})}
var t=location.hash&&document.getElementById(location.hash.slice(1));if(t&&t.matches('details.card'))t.open=true;
cards.forEach(function(d){var s=d.querySelector('summary');if(s)s.addEventListener('click',function(){setTimeout(function(){
 var a=get().filter(function(n){return n!==name(d)});if(d.open)a.push(name(d));put(a)},0)})});
})();</script>`, strconv.Quote("techo5-open-"+tab))
}
