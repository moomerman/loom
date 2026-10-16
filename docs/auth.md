# Sign-in and sessions

Loom keeps who is signed in; your app keeps who its users are. The
`auth` sub-app gives an app sign-in pages in one line: email and
password, sign-in links by email, password resets, Google and GitHub,
two-factor sign-in with an authenticator app, and an account page.
Under it, Loom's sessions sign a user in by id, and pages ask for the
signed-in user, typed. Cross-site request forgery is refused for every
route, with nothing to add to forms.

## The auth sub-app

```go
loom.Users(app, store.UserByID)                // the app's user type (below)
app.Mount("/auth", auth.New(auth.Config{
	AppName:  "Tally",
	OnSignUp: store.CreateProfile,             // the app's rows, same transaction
}))                                             // links point to loom.Config.BaseURL
app.UseMailer(mailer)                           // a loom.Mailer (below)
signedIn := app.Group(loom.SignedIn("/auth/sign-in"))
```

```
loom db install github.com/moomerman/loom/auth   # migrations/NNNN_auth_accounts.sql
loom migrate
```

| Page | What it does |
|---|---|
| `/auth/sign-in` | email and password; "Email me a sign-in link" |
| `/auth/sign-up` | email and password; `OnSignUp` runs in the same transaction |
| `/auth/forgot` | mails a link to choose a new password |
| `/auth/reset/{token}` | a new password; signs out every other session |
| `/auth/link/{token}` | a button that signs in (and makes the account, for a new email) |
| `/auth/two-factor` | the code from an authenticator app, after the first step (below) |
| `/auth/account` | change or set the password, two-factor, sign out, sign out everywhere |
| `/auth/account/two-factor` | set up an authenticator: a QR code, then a code back |
| `POST /auth/sign-out` | a plain handler, for a sign-out button in the app's layout |
| `/auth/oauth/{google,github}` | plain handlers: off to the provider and back (below) |

- **Accounts** are rows of `auth_users` (id, email, password hash,
  `email_verified_at`). Give the app's own users table the same id:
  `OnSignUp(ctx, tx, u)` inserts it when an account is made, and seed
  data inserts both (`examples/issues`, migration 0004).
  `signIn.CreateUser(ctx, tx, email, password)` makes one in code.
- **After signing in** the browser goes to the `?next=` page
  `loom.SignedIn` sent, else `Config.AfterSignIn(u)`, else `/`;
  after signing out to `Config.AfterSignOut`.
- **Looks like the app.** The pages use the app's root layout inside a
  card, and their design tokens are defaults (`components.md`): the
  app's `tokens.toml` colours them.
- **Config:** `NoSignUp` (existing accounts only), `NoEmail` (passwords
  only: no links, no resets, no mailer), `LinkFor` (default 30
  minutes).
- **Startup errors:** email on with no mailer is AU901, no
  `loom.Config.BaseURL` outside dev AU902, a provider with no client ID or secret AU906,
  two-factor on (the default) with no `loom.Config.Secret` outside dev
  AU909 (`errors.md`).
- **Security.** Passwords are hashed with PBKDF2-SHA256, 600,000
  iterations (`auth.HashPassword`); a wrong email and a wrong password
  get the same answer in the same time; links are 256-bit, stored
  hashed, work once and expire; only the newest link of each kind for
  an email works, asking again within two minutes sends no second
  mail, and changing the password or signing out everywhere ends every
  link already sent; a link lands on a page with a button,
  so mail scanners that open links use nothing; "forgot password" says
  the same whether or not the account exists; `BaseURL` is never taken
  from the request's `Host`; a sign-in takes five wrong two-factor codes
  before starting again from the password, and ten wrong codes in a row
  on an account, across sign-ins and the account page, lock its codes
  for 15 minutes (`auth_totp.failed`, `locked_until`). **Proving the email wins:** sign-up with a password doesn't
  prove the email, so the first time a link, a reset or a provider
  proves it, the account loses every way in it had before (password,
  providers, authenticator, recovery codes) and its sessions end.
  Someone who signed up with another's address keeps nothing once the
  owner turns up. Sign-up does say when an account exists for an
  email, the usual trade-off for a clear "sign in instead".
- **Rate limits are built in** (`loom.RateLimit`, see `live.md`), so
  mounting auth can't forget them: 120 requests a minute per IP on every
  auth route (which also caps the password-hashing work); password and
  mail events (`sign_in`, `email_link`, `sign_up`, `send`) 20 a minute
  per IP and 5 a minute per email address; reset, link and code events
  20 a minute per IP; on the account page, a new password 10 an hour and
  code checks 5 an hour per user. Over a limit the event is refused
  (RT436) and the card says "Too many attempts". An app can add
  stricter limits around the mount; behind a proxy, set
  `TrustedProxies` so the limits see each client's IP.

## Google and GitHub

