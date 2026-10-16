// tabs moves between the tabs of a tab list with the arrow keys, Home and
// End, and activates the tab it moves to by clicking it, so the tab's own
// on:click runs. Only the selected tab is in the Tab order (tabindex 0,
// set by the template), as the ARIA tabs pattern asks.
export function mount(el) {
  const keydown = (e) => {
    const tabs = [...el.querySelectorAll("[role=tab]")];
    const i = tabs.indexOf(document.activeElement);
    if (i < 0) return;
    const j = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 }[e.key];
    if (j === undefined) return;
    e.preventDefault();
    const tab = tabs[(j + tabs.length) % tabs.length];
    tab.focus();
    tab.click();
  };
  el.addEventListener("keydown", keydown);
  return {};
}
