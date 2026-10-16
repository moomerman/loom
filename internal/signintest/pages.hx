component Login(error string)

<h1>Sign in</h1>
<form on:submit="sign_in">
  <label>Name <input name="name" /></label>
  <label>Password <input name="password" type="password" /></label>
  <button type="submit">Sign in</button>
</form>
<p :if={error != ""} class="error">{error}</p>

component Home(name string)

<p class="user">Signed in as {name}</p>
<button on:click="ping">Ping</button>
<button on:click="sign_out">Sign out</button>
