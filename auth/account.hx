import "github.com/moomerman/loom/auth/ui"

component Account(email string, password bool, change form[PasswordInput], changed bool, via string, two_factor string, two_factor_on bool, codes_left int, manage form[CodeInput], new_codes []string, turned_off bool)

<style>
  h1 { margin: 0 0 token(space.2); text-align: center; font-size: token(text.xl); }
  h2 { margin: 0; font-size: token(text.md); }
  .email { margin: 0 0 token(space.6); text-align: center; color: token(color.muted); }
  .via { margin: calc(-1 * token(space.4)) 0 token(space.6); text-align: center; font-size: token(text.sm); color: token(color.muted); }
  form { display: grid; gap: token(space.4); }
  .section { display: grid; gap: token(space.4); margin-top: token(space.6); padding-top: token(space.6); border-top: 1px solid token(color.line); }
  .section p { margin: 0; }
  .note { font-size: token(text.sm); color: token(color.muted); }
  .actions { display: flex; flex-wrap: wrap; gap: token(space.2); }
  .out { display: grid; gap: token(space.2); margin-top: token(space.6); padding-top: token(space.6); border-top: 1px solid token(color.line); }
</style>
<h1>Your account</h1>
<p class="email">{email}</p>
<p :if={via != ""} class="via">You also sign in with {via}.</p>
<form on:change="change" on:submit="save_password">
  <h2>{if password, "Change your password", "Set a password"}</h2>
  <.alert :if={changed} tone="success" title="Password saved">You've been signed out everywhere else.</.alert>
  <.input :if={password} name="current" label="Current password" type="password" error={change.current.error} />
  <.input name="new" label="New password" type="password" hint="At least 8 characters. Saving it signs you out everywhere else." error={change.new.error} />
  <.button label="Save password" type="submit" variant="secondary" />
</form>
<div :if={two_factor != "off" && !two_factor_on} class="section">
  <h2>Two-factor sign-in</h2>
  <.alert :if={turned_off} tone="success" title="Two-factor sign-in is off">Signing in no longer asks for a code.</.alert>
  <p class="note">{if two_factor == "required", "This app asks for a code from an authenticator app at every sign-in. Set one up now, or you'll be asked to next time.", "Ask for a code from an authenticator app as well, whenever you sign in, so a stolen password alone can't get in."}</p>
  <.button label="Set up two-factor sign-in" variant="secondary" on:click="set_up_two_factor" />
</div>
<form :if={two_factor_on} class="section" on:change="code_change" on:submit="new_codes">
  <h2>Two-factor sign-in</h2>
  <p class="note">On. You have {codes_left} recovery {if codes_left == 1, "code", "codes"} left.</p>
  <.recovery_codes :if={len(new_codes) > 0} codes={new_codes} />
  <.input field={manage.code} label="Code" hint="To make new recovery codes or turn this off, type a code from your authenticator app." autocomplete="one-time-code" />
  <div class="actions">
    <.button label="New recovery codes" type="submit" variant="secondary" />
    <.button :if={two_factor != "required"} label="Turn off" variant="ghost" on:click="turn_off" />
  </div>
</form>
<div class="out">
  <.button label="Sign out" on:click="sign_out" />
  <.button label="Sign out everywhere" variant="ghost" on:click="sign_out_everywhere" />
</div>
