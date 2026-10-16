import "github.com/moomerman/loom/devmail/ui"

component Mailbox(mail []MailRow, count int, open bool, missing string, id string, to string, subject string, at string, links []Link, body []Part, html string)

<style>
  .app { display: grid; grid-template-columns: 22rem 1fr; height: 100%; }
  .list { display: flex; flex-direction: column; min-height: 0; background: token(color.sidebar); color: token(color.on-sidebar); border-right: 1px solid token(color.line); }
  .head { display: flex; align-items: center; gap: token(space.2); min-height: 3rem; padding: token(space.2) token(space.3) token(space.2) token(space.4); border-bottom: 1px solid token(color.line); }
  .logo { display: grid; place-items: center; width: 1.375rem; height: 1.375rem; border-radius: token(radius.md); background: token(color.accent); color: token(color.on-accent); }
  .logo svg { width: 0.875rem; height: 0.875rem; }
  .name { font-weight: 600; color: token(color.on-page); }
  .count { color: token(color.faint); font-weight: 500; }
  .clear { margin-left: auto; }
  .note { margin: 0; padding: token(space.2) token(space.4); border-bottom: 1px solid token(color.line); color: token(color.muted); font-size: token(text.sm); }
  ol { flex: 1; margin: 0; padding: token(space.2); list-style: none; overflow-y: auto; }
  .item { display: grid; grid-template-columns: 1fr auto; gap: 0 token(space.2); padding: token(space.2) token(space.3); border-radius: token(radius.md); color: inherit; }
  .item:hover { background: token(color.hover); text-decoration: none; }
  .item:focus-visible { outline: 2px solid token(color.focus); outline-offset: 1px; }
  .current { background: token(color.selected); }
  .current:hover { background: token(color.selected); }
  .to { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; color: token(color.on-page); }
  .when { color: token(color.muted); font-size: token(text.sm); white-space: nowrap; }
  .subject { grid-column: 1 / -1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: token(color.muted); }
  .main { min-width: 0; overflow-y: auto; background: token(color.page); }
  .content { padding: token(space.6); max-width: 52rem; }
  h1 { font-size: token(text.title); font-weight: 600; letter-spacing: -0.01em; overflow-wrap: anywhere; }
  .meta { display: grid; grid-template-columns: auto 1fr; gap: token(space.1) token(space.3); margin: token(space.3) 0 token(space.6); color: token(color.muted); }
  .meta dt { font-size: token(text.sm); }
  .meta dd { margin: 0; color: token(color.on-page); overflow-wrap: anywhere; }
  .links { display: grid; grid-template-columns: minmax(0, 1fr); gap: token(space.2); margin-bottom: token(space.6); }
  .link { display: flex; align-items: center; gap: token(space.3); padding: token(space.2) token(space.2) token(space.2) token(space.4); border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); box-shadow: token(shadow.card); }
  .href { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: token(color.muted); font-family: token(font.mono); font-size: token(text.code); }
  .follow { flex: none; padding: token(space.1) token(space.3); border-radius: token(radius.md); background: token(color.accent); color: token(color.on-accent); font-weight: 500; font-size: token(text.sm); }
  .follow:hover { filter: brightness(1.08); text-decoration: none; }
  .follow:focus-visible { outline: 2px solid token(color.focus); outline-offset: 2px; }
  h2 { margin-bottom: token(space.2); font-size: token(text.md); font-weight: 600; }
  .body { white-space: pre-wrap; overflow-wrap: anywhere; }
  section { margin-bottom: token(space.6); }
  iframe { display: block; width: 100%; height: 32rem; border: 1px solid token(color.line); border-radius: token(radius.lg); background: token(color.surface); }
  .missing { padding: token(space.12) token(space.6); }
  @media (max-width: 48rem) {
    .app { grid-template-columns: 1fr; grid-template-rows: auto 1fr; }
    .list { max-height: 40vh; border-right: 0; border-bottom: 1px solid token(color.line); }
    .content { padding: token(space.4); }
  }
</style>

<div class="app">
  <aside class="list">
    <div class="head">
      <span class="logo" aria-hidden="true"><svg viewBox="0 0 16 16"><rect x="2" y="3.5" width="12" height="9" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.5" /><path d="M2.5 4.5 8 8.75l5.5-4.25" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg></span>
      <span class="name">Mail</span>
      <span class="count">{count}</span>
      <span :if={count > 0} class="clear"><.button label="Clear" variant="ghost" size="sm" on:click="clear" /></span>
    </div>
    <p class="note">Mail the app sends in dev, kept here instead of sent.</p>
    <ol :if={count > 0} aria-label="Mail">
      <li :for={m <- mail} :key={m.id}>
        <a href={path("/" + m.id)} class={"item" if m.current, "current"} aria-current={if m.current, "page", "false"}>
          <span class="to">{m.to}</span>
          <span class="when">{m.when}</span>
          <span class="subject">{m.subject}</span>
        </a>
      </li>
    </ol>
  </aside>
  <main class="main">
    <div :if={open} class="content">
      <h1>{subject}</h1>
      <dl class="meta">
        <dt>To</dt><dd>{to}</dd>
        <dt>Sent</dt><dd>{at}</dd>
      </dl>
      <div :if={len(links) > 0} class="links">
        <div :for={l <- links} :key={l.href} class="link">
          <code class="href" title={l.href}>{l.href}</code>
          <a href={l.href} class="follow" target="_blank" rel="noreferrer">Open link</a>
        </div>
      </div>
      <section>
        <h2 :if={html != ""}>Text</h2>
        <pre class="body"><span :for={b <- body} :key={b.seq}><a :if={b.href != ""} href={b.href} target="_blank" rel="noreferrer">{b.text}</a><span :if={b.href == ""}>{b.text}</span></span></pre>
      </section>
      <section :if={html != ""}>
        <h2>HTML</h2>
        <iframe title={"HTML body of " + subject} sandbox="" srcdoc={html}></iframe>
      </section>
    </div>
    <div :if={!open && missing != ""} class="missing">
      <.empty_state title={"Mail " + missing + " is gone"}>
        <p>The mailbox keeps the last 50 mails, until the app restarts or you clear it. <a href={path("/")}>Newest mail</a></p>
      </.empty_state>
    </div>
    <div :if={!open && missing == ""} class="missing">
      <.empty_state title="No mail yet">
        <p>Sign-in links and other mail the app sends will appear here as they are sent.</p>
      </.empty_state>
    </div>
  </main>
</div>
