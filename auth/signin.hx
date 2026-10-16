import "github.com/moomerman/loom/auth/ui"

component SignIn(sign_in form[SignInInput], sent string, email bool, sign_up bool, next string, providers []ProviderButton, failed string)

<style>
  h1 { margin: 0 0 token(space.6); text-align: center; font-size: token(text.xl); }
  form { display: grid; gap: token(space.4); }
  .alt { display: flex; flex-wrap: wrap; justify-content: center; gap: token(space.4); margin: token(space.6) 0 0; font-size: token(text.sm); }
  a { color: token(color.accent); }
  .failed { margin: 0 0 token(space.4); }
  .providers { display: grid; gap: token(space.2); margin: 0 0 token(space.6); }
  .provider { display: block; padding: token(space.2) token(space.4); border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.subtle); color: token(color.on-subtle); font-weight: 500; text-align: center; text-decoration: none; }
  .provider:hover { filter: brightness(1.08); }
  .provider:focus-visible { outline: 2px solid token(color.focus); outline-offset: 2px; }
</style>
<h1>Sign in</h1>
<div :if={failed != ""} class="failed"><.alert tone="danger">{failed}</.alert></div>
<div :if={len(providers) > 0} class="providers">
  <a :for={pr <- providers} :key={pr.name} class="provider" href={path("/oauth/" + pr.name) + next}>{pr.label}</a>
</div>
<form on:change="change" on:submit="sign_in">
  <.alert :if={sent != ""} tone="success" title="Check your email">We sent a sign-in link to {sent}. It works once.</.alert>
  <.input field={sign_in.email} label="Email" type="email" autofocus />
  <.input name="password" label="Password" type="password" error={sign_in.password.error} />
  <.button label="Sign in" type="submit" />
  <.button :if={email} label="Email me a sign-in link" variant="secondary" on:click="email_link" />
</form>
<p class="alt">
  <a :if={email} href={path("/forgot")}>Forgot your password?</a>
  <a :if={sign_up} href={path("/sign-up") + next}>Create an account</a>
</p>
