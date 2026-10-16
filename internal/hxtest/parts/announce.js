// announce marks a live region polite or assertive.
export function mount(el, props) {
  const set = (p) => el.setAttribute("aria-live", p.polite ? "polite" : "assertive");
  set(props);
  return { update: set };
}
