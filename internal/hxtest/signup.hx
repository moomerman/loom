component Signup(signup form[SignupInput], people []string, short bool)

<form on:change="validate" on:submit="save">
  <.text_field field={signup.name} label="Name" />
  <.text_field field={if short, signup.age, signup.name} label="Age or name" />
  <label><input type="checkbox" name={signup.agree.name} checked={signup.agree.value == "true"} /> Agree</label>
  <input :for={p <- people} :key={p} name={signup.shares[p].name} value={signup.shares[p].value} />
</form>

component TextField(field field, label string)

<label>{label} <input name={field.name} value={field.value} aria-invalid={if field.error != "", "true", "false"} />
<span class="error" :if={field.error != ""}>{field.error}</span></label>
