// dropdown drives a listbox of [role=option][data-value] items: when it
// opens, focus goes to the selected option (or the first); arrow keys,
// Home and End move between options; Enter, Space or a click emits
// select(value); Escape, Tab or a click outside emits close. The server
// owns open and value, so the listbox only changes when the page does.
export function mount(el, props, emit) {
  const options = () => [...el.querySelectorAll("[role=option]")];

  // apply marks the selected option and makes options focusable. The
  // patcher resets these attributes, so it runs on every update.
  const apply = () => {
    for (const o of options()) {
      o.setAttribute("tabindex", "-1");
      o.setAttribute("aria-selected", String(o.dataset.value === props.value));
    }
  };
  const focusSelected = () => {
    const list = options();
    (list.find((o) => o.dataset.value === props.value) || list[0] || el).focus();
  };

  const keydown = (e) => {
    const list = options();
    const i = list.indexOf(document.activeElement);
    const go = (j) => {
      e.preventDefault();
      if (list.length) list[(j + list.length) % list.length].focus();
    };
    if (e.key === "ArrowDown") go(i + 1);
    else if (e.key === "ArrowUp") go(i < 0 ? -1 : i - 1);
    else if (e.key === "Home") go(0);
    else if (e.key === "End") go(-1);
    else if ((e.key === "Enter" || e.key === " ") && i >= 0) {
      e.preventDefault();
      emit("select", { value: list[i].dataset.value });
    } else if (e.key === "Escape") {
      e.preventDefault();
      close();
    } else if (e.key === "Tab") close();
  };
  const click = (e) => {
    const o = e.target.closest("[role=option]");
    if (o && el.contains(o)) emit("select", { value: o.dataset.value });
  };
  const outside = (e) => {
    if (props.open && !el.parentElement.contains(e.target)) close();
  };
  // close gives focus back to the button that opened the listbox.
  const close = () => {
    const trigger = el.parentElement.querySelector("[aria-haspopup]");
    if (trigger && el.contains(document.activeElement)) trigger.focus();
    emit("close");
  };

  el.addEventListener("keydown", keydown);
  el.addEventListener("click", click);
  document.addEventListener("click", outside);
  apply();
  if (props.open) focusSelected();

  return {
    update(next) {
      const opened = next.open && !props.open;
      props = next;
      apply();
      if (opened) focusSelected();
    },
    destroy() {
      document.removeEventListener("click", outside);
    },
  };
}
