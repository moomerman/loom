// behaviors.js: runs behaviors' lifecycles (docs/components.md).
//
// A page whose components attach behaviors loads a module that imports
// this one and each behavior's module, then calls define. Elements with
// use:name carry data-loom-use (the behavior's ID) and data-loom-props
// (JSON). After the page first loads and after every patch, scan mounts
// new elements, updates kept ones, and destroys removed ones. loom.js's
// patcher keeps elements wherever it can, including keyed elements that
// move, so a behavior's state survives updates and reordering.
//
// Nothing here talks to the server: emit goes through loom.js's on:
// event path, and behaviors work the same with the connection down.
const root = document.getElementById("loom-root");
const defs = {}; // ID -> [module, emits]
const live = new Set();
let dev = false;

// define registers behaviors by ID and mounts any already on the page.
export function define(behaviors, isDev) {
  Object.assign(defs, behaviors);
  dev = isDev;
  scan();
}

function scan() {
  for (const el of live) {
    if (!root.contains(el) || el.getAttribute("data-loom-use") !== el.loomUse.id) destroy(el);
  }
  for (const el of root.querySelectorAll("[data-loom-use]")) {
    const props = JSON.parse(el.getAttribute("data-loom-props") || "{}");
    if (el.loomUse) {
      // Called after every patch, since the patcher resets attributes the
      // behavior set: update re-applies them.
      call(el.loomUse, "update", props);
      continue;
    }
    const id = el.getAttribute("data-loom-use");
    const def = defs[id];
    if (!def) continue;
    const requires = el.getAttribute("data-loom-requires");
    if (dev && requires && !el.matches(requires)) {
      console.error(`RT417: ${id.replace(/-\w+$/, "")} requires ${requires}, but this element doesn't match; fix the attribute's value in the template`, el);
    }
    const emit = (name, data) => {
      if (dev && !def[1].includes(name)) {
        console.error(`RT418: ${id.replace(/-\w+$/, "")} emitted ${name}, which it doesn't declare; add it to the behavior's emits (...)`, el);
        return;
      }
      root.loomEmit(el, name, data || {});
    };
    const u = { id, inst: {} };
    el.loomUse = u;
    live.add(el);
    try {
      u.inst = def[0].mount(el, props, emit) || {};
    } catch (err) {
      console.error(err);
    }
  }
}

function call(u, fn, arg) {
  try {
    if (u.inst[fn]) u.inst[fn](arg);
  } catch (err) {
    console.error(err);
  }
}

function destroy(el) {
  call(el.loomUse, "destroy");
  el.loomUse = null;
  live.delete(el);
}

root.addEventListener("loom:patched", scan);
