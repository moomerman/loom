import "github.com/moomerman/loom/auth/ui"

component Reset(reset form[ResetInput], email string, invalid bool)

<style>
  h1 { margin: 0 0 token(space.6); text-align: center; font-size: token(text.xl); }
  form { display: grid; gap: token(space.4); }
  .alt { margin: token(space.6) 0 0; text-align: center; font-size: token(text.sm); }
  a { color: token(color.accent); }
  .for { margin: 0; color: token(color.muted); font-size: token(text.sm); }
</style>
<h1>Choose a new password</h1>
<.alert :if={invalid} tone="danger" title="This link doesn't work">It was used already, or it's more than a few minutes old. Ask for a new one.</.alert>
<form :if={!invalid} on:change="change" on:submit="save">
  <p class="for">For {email}. Saving it signs you out everywhere else.</p>
  <.input name="password" label="New password" type="password" hint="At least 8 characters." error={reset.password.error} autofocus />
  <.button label="Save password and sign in" type="submit" />
</form>
<p class="alt"><a href={path(if invalid, "/forgot", "/sign-in")}>{if invalid, "Get a new link", "Back to sign in"}</a></p>
