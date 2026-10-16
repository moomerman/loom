import "github.com/moomerman/loom/auth/ui"

component Link(email string, invalid bool)

<style>
  h1 { margin: 0 0 token(space.6); text-align: center; font-size: token(text.xl); }
  .stack { display: grid; gap: token(space.4); }
  .as { margin: 0; text-align: center; color: token(color.muted); }
  .alt { margin: token(space.6) 0 0; text-align: center; font-size: token(text.sm); }
  a { color: token(color.accent); }
</style>
<h1>Sign in</h1>
<div class="stack">
  <.alert :if={invalid} tone="danger" title="This link doesn't work">It was used already, or it's more than a few minutes old. Ask for a new one.</.alert>
  <p :if={!invalid} class="as">as {email}</p>
  <.button :if={!invalid} label="Sign in" on:click="sign_in" />
</div>
<p class="alt" :if={invalid}><a href={path("/sign-in")}>Back to sign in</a></p>
