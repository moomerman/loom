// hint shows an element's aria-label as a tooltip.
export function mount(el) {
  el.title = el.getAttribute("aria-label");
  return {};
}
