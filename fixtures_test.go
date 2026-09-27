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
