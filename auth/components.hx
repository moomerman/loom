import "github.com/moomerman/loom/auth/ui"

component Authenticator(qr string, secret string)

<style>
  .scan { margin: 0; }
  img { display: block; width: calc(token(space.8) * 6); max-width: 100%; margin: 0 auto; border-radius: token(radius.md); }
  .key { margin: 0; font-size: token(text.sm); color: token(color.muted); text-align: center; }
  code { font-family: token(font.mono); color: token(color.on-surface); white-space: nowrap; }
</style>
<p class="scan">Scan this with an authenticator app, such as 1Password, Google Authenticator or Authy, then type the code it shows.</p>
<img :if={qr != ""} src={qr} alt="QR code to add this account to an authenticator app" />
<p class="key">Can't scan it? Enter this key: <code>{secret}</code></p>

component RecoveryCodes(codes []string)

<style>
  ul { display: grid; grid-template-columns: 1fr 1fr; gap: token(space.1) token(space.4); margin: 0; padding: token(space.3) token(space.4); list-style: none; border: 1px solid token(color.line); border-radius: token(radius.md); background: token(color.subtle); color: token(color.on-subtle); font-family: token(font.mono); text-align: center; }
</style>
<.alert tone="warning" title="Save your recovery codes">If you lose your phone, each of these signs you in once. Keep them somewhere safe, like a password manager: they won't be shown again.</.alert>
<ul aria-label="Recovery codes">
  <li :for={code <- codes} :key={code}>{code}</li>
</ul>