```go
var providers []auth.Provider
if id := os.Getenv("GOOGLE_CLIENT_ID"); id != "" {
	providers = append(providers, auth.Google(id, os.Getenv("GOOGLE_CLIENT_SECRET")))
}
if id := os.Getenv("GITHUB_CLIENT_ID"); id != "" {
	providers = append(providers, auth.GitHub(id, os.Getenv("GITHUB_CLIENT_SECRET")))
}
auth.New(auth.Config{Providers: providers}) // and loom.Config{BaseURL: os.Getenv("BASE_URL")}
```

Each provider is a "Continue with Google" button on the sign-in page.
Register the app with the provider, giving it the callback URL
`<BaseURL>/auth/oauth/<name>/callback` (in dev, the address you browse,
e.g. `http://localhost:4000/auth/oauth/google/callback`):

- **Google:** Google Cloud console, APIs & Services, Credentials,
  Create OAuth client ID, Web application, Authorized redirect URIs.
- **GitHub:** Settings, Developer settings, OAuth Apps, New OAuth App,
  Authorization callback URL.

How it works: OAuth's authorization code flow with PKCE, on the
standard library. The state and code verifier live in a ten-minute
cookie scoped to the callback; the provider's id for the person
(`auth_identities`, installed with the accounts) is what signs them in
again, so a changed email at the provider still finds their account.
The first time, the provider's email must be verified: it then signs
in to the account with that email (joining it) or makes one (running
`OnSignUp`; not with `NoSignUp`). A refused code goes back to the
sign-in page with "didn't work" and is AU907 in the log and `loom
errors`, naming the callback URL to register. The account page lists
the providers an account signs in with.

In tests, point a provider's `AuthURL`, `TokenURL` and `UserURL` at an
`httptest` server (`auth/oauth_test.go` has one); tests of the app's
own pages sign in with `loomtest.SignIn`.

## Two-factor sign-in

```go
auth.New(auth.Config{TwoFactor: auth.TwoFactorRequired}) // or Optional (the default), or Off
```

People can add a code from an authenticator app (1Password, Google
Authenticator, Authy) to signing in. `Config.TwoFactor` is the app's
policy:

| Policy | What people see |
|---|---|
| `TwoFactorOptional` (default) | "Set up two-factor sign-in" on the account page; once on, every sign-in asks for a code |
| `TwoFactorRequired` | every sign-in asks for a code; anyone without an authenticator sets one up before they are signed in; it can't be turned off |
| `TwoFactorOff` | never asked for or offered; authenticators already set up are kept for when it is turned back on |

- **Every way in asks:** a password, a sign-in link, a new account, a
  reset password, Google or GitHub. The first step doesn't sign in: it
  stores a challenge and sends the browser to `/auth/two-factor`
  (through `/auth/two-factor/begin/{secret}`, which moves the secret
  into a cookie scoped to the code page), keeping `?next=`. A
  challenge lasts ten minutes and takes five wrong codes, then it is
  deleted and the person starts again from the first step.
- **Setting up** shows a QR code of the `otpauth://` address and the
  key to type by hand; a code typed back turns it on and shows ten
  recovery codes, once. The secret is stored before it is confirmed, so
  a reload or a restart shows the same QR code.
- **Recovery codes** sign in once each in place of a code, for a lost
  phone. The account page says how many are left, and makes new ones
  or turns two-factor off; both take a current code (or a recovery
  code).
- **Codes** are RFC 6238 (SHA-1, six digits, 30 seconds), the kind every
  app supports, on the standard library. One step either side of now is
  accepted for clock drift, and each code works once: a code no newer
  than the last one used is refused.
