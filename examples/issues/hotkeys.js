// hotkeys gives the app its keyboard shortcuts. It knows no actions
// itself: elements declare theirs with data-hotkey, and a key press
// clicks the element that names it (focuses it, for an input), so each
// shortcut does exactly what clicking does, through the normal event
// path.
//
//   <button data-hotkey="c" on:click="compose">   C opens the composer
//   <a href="/inbox" data-hotkey="g i">            G then I goes to the inbox
//   <input data-hotkey="/">                         / focuses search
//
// J and K move focus through the rows of a list ([data-row]), and Enter
// follows the focused row's link, as browsers do. ⌘ or Ctrl+Enter
// submits the form being typed in.
export function mount(el) {
  let pending = ""; // "g" after G, waiting for the second key
  let timer = 0;

  const typing = (t) => t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName);

  const keydown = (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && typing(e.target) && e.target.form) {
      e.preventDefault();
      e.target.form.requestSubmit();
      return;
    }
    if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return;
    if (typing(e.target)) {
      if (e.key === "Escape") e.target.blur();
      return;
    }
    if (document.querySelector("[aria-modal=true]")) return; // a dialog handles its own keys

    const key = e.key.toLowerCase();
    if (key === "j" || key === "k") {
      e.preventDefault();
      move(key === "j" ? 1 : -1);
      return;
    }
    const combo = pending ? pending + " " + key : key;
    clearTimeout(timer);
    pending = "";
    const target = el.querySelector(`[data-hotkey="${CSS.escape(combo)}"]`);
    if (target) {
      e.preventDefault();
      if (typing(target)) target.focus();
      else target.click();
    } else if (key === "g") {
      pending = "g";
      timer = setTimeout(() => (pending = ""), 1200);
    }
  };

  // move focuses the next or previous row, starting from the focused one.
  const move = (step) => {
    const rows = [...el.querySelectorAll("[data-row]")];
    if (!rows.length) return;
    const i = rows.indexOf(document.activeElement.closest("[data-row]"));
    const next = rows[i < 0 ? (step > 0 ? 0 : rows.length - 1) : Math.min(rows.length - 1, Math.max(0, i + step))];
    next.focus();
    next.scrollIntoView({ block: "nearest" });
  };

  document.addEventListener("keydown", keydown);
  return {
    destroy() {
      document.removeEventListener("keydown", keydown);
      clearTimeout(timer);
    },
  };
}
