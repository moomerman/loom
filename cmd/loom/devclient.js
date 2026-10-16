// The loom dev overlay: remounts the page when the app is rebuilt (a
// reload if it isn't a live page, or its root layout changed), keeping
// form input, and shows build errors over the page until they are fixed.
(function () {
  "use strict";
  var box;
  function show(e) {
    if (!box) {
      box = document.createElement("div");
      box.id = "loom-dev-overlay";
      box.style.cssText = "position:fixed;inset:0;z-index:2147483647;overflow:auto;background:rgba(20,20,20,.94);color:#eee;font:13px/1.5 ui-monospace,Menlo,monospace;padding:2rem";
      document.body.appendChild(box);
    }
    box.innerHTML = "";
    var h = document.createElement("div");
    h.style.cssText = "color:#ff6b6b;font-weight:bold;margin-bottom:1rem";
    h.textContent = "loom dev: " + e.kind + " error. Fix it and save; this page reloads.";
    var pre = document.createElement("pre");
    pre.style.cssText = "white-space:pre-wrap;margin:0";
    pre.textContent = e.output;
    box.appendChild(h);
    box.appendChild(pre);
  }
  var es = new EventSource("/_loom/dev/reload");
  es.addEventListener("failed", function (m) { show(JSON.parse(m.data)); });
  es.addEventListener("ready", function () {
    var root = document.getElementById("loom-root");
    if (box) box.remove(), box = null;
    root && root.loomRemount ? root.loomRemount() : location.reload();
  });
})();
