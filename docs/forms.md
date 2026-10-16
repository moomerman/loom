# Forms

A form on a live page is a Go struct, its input: one field per form
field. The page keeps a `form.Form[Input]`, which holds what the person
typed, decodes it into the struct, validates it and remembers each
field's error. The template binds inputs to the form's fields by name,
and the compiler checks every name:

| Seam | Written as | Checked by |
|---|---|---|
| template to input struct | `<.input field={expense.amount} />` | `loom check` (TC302, did-you-mean) |
| page to template | `Expense: p.expense` in Assigns | `go build`: the param is `form.Form[ExpenseInput]` |
| validation to field | `e.Add(&in.Amount, "...")` | `go build`: a pointer to the field |
| input type | `form[ExpenseInput]` | `loom check` (TC352), and startup (FM801, FM802) |

The flow: the form's `on:change` validates as the person
types and shows the errors of the fields they have changed; its
`on:submit` shows every error, or hands the decoded input to the code
that saves it.

## The input

```go
// SettleInput is the record-a-payment form.
type SettleInput struct {
	From   string
	To     string
	Amount string // dollars as typed; parsed to cents in Validate and on save
}

// Validate runs after every change and submit. A field keeps its first error.
func (in *SettleInput) Validate(e *form.Errors) {
	if in.From == in.To {
		e.Add(&in.To, "Pick two different people.")
	}
	if _, ok := parseCents(in.Amount); !ok {
		e.Add(&in.Amount, "Enter an amount, like 40.00.")
	}
}
```

- Fields are named as everywhere in Loom: Go `PaidBy` is `paid_by`, in
  templates and as the input's `name`, so in the payload too.
- Field types: `string`, `int`, `int64`, `float64`, `bool`,
  `map[string]string` and `map[string]bool`. Text that doesn't parse as
  an int or float is kept as typed, with the error "Enter a whole
  number." or "Enter a number."; parse anything richer (money, dates)
  from a string in Validate.
- `Validate` must have a pointer receiver, so `&in.To` is the form's own
  field (FM802 otherwise). `e.Add` takes a pointer to a field (FM803 for
  anything else); `e.AddKey(&in.Exact, key, msg)` is one map entry's
  error; `e.Has(&in.Amount)` tells whether a field already has one.
- The input struct is declared in the template's package, as other
  structs templates use are.

## The page

```go
type GroupPage struct {
	settling bool
	settle   form.Form[SettleInput]
}

func (p *GroupPage) openSettle(c *loom.Ctx, _ loom.Payload) {
	p.settling, p.settle = true, form.New(SettleInput{From: p.me.ID})
}

func (p *GroupPage) settleChange(c *loom.Ctx, payload loom.Payload) {
	p.settle.Change(payload)
}

func (p *GroupPage) saveSettle(c *loom.Ctx, payload loom.Payload) {
	in, ok := p.settle.Submit(payload)
	if !ok {
		return // every error now shows
	}
	if err := p.app.store.Settle(c.Context(), in.From, in.To, in.Amount); err != nil {
		c.Fail(err)
		p.settle.AddError(&in.Amount, "Couldn't record the payment; try again.")
		return
	}
	p.settling = false
}

func (p *GroupPage) Assigns(c *loom.Ctx) GroupViewAssigns {
	return GroupViewAssigns{Settling: p.settling, Settle: p.settle}
}
```

- `form.New(input)` starts a form with that input, showing no errors.
  Make one in Mount or in the handler that opens the form; the zero
  `Form` is an empty form.
- `Change(payload)` decodes and validates. A field's error shows once
  its value has changed; a field left alone stays quiet.
- `Submit(payload)` decodes, validates and shows every error. It
  returns the input and `true` when there are none. The input is the
  form's own, so `AddError(&in.Field, msg)` can add what only the save
  knew, a unique name taken (`loom.ErrUniqueViolation`) say.
- `Value()` is the input as last decoded, for what the page computes
  from it as the person types (a total, a preview); `Valid()` says
  whether it has errors, shown or not.
- Changing the form replaces its maps, so the copy of the page each
  page load gets never shares a form with another.

## The template

```heex
component GroupView(settling bool, settle form[SettleInput], people []Option)

<form on:change="settle_change" on:submit="save_settle">
  <.select field={settle.from} label="Who paid">
    <option :for={o <- people} :key={o.value} value={o.value} selected={o.value == settle.from.value}>{o.label}</option>
  </.select>
  <.input field={settle.amount} label="Amount ($)" />
  <.button label="Record payment" type="submit" />
</form>
```

- `form[Input]` is a parameter type; `settle.amount` is a `field`, with
  `name`, `value` and `error`, all strings (a bool field's value is
  `"true"` or `"false"`). A name that isn't one of the input's exported
  fields is TC302 with a did-you-mean.
- The kit's `input`, `select`, `textarea` and `checkbox` take
  `field={...}` for their name, value (or checked) and error, in place
  of those parameters, which remain for inputs outside a form. Your own
  components take a `field` parameter the same way; a call may leave it
  out, giving an empty field.
- Plain markup uses the field's parts: `<input name={settle.amount.name}
  value={settle.amount.value} />`. Passing the field itself to `name=` is
  TC314, with that fix.
- A map field has an entry per key: `expense.exact[person.id]` is the
  field named `exact[sam]`.
- Put `on:change` and `on:submit` on the `<form>`, so each event sends
  every named field. An unchecked checkbox isn't sent, so a bool field
  missing from the payload is false: keep a checkbox in the form while
  its value matters, hidden (`hidden={...}`) rather than left out with
  `:if`.

## Restarts, tests, dev tools

Inputs are named fields of a form with `on:change`, so after a restart
the browser restores them and sends the change, and the form is as it
was (`live.md`, "Restarts"). In tests, `p.Submit(selector, fields)`
and `p.Event("settle_change", fields)` send payloads as the browser
would (Submit also checks each name is in the form's markup), and
assertions read the errors from the markup:

```go
p.Submit("form.settle", loom.Payload{"from": "sam", "to": "sam", "amount": ""})
p.AssertText("label.field", "Pick two different people.")
```

`loom inspect` shows a form as each field's value and shown error. Tag
a field `loom:"secret"` (a password, a code) and it shows `"[secret]"`,
or `""` while empty; the same tag hides any field of a page's assigns.
`examples/tally` (`group.go`, `group.hx`) has two forms, one with map
fields for a split among people.
