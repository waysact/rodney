package main

import (
	"net/http"
	"strings"
)

// fixtures holds the HTML pages that the viewport, capture and inject tests
// load. Each page is served at /fixture/<name>.
var fixtures = map[string]string{
	"plain": `<!DOCTYPE html><html><head><title>Plain</title></head><body><p>plain</p></body></html>`,
	"inject-probe": `<!DOCTYPE html><html><head><title>Probe</title>
<script>document.title = window.presenterLoaded ? "seen" : "missing";</script>
</head><body></body></html>`,
	"iframe": `<!DOCTYPE html><html><head><title>Iframe</title></head><body>
<iframe id="child" srcdoc="<p>child</p>"></iframe>
</body></html>`,
	"throws": `<!DOCTYPE html><html><head><title>Throws</title></head><body></body></html>`,
	"resize-counter": `<!DOCTYPE html><html><head><title>Resize counter</title></head><body>
<script>window.resizes = 0; addEventListener("resize", () => { window.resizes++; });</script>
</body></html>`,
	"tall": `<!DOCTYPE html><html><head><title>Tall</title><style>
body { margin: 0; }
#fixed { position: fixed; left: 10px; top: 10px; width: 50px; height: 50px; background: #ff0000; }
#content { height: 3000px; }
</style></head><body><div id="fixed"></div><div id="content"></div></body></html>`,
	"reposition": `<!DOCTYPE html><html><head><title>Reposition</title><style>
body { margin: 0; }
#box { position: absolute; left: 0; top: 0; width: 40px; height: 40px; background: #ff0000; }
</style></head><body><div id="box"></div>
<script>
addEventListener("resize", () => requestAnimationFrame(() => {
	document.getElementById("box").style.left = (innerWidth - 40) + "px";
}));
</script></body></html>`,
	"elements": `<!DOCTYPE html><html><head><title>Elements</title><style>
body { margin: 0; height: 2000px; }
div { position: absolute; background: #ff0000; }
#target { left: 100px; top: 100px; width: 50px; height: 40px; }
.extra-a { left: 300px; top: 100px; width: 20px; height: 20px; }
.extra-b { left: 100px; top: 300px; width: 30px; height: 30px; }
.hidden { display: none; }
.invisible { left: 400px; top: 100px; width: 20px; height: 20px; visibility: hidden; }
#edge { left: 0; top: 0; width: 10px; height: 10px; }
#far { left: 100px; top: 1500px; width: 50px; height: 50px; }
</style></head><body>
<div id="target"></div><div class="extra extra-a"></div><div class="extra extra-b"></div>
<div class="hidden"></div><div class="invisible"></div><div id="edge"></div><div id="far"></div>
</body></html>`,
	"big": `<!DOCTYPE html><html><head><title>Big</title><style>
body { margin: 0; }
#big { position: absolute; left: 0; top: 0; width: 600px; height: 500px; background: #ff0000; }
</style></head><body><div id="big"></div></body></html>`,
	"record-reload": `<!DOCTYPE html><html><body style="margin:0;background:green"><script>
if (location.search) document.body.style.background = window.presenterLoaded ? 'red' : 'blue';
else setTimeout(() => location.search = 'reloaded', 2500);
</script></body></html>`,
	"record-green": `<!DOCTYPE html><html><body style="margin:0;background:green"></body></html>`,
	"record-blue":  `<!DOCTYPE html><html><body style="margin:0;background:blue"></body></html>`,
	"keyboard": `<!DOCTYPE html><html><body>
<input id="edit" value=""><input id="other" value=""><input id="disabled" disabled>
<input id="readonly" readonly><textarea id="area"></textarea><div id="rich" contenteditable="true"></div>
<div id="plain-focus" tabindex="0"></div><form id="enter-form"><input id="form-input"></form>
<script>
window.keyEvents = [];
window.submits = 0;
document.querySelector('#enter-form').addEventListener('submit', e => { e.preventDefault(); submits++; });
for (const type of ['keydown','keyup','beforeinput','input','change']) {
 document.addEventListener(type, e => keyEvents.push({type, key:e.key || '', shift:e.shiftKey || false, ctrl:e.ctrlKey || false, meta:e.metaKey || false, value:e.target.value || '', time:performance.now()}));
}
</script></body></html>`,
	"mouse": `<!DOCTYPE html><html><head><style>
body { margin:0; height:2200px; }
#target { position:absolute; left:100px; top:100px; width:50px; height:40px; background:red; }
#far { position:absolute; left:100px; top:1500px; width:50px; height:40px; background:blue; }
#covered { position:absolute; left:300px; top:100px; width:50px; height:40px; background:green; }
#overlay { position:absolute; left:300px; top:100px; width:50px; height:40px; background:orange; }
#source { position:absolute; left:100px; top:400px; width:80px; height:80px; background:purple; }
#drop { position:absolute; left:400px; top:400px; width:100px; height:100px; background:yellow; }
#nav { position:absolute; left:550px; top:100px; }
#close { position:absolute; left:700px; top:100px; }
</style></head><body><div id="target"></div><div id="far"></div><div id="covered"></div><div id="overlay"></div>
<div id="source" draggable="true">drag</div><div id="drop">drop here</div><a id="nav" href="/fixture/plain">navigate</a><button id="close" onclick="window.close()">close</button>
<script>
window.mouseEvents=[]; window.dragEvents=[]; window.clickEvents=[];
addEventListener('mousemove', e => mouseEvents.push({x:e.clientX,y:e.clientY,time:performance.now(),buttons:e.buttons}));
for (const type of ['click','auxclick','contextmenu']) addEventListener(type,e => clickEvents.push({type,button:e.button,x:e.clientX,y:e.clientY}));
for (const type of ['dragstart','dragenter','dragover','drop','dragend']) addEventListener(type,e => dragEvents.push(type));
document.querySelector('#source').addEventListener('dragstart',e => e.dataTransfer.setData('text/plain','dragged'));
document.querySelector('#drop').addEventListener('dragover',e => e.preventDefault());
document.querySelector('#drop').addEventListener('drop',e => { e.preventDefault(); e.target.textContent=e.dataTransfer.getData('text/plain'); });
</script>
</body></html>`,
	"mouse-parent": `<!DOCTYPE html><html><head><title>Mouse parent</title></head><body><a id="open" target="_blank" href="/fixture/mouse">open child</a></body></html>`,
}

func handleFixture(w http.ResponseWriter, r *http.Request) {
	body, ok := fixtures[strings.TrimPrefix(r.URL.Path, "/fixture/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(body))
}
