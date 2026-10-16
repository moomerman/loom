component Spinner(label string = "Loading", size: :sm | :md = :md)

<style>
  .spinner { display: inline-block; border: 2px solid token(color.line); border-top-color: token(color.accent); border-radius: token(radius.full); animation: spin 0.8s linear infinite; }
  .sm { width: 1em; height: 1em; }
  .md { width: 2em; height: 2em; }
  .hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
<span role="status"><span class={"spinner" size}></span><span class="hidden">{label}</span></span>
