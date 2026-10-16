// item records its lifecycle in window.itemLog, for the browser tests,
// and marks its element with data-mounted, which the patcher removes and
// update puts back.
export function mount(el, props) {
  const log = (window.itemLog ||= []);
  log.push("mount " + props.id);
  el.dataset.mounted = props.id;
  return {
    update(p) {
      el.dataset.mounted = p.id;
    },
    destroy() {
      log.push("destroy " + props.id);
    },
  };
}
