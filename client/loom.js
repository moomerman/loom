// loom.js: the browser runtime for Loom live pages.
//
// It keeps a copy of the page's render tree (docs/wire.md), applies the
// diffs the server streams over SSE, and morphs the DOM to match, keeping
// existing elements (and so focus and input state) wherever it can. It
// sends DOM events bound with on:<event> attributes to the server, and
// events behaviors emit (see behaviors.js, loaded only on pages with
// behaviors) through the same path. navigate.js navigates between pages
// with the same root layout, swapping only the live root.
(function () {
  "use strict";
  var root = document.getElementById("loom-root");
  if (!root) return;
  var sid = root.getAttribute("data-sid");
  var init = JSON.parse(document.getElementById("loom-tree").textContent);
  var tree = init.tree, version = init.v, stream, retry, wait = 1e3, leaving, path = location.pathname;

  // --- Render trees ---

  function html(n) {
    if (typeof n === "string") return n;
    if (n.k) return n.k.map(function (it) { return parts(n.s, it[1]); }).join("");
    return parts(n.s, n.d || []);
  }

  function parts(s, d) {
    var out = s[0];
    for (var i = 1; i < s.length; i++) out += html(d[i - 1]) + s[i];
    return out;
  }

  // apply returns the result of applying change c to the tree node old.
  function apply(old, c) {
    if (typeof c === "string" || c.s) return c; // replace
    if (c.d) return { s: old.s, d: slots(old.d, c.d) };
    var items = {};
    old.k.forEach(function (it) { items[it[0]] = it[1]; });
    if (c.k) { // same keys, same order: patch changed items
      c.k.forEach(function (e) { items[e[0]] = slots(items[e[0]], e[1]); });
      return { s: old.s, k: old.k.map(function (it) { return [it[0], items[it[0]]]; }) };
    }
    return { s: old.s, k: c.o.map(function (e) { // the complete new order
      var d = e[1];
      return [e[0], !d ? items[e[0]] : Array.isArray(d) ? d : slots(items[e[0]], d)];
    }) };
  }

  function slots(d, changes) {
    d = d.slice();
    for (var i in changes) d[i] = apply(d[i], changes[i]);
    return d;
  }

  // --- DOM morphing ---

  function update() {
    var t = document.createElement("template");
    t.innerHTML = html(tree);
    children(root, t.content);
    // The morph undid pending state; keep it where the ack's diff hasn't
    // arrived yet.
    pend = pend.filter(function (h) { return !(h.v <= version) && h.el.isConnected; });
    pend.forEach(pending);
    root.dispatchEvent(new Event("loom:patched")); // behaviors.js runs lifecycles
  }

  function key(n) { return n.nodeType === 1 ? n.getAttribute("data-loom-key") : null; }

  // same: the same tag, or both #text or #comment.
  function same(a, b) { return a.nodeName === b.nodeName; }

  // children makes f's children match t's. Elements with data-loom-key are
  // matched by key, wherever they are; other nodes by position and type.
  function children(f, t) {
    var keyed = {}, n, k, m, cur = f.firstChild, next = t.firstChild;
    for (n = cur; n; n = n.nextSibling) if ((k = key(n))) keyed[k] = n;
    while (next) {
      n = next;
      next = n.nextSibling; // n may move into f
      k = key(n);
      m = null;
      if (k) {
        if (keyed[k] && same(keyed[k], n)) m = keyed[k];
        delete keyed[k];
      } else if (cur && !key(cur) && same(cur, n)) m = cur;
      if (m) {
        if (m === cur) cur = cur.nextSibling;
        else f.insertBefore(m, cur);
        morph(m, n);
      } else f.insertBefore(n, cur);
    }
    while (cur) { n = cur.nextSibling; f.removeChild(cur); cur = n; }
  }

  function attrs(f, t) {
    var i, a;
    for (i = f.attributes.length - 1; i >= 0; i--) {
      a = f.attributes[i].name;
      if (!t.hasAttribute(a)) f.removeAttribute(a);
    }
    for (i = 0; i < t.attributes.length; i++) {
      a = t.attributes[i];
      if (f.getAttribute(a.name) !== a.value) f.setAttribute(a.name, a.value);
    }
  }

  function morph(f, t) {
    if (f.nodeType !== 1) {
      if (f.nodeValue !== t.nodeValue) f.nodeValue = t.nodeValue;
      return;
    }
    attrs(f, t);
    // Form state lives in properties, which stop following attributes
    // once the user interacts. Don't overwrite what the user is typing.
    if (f.tagName === "INPUT") {
      f.checked = t.hasAttribute("checked");
      if (f !== document.activeElement && f.type !== "checkbox" && f.type !== "radio") f.value = t.getAttribute("value") || "";
    }
    if (f.tagName === "TEXTAREA" && f !== document.activeElement) f.value = t.textContent;
    children(f, t);
  }

  // --- Events ---

  var aliases = { focusin: "focus", focusout: "blur" };
  ["click", "change", "input", "submit", "keydown", "keyup", "focusin", "focusout"].forEach(function (type) {
    root.addEventListener(type, function (e) {
      var name = aliases[type] || type, attr = "on:" + name;
      for (var el = e.target; el && el !== root.parentNode; el = el.parentNode) {
        if (el.nodeType === 1 && el.hasAttribute(attr)) {
          if (name === "submit") e.preventDefault();
          fire(el, attr, e);
          if (name === "submit") el.reset();
          return;
        }
      }
    });
  });

  // A behavior's emit(name, data) sends el's on:<name> event, with data
  // added to the payload.
  root.loomEmit = function (el, name, data) { if (el.hasAttribute("on:" + name)) fire(el, "on:" + name, {}, data); };

  function fire(el, attr, e, data) {
    var p = payload(el, e), k;
    for (k in data) p[k] = String(data[k]);
    root.loomSend(el.getAttribute(attr), p, el);
  }

  // --- Pending events ---

  // From sending an event until the diff of its ack is applied, the
  // element that sent it has class loom-pending and its optimistic:*
  // hints applied: optimistic:class="a b" toggles classes,
  // optimistic:disabled and optimistic:hidden set those. The morph puts
  // every attribute back as the server rendered it, so update re-applies
  // them until the event's update is in, and settle undoes them.
  var pend = [];

  function pending(h) {
    var el = h.el, c = el.getAttribute("optimistic:class");
    el.classList.add("loom-pending");
    if (c) c.split(/\s+/).forEach(function (k) { if (k) el.classList.toggle(k); });
    if (el.hasAttribute("optimistic:disabled")) el.disabled = true;
    if (el.hasAttribute("optimistic:hidden")) el.hidden = true;
  }

  // done ends event s's pending state once the page has version v (0 for
  // a dropped event).
  function done(s, v) {
    pend.forEach(function (h) { if (h.s === s) h.v = v; });
    settle();
  }

  // settle undoes the pending state of events whose update is in: their
  // elements' attributes go back to the render. Only theirs: a morph of
  // the page would also undo what behaviors set, such as focusability.
  function settle() {
    var t, ended = pend.filter(function (h) { return h.v <= version; });
    if (!ended.length) return;
    pend = pend.filter(function (h) { return ended.indexOf(h) < 0; });
    t = document.createElement("template");
    t.innerHTML = html(tree);
    ended.forEach(function (h) {
      var n = t.content, p = [], el = h.el;
      if (!el.isConnected) return;
      for (; el && el !== root; el = el.parentNode) p.unshift(Array.prototype.indexOf.call(el.parentNode.childNodes, el));
      p.forEach(function (i) { n = n && n.childNodes[i]; });
      if (n && same(n, h.el)) attrs(h.el, n);
    });
    pend.forEach(function (h) { if (ended.some(function (e) { return e.el === h.el; })) pending(h); });
    root.dispatchEvent(new Event("loom:patched"));
  }

  // payload collects an event's data: val-* attributes, a form's fields,
  // an input's value and checked state, and the key pressed.
  function payload(el, e) {
    var p = {}, i, a;
    if (el.tagName === "FORM") new FormData(el).forEach(function (v, k) { p[k] = String(v); });
    if (/^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName)) p.value = el.value;
    if (el.type === "checkbox" || el.type === "radio") p.checked = String(el.checked);
    if (e.key) p.key = e.key;
    for (i = 0; i < el.attributes.length; i++) {
      a = el.attributes[i];
      if (a.name.indexOf("val-") === 0) p[a.name.slice(4)] = a.value;
    }
    return p;
  }

  // --- Outbox ---

  // Events wait in an outbox until the server acks them, having run their
  // handler (docs/wire.md). It is kept in sessionStorage, so a reload
  // keeps it, and sent one event at a time, in order. A network error or
  // a 5xx (such as a restart) is retried with backoff, and the server runs
  // each (client, seq) once however often it arrives. A 404 means this
  // page's session is gone: remount, then resend. Any other refusal drops
  // the event and reports it; a 429 (a rate limit, RT436) also shows the
  // page's rate-limit notes until its Retry-After (limit).
  var client = crypto.randomUUID ? crypto.randomUUID() : String(Math.random()).slice(2),
    seq = 0, box = [], sending, held, limited, doc = document.documentElement;
  try {
    // Entries a reload left for this page are resent to its new session,
    // marked moved (m), as its render may no longer show what sent them
    // (RT438); those for another page can't be, as it's gone.
    box = JSON.parse(sessionStorage.loomOutbox || "[]").filter(function (m) {
      if (m.u === location.pathname) return (m.m = m.i = sid);
      console.error("loom: dropped an unacked " + m.e + " event of " + m.u + ", which is no longer open");
    });
  } catch (e) {}

  // limit shows (on) or hides the page's rate-limit notes: <html> gets
  // class loom-limited, and elements with data-loom-limited are unhidden.
  function limit(on) {
    doc.classList.toggle("loom-limited", !!on);
    doc.querySelectorAll("[data-loom-limited]").forEach(function (e) { e.hidden = !on; });
  }

  function save() {
    try { sessionStorage.loomOutbox = JSON.stringify(box); } catch (e) {}
  }

  // loomSend queues an event for the server.
  root.loomSend = function (event, p, el) {
    box.push({ c: client, s: ++seq, i: sid, u: location.pathname, e: event, p: p });
    save();
    if (el) {
      pend.push({ s: seq, el: el });
      pending(pend[pend.length - 1]);
    }
    flush();
  };

  function flush() {
    var m = box[0];
    if (sending || held || !m) return;
    sending = 1;
    fetch("/_loom/event", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sid: m.i, client: m.c, seq: m.s, event: m.e, payload: m.p, moved: !!m.m })
    }).then(function (r) {
      if (r.status > 499) throw r;
      sending = 0;
      wait = 1e3;
      if (r.status === 404 && m.i === sid) {
        held = 1; // until loomSwap resends it to the new session
        return remount();
      }
      box.shift();
      save();
      if (r.status === 429) {
        clearTimeout(limited);
        limit(1);
        limited = setTimeout(limit, (r.headers.get("Retry-After") || 60) * 1000);
      }
      if (r.status > 299) {
        done(m.s, 0);
        r.text().then(function (t) { console.error("loom: the server refused " + m.e + ": " + t); });
      } else r.json().then(function (a) { done(m.s, a.v); }, function () { done(m.s, 0); });
      flush();
    }).catch(function () {
      sending = 0;
      later(flush);
    });
  }

  // title sets the document title from the page's title, framed as the
  // root layout's <.live_title> says (its data-* attributes).
  function title(t) {
    if (typeof t !== "string") return;
    var d = (document.querySelector("title") || {}).dataset || {};
    document.title = (d.prefix || "") + (t || d.default || "") + (d.suffix || "");
  }

  // --- Stream ---

  function full(m) {
    tree = m.tree;
    version = m.v;
    // Acked events are in this tree, whatever their version.
    pend.forEach(function (h) { if (h.v) h.v = 0; });
    update();
    title(m.title);
  }

  function connect() {
    stream = new EventSource("/_loom/stream?sid=" + sid + "&v=" + version);
    stream.addEventListener("full", function (e) { full(JSON.parse(e.data)); });
    stream.addEventListener("diff", function (e) {
      var m = JSON.parse(e.data);
      if (m.v !== version + 1) return reconnect(); // missed an update: resync
      version = m.v;
      if (m.diff) {
        tree = apply(tree, m.diff);
        update();
      } else settle();
      title(m.title);
    });
    stream.addEventListener("reload", function () {
      stream.close();
      if (!leaving) remount(); // else the server ended it for the page we're going to
    });
    stream.addEventListener("navigate", function (e) { root.loomGo(JSON.parse(e.data).to); });
    stream.addEventListener("patch", function (e) {
      var m = JSON.parse(e.data);
      root.loomPatched(m.to, m.replace ? "replace" : "push");
    });
    stream.onopen = function () {
      wait = 1e3;
      root.classList.add("loom-connected");
    };
    stream.onerror = reconnect;
  }

  // The root has class loom-connected while the stream is open, so pages
  // can style a lost connection.
  function reconnect() {
    root.classList.remove("loom-connected");
    stream.close();
    retry = later(connect);
  }

  // later runs f after a wait that doubles with each failure, to 30s,
  // reset when the stream opens or an event is answered, and is random
  // within half of it either way, so the tabs of a restarted server
  // don't all come back in the same second.
  function later(f) {
    var t = setTimeout(f, wait * (0.5 + Math.random()));
    wait = Math.min(wait * 2, 3e4);
    return t;
  }

  // --- Navigation (navigate.js) ---

  // loomLeave marks the page as maybe leaving, while navigate.js asks
  // the server: the session ends if it swaps in another page, and its
  // stream's reload is then no reason to remount. loomStay undoes it when
  // the server patched this page instead. loomSwap takes over another
  // page, whose data-sid navigate.js has set on the root, starting from
  // its tree t.
  root.loomLeave = function () { leaving = 1; };
  root.loomStay = function () { leaving = 0; };

  // loomPatched records that the page's URL changed to url within the
  // page (c.Patch, or a link to the page's own route): how is "push" or
  // "replace" for the history, or "" if the browser already shows url.
  // Unacked events follow the page to its new path.
  root.loomPatched = function (url, how) {
    if (how) history[how + "State"](null, "", url);
    box.forEach(function (m) { if (m.u === path) m.u = location.pathname; });
    path = location.pathname;
    save();
    document.dispatchEvent(new Event("loom:patch"));
  };
  // The server lost this page's session (it restarted): take over a new
  // one, keeping the document and form input (navigate.js).
  function remount() {
    root.loomRemount ? root.loomRemount() : location.reload();
  }
  root.loomSwap = function (t) {
    stream.close();
    clearTimeout(retry);
    leaving = 0;
    path = location.pathname;
    sid = root.getAttribute("data-sid");
    full(t);
    connect();
    // Unacked events of this page go to its new session, marked moved.
    box.forEach(function (m) { if (m.u === location.pathname) m.m = m.i = sid; });
    save();
    held = 0;
    flush();
  };

  connect();
  flush();
})();