- **Stored:** `auth_totp` (the secret, encrypted with
  `loom.Config.Secret`, AES-GCM through `app.Seal`: checking a code needs
  it, so it can't be hashed; one stored before the app had a secret is
  encrypted at the next start, and one that won't decrypt is AU908),
  `auth_recovery_codes` (SHA-256 only) and `auth_challenges`
  (SHA-256 of the secret), from auth's migration 0003. An app
  installed before it is RT433 at startup until
  `loom db install github.com/moomerman/loom/auth` and `loom migrate`.
- **Changing the policy** applies at the next sign-in: turning on
  `TwoFactorRequired` leaves existing sessions signed in, so sign
  everyone out (`app.SignOutEverywhere`) to apply it at once.
- **Tests** that sign in with `loomtest.SignIn` skip the code, like
  every first step; tests of the flow read the key from the set-up page
  (`auth/twofactor_test.go`).

## Email

```go
type Mailer interface {
	Send(ctx context.Context, m loom.Mail) error // Mail{To, Subject, Text, HTML}
}
app.UseMailer(m)
```

Wrap your mail service (its HTTP API, or `net/smtp`) in that one
method. `auth` sends from a background job (`auth.SendLink`), so a
failed send is retried and shows in the jobs dashboard; the job makes
the link when it runs, so the secret is never in its args. Under `loom
dev`, an app with no mailer keeps its mail instead: `loom mail` (and the
`mail` MCP tool) shows it, links included. For people, mount the dev
mailbox, which shows the same mail live in the browser, each link a
button:

```go
app.Mount("/dev/mail", devmail.New()) // 404 outside dev, 403 off this machine
```

It is `loom.DevOnly` (`subapps.md`), so the line stays in production,
where the mailbox doesn't exist. `app.SentMail()` reads the kept mail
(the last 50, until a restart) and `loom.MailTopic` announces each one.

## Requiring a signed-in user

```go
loom.Users(app, store.UserByID) // func(ctx context.Context, id string) (*User, error)

app.Live("/login", &LoginPage{}, templates.Login)
signedIn := app.Group(loom.SignedIn("/login"))
signedIn.Live("/", &HomePage{}, templates.Home)
```

```go
func (p *HomePage) Mount(c *loom.Ctx) {
	p.me = loom.User[*User](c)
}
```

- `loom.SignedIn(path)` is an auth check: anyone not signed in is sent
  to `path?next=<the page they asked for>`. Like every auth check it runs
  on the page load and before every event, so a page whose session is
  signed out, in another tab or on another node, ends at its next event.
  The manifest shows it as `auth: loom.SignedIn`.
- `loom.Users` registers the app's user type and its loader, once. The
  `SignedIn` check loads the user; a user the loader can't find
  (`loom.ErrNotFound`) counts as signed out.
- `loom.User[T](c)` returns the user, loaded once per event, in pages,
  layouts' assigns funcs and sub-apps alike. A `T` other than the
  loader's is RT431; no one signed in is RT430.
- `c.UserID()` is the id, or `""`: for pages anyone may see.

## Signing in and out yourself

The `auth` sub-app does this for you. An app with its own sign-in (a
different credential, an SSO callback) signs a user in from a live
page's event handler, once it knows who it is:

```go
func (p *LoginPage) signIn(c *loom.Ctx, payload loom.Payload) {
	u, err := p.store.CheckPassword(c.Context(), payload["email"], payload["password"])
	if err != nil {
		p.error = "Wrong email or password."
		return
	}
	c.SignIn(u.ID, "/")
}

func (p *HomePage) signOut(c *loom.Ctx, _ loom.Payload) { c.SignOut("/login") }
```

A live page can't set a cookie (its events are POSTs answered with
diffs), so `c.SignIn` sends the browser to `/_loom/signin/<token>`,
which sets the cookie and goes on to the sign-in page's `?next=`, or
else to the path given (`"/"` above). The token works once, within a minute. `c.SignOut(next)` ends the
session at once, then sends the browser to `/_loom/signout`, which
clears the cookie and goes to `next`.

From a plain handler (a form post, an OAuth callback), `app.SignIn(w,
r, id)` and `app.SignOut(w, r)` set and clear the cookie directly;
redirect afterwards. `app.SignOutEverywhere(ctx, id)` ends every session
of a user, after a password change, say.

## Sessions

- The cookie, `loom_session`, holds a random token: `HttpOnly`,
  `SameSite=Lax`, `Secure` behind HTTPS (or `X-Forwarded-Proto: https`),
  lasting `Config.SignInFor` (30 days).
- The store holds the token's SHA-256, the user id and the expiry, in
  `loom_user_sessions` when the app has a database (created on first
  use), else in memory. A stored session, unlike a signed cookie, can be
  revoked. Signing in always starts a new session and ends the
  browser's old one.

## Cross-site request forgery

The runtime refuses, with 403 and RT429, any POST, PUT, PATCH or DELETE
that a browser sent from another site, before any handler runs: live
events, `app.Handle` routes, the dev endpoints. Browsers say where a
request came from in `Sec-Fetch-Site` (or `Origin`); a request with
neither isn't from a browser page (curl, a webhook) and is allowed. So
forms need no hidden token. With `SameSite=Lax` cookies and live events
needing the page's unguessable session id, that is the whole story. A
GET never changes anything: `/_loom/signout` only clears the cookie of a
session that already ended. Another subdomain counts as another site.

## Testing

```go
p := loomtest.Mount(t, app, "/", loomtest.SignIn(t, app, "ada"))
```

To test mail, `box := loomtest.Mail(t, app)` is a mailer that keeps
what is sent, and `q.Attach(app)` (before mounting) runs the app's jobs,
auth's among them, in a `loomtest.Jobs` queue:

```go
p.Submit("form", loom.Payload{"email": "ada@example.com"}) // on /auth/forgot
q.Drain(t)                                                  // sends the mail
reset := loomtest.Mount(t, app, box.Link(t))               // follows its link
```

`loomtest.SignIn` starts a session and sends its cookie, so `SignedIn`
lets the page in and `loom.User` loads `"ada"`. To test a sign-in page,
submit its form and check `p.Navigated()` starts with `/_loom/signin/`;
`app.SessionCookie(ctx, id)` gives a cookie for `httptest` requests.
