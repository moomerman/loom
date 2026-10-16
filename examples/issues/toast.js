// toast dismisses a toast after a few seconds, unless the pointer is on
// it. A new toast (a new id) starts the time again.
const shown = 4000;

export function mount(el, props, emit) {
  let timer = 0;
  const start = () => {
    clearTimeout(timer);
    timer = setTimeout(() => emit("dismiss"), shown);
  };
  const enter = () => clearTimeout(timer);
  el.addEventListener("mouseenter", enter);
  el.addEventListener("mouseleave", start);
  start();
  return {
    update(next) {
      if (next.id !== props.id) start();
      props = next;
    },
    destroy() {
      clearTimeout(timer);
    },
  };
}
