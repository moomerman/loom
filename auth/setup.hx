import "github.com/moomerman/loom/auth/ui"

component SetUp(on bool, code form[CodeInput], qr string, secret string, codes []string)

<style>
  h1 { margin: 0 0 token(space.6); text-align: center; font-size: token(text.xl); }
  form, .stack { display: grid; gap: token(space.4); }
  .alt { margin: token(space.6) 0 0; text-align: center; font-size: token(text.sm); }
  a { color: token(color.accent); }
</style>
<h1>Set up two-factor sign-in</h1>
<form :if={!on} on:change="change" on:submit="set_up">
  <.authenticator qr={qr} secret={secret} />
  <.input field={code.code} label="Code" autocomplete="one-time-code" autofocus />
  <.button label="Turn on" type="submit" />
</form>
<div :if={on} class="stack">
  <.alert :if={len(codes) == 0} tone="success" title="Two-factor sign-in is on">Signing in asks for a code from your authenticator app.</.alert>
  <.recovery_codes :if={len(codes) > 0} codes={codes} />
</div>
<p class="alt"><a href={path("/account")}>{if on, "Back to your account", "Cancel"}</a></p>
