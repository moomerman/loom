import "github.com/moomerman/loom/auth/ui"

component Forgot(forgot form[ForgotInput], sent string)

<style>
  h1 { margin: 0 0 token(space.6); text-align: center; font-size: token(text.xl); }
  form { display: grid; gap: token(space.4); }
  .alt { margin: token(space.6) 0 0; text-align: center; font-size: token(text.sm); }
  a { color: token(color.accent); }
</style>
<h1>Reset your password</h1>
<.alert :if={sent != ""} tone="success" title="Check your email">If there's an account for {sent}, we've sent it a link to choose a new password.</.alert>
<form :if={sent == ""} on:change="change" on:submit="send">
  <.input field={forgot.email} label="Email" type="email" hint="We'll email you a link to choose a new password." autofocus />
  <.button label="Email me a link" type="submit" />
</form>
<p class="alt"><a href={path("/sign-in")}>Back to sign in</a></p>
