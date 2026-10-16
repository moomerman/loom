// sortable lets people reorder a list's items with Alt+Arrow keys and
// emits moved(id, to). It keeps no state the server doesn't send back.
export function mount(el, props, emit) {
  const key = (e) => {
    const li = e.target.closest("li");
    if (!li || !e.altKey) return;
    const next = props.axis === "x" ? "ArrowRight" : "ArrowDown";
    const prev = props.axis === "x" ? "ArrowLeft" : "ArrowUp";
    const i = [...el.children].indexOf(li);
    if (e.key === next) emit("moved", { id: props.ids[i], to: i + 1 });
    if (e.key === prev) emit("moved", { id: props.ids[i], to: i - 1 });
    if (e.key === "Escape") emit("cancel");
  };
  el.addEventListener("keydown", key);
  return {
    update(p) { props = p; },
    destroy() { el.removeEventListener("keydown", key); },
  };
}
