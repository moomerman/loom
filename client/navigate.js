// navigate.js: live navigation for Loom pages (docs/layouts.md).
//
// A click on a link within the site, without a modifier key or a target,
// navigates live, as does a GET form within the site, and so do the back and forward buttons and a page's
// c.Navigate: it fetches the page with the X-Loom-Navigate header, naming
// the session it leaves. The server answers "swap" for a page with the
// same root layout, and this swaps in its live root, title, stylesheet
// and scripts, keeping the rest of the document; loom.js takes over the
// new page's session. A URL of the same path with another query, on a
// page with HandleParams, the server answers "patch": the page stays,
// its session has run HandleParams and streams the diff, and only the
// URL changes. Anything else (a page with another root layout, a path
// that isn't a page, a server that can't be reached) the browser loads
// as an ordinary navigation.
(function () {
  "use strict";
  var root = document.getElementById("loom-root");
  if (!root || !root.loomSwap) return;
  var here = location.pathname + location.search, nav = 0;

  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest("a[href]");
    if (!a || e.defaultPrevented || e.button || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey ||
      a.target || a.hasAttribute("download") || a.origin !== location.origin ||
      (a.href.indexOf("#") >= 0 && a.pathname + a.search === here)) return; // a link within the page
    e.preventDefault();
    go(a.href, true);
  });

  // A GET form within the site (a search box) is a link whose query the
  // user types: it navigates live too. A form with on:submit is loom.js's.
  document.addEventListener("submit", function (e) {
    var f = e.target, s = e.submitter, attr = function (n) { return s && s.getAttribute(n); };
    var url = new URL(attr("formaction") || f.action, location.href);
    if (e.defaultPrevented || (attr("formmethod") || f.method).toLowerCase() !== "get" ||
      attr("formtarget") || f.target || url.origin !== location.origin) return;
    e.preventDefault();
    url.search = new URLSearchParams(new FormData(f, s)).toString();
    go(url.href, true);
  });

  addEventListener("popstate", function () {
    if (location.pathname + location.search !== here) go(location.href, false);
  });

  root.loomGo = function (url) { go(new URL(url, location.href).href, true); };

  // A c.Patch changed the URL (loom.js).
  document.addEventListener("loom:patch", function () { here = location.pathname + location.search; });

  // loomRemount swaps in a new session for this page when the server has
  // lost its own (a restart), keeping what the user typed. A page whose root layout changed reloads.
  root.loomRemount = function () { go(location.href, false, true); };

  // go navigates to url, pushing it to the history if push is true.
  function go(url, push, remount) {
    var n = ++nav, sid = root.getAttribute("data-sid");
    root.loomLeave(); // the server ends the session when it swaps
    fetch(url, { headers: { "X-Loom-Navigate": remount ? "-" : sid } }).then(function (r) {
      if (!remount && r.headers.get("X-Loom-Navigate") === "patch") return r.json().then(function (a) {
        if (n !== nav) return;
        root.loomStay();
        // HandleParams may have corrected the URL: keep the one it settled on.
        root.loomPatched(a.url + url.replace(/^[^#]*/, ""), push ? "push" : "replace");
      });
      // A remount redirected (an auth check failed) loads the page.
      if (r.headers.get("X-Loom-Navigate") !== "swap" || remount && r.url !== url.split("#")[0]) throw new Error("not swappable");
      return r.text().then(function (html) {
        if (n === nav) return swap(n, r.url, new DOMParser().parseFromString(html, "text/html"), push, remount);
      });
    }).catch(function () {
      if (n === nav) push ? location.assign(url) : location.replace(url);
    });
  }

  // swap takes the new document's stylesheets (once they load, so the
  // page never shows unstyled), live root, title, metadata and scripts.
  function swap(n, url, doc, push, remount) {
    var head = document.head, hrefs = [], loads = [], kept = {}, forms = [],
      tree = doc.getElementById("loom-tree"), old = document.getElementById("loom-tree");
    if (remount) {
      // Another root layout, scripts or wire protocol: load the page.
      var t = JSON.parse(tree.textContent), o = JSON.parse(old.textContent);
      if (t.root !== o.root || t.p !== o.p) return location.reload();
      controls().forEach(function (el) { kept[key(el)] = value(el); });
    }
    doc.querySelectorAll("link[rel=stylesheet]").forEach(function (l) {
      var href = l.getAttribute("href");
      hrefs.push(href);
      if (find(head, "link", "href", href)) return;
      l = head.appendChild(document.importNode(l));
      loads.push(new Promise(function (done) { l.onload = l.onerror = done; }));
    });
    return Promise.all(loads).then(function () {
      if (n !== nav) return; // another navigation started meanwhile
      head.querySelectorAll("link[rel=stylesheet]").forEach(function (l) {
        if (hrefs.indexOf(l.getAttribute("href")) < 0) l.remove();
      });
      if (push) history.pushState(null, "", url);
      here = location.pathname + location.search;
      var r = doc.getElementById("loom-root");
      root.setAttribute("data-sid", r.getAttribute("data-sid"));
      // The page's tree.
      old.remove();
      root.after(document.importNode(tree, true));
      document.title = doc.title;
      // Its metadata (<.live_meta />), as a share button would read it.
      head.querySelectorAll("[data-live-meta]").forEach(function (m) { m.remove(); });
      doc.querySelectorAll("[data-live-meta]").forEach(function (m) { head.appendChild(document.importNode(m, true)); });
      root.loomSwap(JSON.parse(tree.textContent));
      // A remount restores the form input, telling each form that has
      // an on:change, so the new session sees it too.
      if (remount) controls().forEach(function (el) {
        var v = kept[key(el)];
        if (v === undefined || v === value(el)) return;
        /checkbox|radio/.test(el.type) ? (el.checked = v) : (el.value = v);
        if (el.form && forms.indexOf(el.form) < 0) forms.push(el.form);
      });
      forms.forEach(function (f) { root.loomEmit(f, "change"); });
      // Scripts this page hasn't loaded: its behaviors. Scripts
      // put in the DOM run; parsed ones don't, so they are copied.
      doc.querySelectorAll("script[src]").forEach(function (s) {
        var src = s.getAttribute("src");
        if (find(document, "script", "src", src)) return;
        var n = document.createElement("script");
        if (s.type) n.type = s.type;
        n.src = src;
        document.body.appendChild(n);
      });
      if (push) {
        var target = location.hash && document.getElementById(decodeURIComponent(location.hash.slice(1)));
        target ? target.scrollIntoView() : scrollTo(0, 0);
      }
      document.dispatchEvent(new Event("loom:navigate"));
    });
  }

  // controls are the form fields whose input a remount keeps.
  function controls() {
    return Array.prototype.filter.call(root.querySelectorAll("input[name],select[name],textarea[name]"), function (el) {
      return !/hidden|file|password|submit|button/.test(el.type);
    });
  }
  function key(el) { return el.name + (/checkbox|radio/.test(el.type) ? "=" + el.value : ""); }
  function value(el) { return /checkbox|radio/.test(el.type) ? el.checked : el.value; }

  function find(within, tag, attr, value) {
    return Array.prototype.some.call(within.querySelectorAll(tag + "[" + attr + "]"), function (el) {
      return el.getAttribute(attr) === value;
    });
  }
})();
