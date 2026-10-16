import "github.com/moomerman/loom/auth/ui"

component Challenge(mode string, email string, code form[CodeInput], qr string, secret string, codes []string, next string)

<style>
  h1 { margin: 0 0 token(space.2); text-align: center; font-size: token(text.xl); }
  .as { margin: 0 0 token(space.6); text-align: center; color: token(color.muted); }
  form, .stack { display: grid; gap: token(space.4); }
  .alt { margin: token(space.6) 0 0; text-align: center; font-size: token(text.sm); }
  a { color: token(color.accent); }
</style>
<h1>{if mode == "set_up" || mode == "codes", "Set up two-factor sign-in", "Two-factor sign-in"}</h1>
<p :if={email != ""} class="as">as {email}</p>
<form :if={mode == "code"} on:change="change" on:submit="verify">
  <.input field={code.code} label="Code" hint="From your authenticator app, or one of your recovery codes." autocomplete="one-time-code" autofocus />
  <.button label="Sign in" type="submit" />
</form>
<form :if={mode == "set_up"} on:change="change" on:submit="set_up">
  <.alert title="This app asks for a second step">Signing in takes your password and a code from your phone, so a stolen password alone can't get in.</.alert>
  <.authenticator qr={qr} secret={secret} />
  <.input field={code.code} label="Code" autocomplete="one-time-code" />
  <.button label="Turn on and sign in" type="submit" />
</form>
<div :if={mode == "codes"} class="stack">
  <.recovery_codes codes={codes} />
  <.button label="Continue" on:click="continue" />
</div>
<.alert :if={mode == "expired"} tone="danger" title="Sign in again">This sign-in took more than ten minutes, or had too many wrong codes.</.alert>
<p class="alt"><a href={path("/sign-in") + next}>{if mode == "expired", "Back to sign in", "Sign in as someone else"}</a></p>
