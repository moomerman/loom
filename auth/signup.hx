import "github.com/moomerman/loom/auth/ui"

component SignUp(sign_up form[SignUpInput], next string)

<style>
  h1 { margin: 0 0 token(space.6); text-align: center; font-size: token(text.xl); }
  form { display: grid; gap: token(space.4); }
  .alt { margin: token(space.6) 0 0; text-align: center; font-size: token(text.sm); }
  a { color: token(color.accent); }
</style>
<h1>Create an account</h1>
<form on:change="change" on:submit="sign_up">
  <.input field={sign_up.email} label="Email" type="email" autofocus />
  <.input name="password" label="Password" type="password" hint="At least 8 characters." error={sign_up.password.error} />
  <.button label="Create account" type="submit" />
</form>
<p class="alt">Have an account? <a href={path("/sign-in") + next}>Sign in</a></p>
