// menu is a disclosure menu of links or buttons on a <details>: the
// browser opens and closes it from its <summary>, and menu also closes it
// when a link or button inside is chosen, on a click outside, and on
// Escape, which gives focus back to the summary. A live navigation
// closes it too (navigate.js). It keeps no state on the server.
export function mount(el) {
  const shut = () => { el.open = false; };
  const click = (e) => {
    if (e.target.closest("a[href], button") && !e.target.closest("summary")) shut();
  };
  const outside = (e) => {
    if (el.open && !el.contains(e.target)) shut();
  };
  const keydown = (e) => {
    if (e.key !== "Escape" || !el.open) return;
    e.preventDefault();
    shut();
    el.querySelector("summary").focus();
  };
  el.addEventListener("click", click);
  el.addEventListener("keydown", keydown);
  document.addEventListener("click", outside);
  return {
    destroy() {
      document.removeEventListener("click", outside);
    },
  };
}
