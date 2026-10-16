
// dev.js: appended to loom.js in dev mode only (App.Dev). It reports the
// page's script errors to the server, so `loom errors` and the
// recent_errors MCP tool show what only the browser's console would:
// uncaught errors, rejected promises and console.error calls, such as
// RT417 and RT418 from behaviors. A message that starts with a code ("RT417: ...") keeps
// it; text after "; " is the fix.
(function () {
  "use strict";
  var sent = 0, since = Date.now();
  function text(v) {
    if (v instanceof Error) return v.stack || v.message;
    if (v instanceof Element) return "<" + v.tagName.toLowerCase() + (v.id ? "#" + v.id : "") + ">";
    if (typeof v === "string") return v;
    try { return JSON.stringify(v); } catch (e) { return String(v); }
  }
  function report(args, where) {
    var code = "";
    for (var i = 0; i < args.length; i++) if (args[i] && args[i].code) code = args[i].code;
    send(code, Array.prototype.map.call(args, text).join(" "), where);
  }
  function send(code, msg, where) {
    if (Date.now() - since > 10000) sent = 0, since = Date.now();
    if (++sent > 20) return; // a failing loop shouldn't flood the server
    var fix = "";
    var m = /^([A-Z]{2}\d{3}):\s*([\s\S]*)$/.exec(msg);
    if (m) {
      code = m[1], msg = m[2];
      var j = msg.indexOf("; ");
      if (j > 0) fix = msg.slice(j + 2), msg = msg.slice(0, j);
    }
    var root = document.getElementById("loom-root");
    fetch("/_loom/dev/errors", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sid: root ? root.getAttribute("data-sid") : "", url: location.href, code: code, message: msg, where: where || "", fix: fix })
    }).catch(function () {});
  }
  // Other scripts report errors with a code through this.
  window.loomReportError = function (code, msg) { send(code || "", msg); };
  var consoleError = console.error;
  console.error = function () {
    consoleError.apply(console, arguments);
    report(arguments);
  };
  addEventListener("error", function (e) {
    report([e.error || e.message], e.filename ? e.filename + ":" + e.lineno + ":" + e.colno : "");
  });
  addEventListener("unhandledrejection", function (e) { report([e.reason]); });
})();
