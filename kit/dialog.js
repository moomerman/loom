// dialog keeps focus inside a modal dialog while it is open: it moves
// focus into the dialog when it mounts (to the [autofocus] control, else
// the first one), keeps Tab and Shift+Tab inside, emits close on Escape,
// and gives focus back to where it was when the dialog goes away.
const controls = "a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex='-1'])";

export function mount(el, props, emit) {
  const before = document.activeElement;
  const all = () => [...el.querySelectorAll(controls)];
  (el.querySelector("[autofocus]") || all()[0] || el).focus();

  const keydown = (e) => {
    if (e.key === "Escape") {
      e.preventDefault();
      emit("close");
      return;
    }
    if (e.key !== "Tab") return;
    const list = all();
    if (list.length === 0) {
      e.preventDefault();
      return;
    }
    const first = list[0];
    const last = list[list.length - 1];
    if (e.shiftKey && (document.activeElement === first || document.activeElement === el)) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  };
  el.addEventListener("keydown", keydown);

  return {
    destroy() {
      el.removeEventListener("keydown", keydown);
      if (before && before.isConnected) before.focus();
    },
  };
}
