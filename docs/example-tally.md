# Example: Tally, shared expenses

`examples/tally/` is a Splitwise-style app: groups of people who split
bills, see who owes whom, settle up and get reminded. An agent built it
as a newcomer would, from `llms.txt`, `loom new` and the CLI, to find
where Loom helps and where it hurts. Read `example-issues.md` first;
this page covers what Tally does differently.

```
examples/tally/
  app.go              Setup, routes, sign-in, the app layout's assigns
  store.go            every query and write; balances and settle-up maths
  rows.go             the rows templates render; money and date formatting
  jobs.go             Notify, Remind (unique per hour), PostRecurring (cron)
  layouts.hx          Root and Shell (sidebar with each group's balance)
  components.hx       Avatar, Money, CategoryIcon, Panel, ToastBar, ...
  dashboard.go/.hx    /                       balances overall, groups, activity, new group
  group.go/.hx        /groups/{id}            expenses by month, balances, settle up, add expense
  expense.go/.hx      /groups/{id}/expenses/{eid}   one expense, its split, delete
  activity.go/.hx     /activity               notifications from the jobs
  login.go/.hx        /login                  pick someone to be
  components/         starter kit copies (button, input, select, dialog)
  migrations/         the schema (every table tracked) and example data
```

Run it with `loom migrate --dir examples/tally/migrations` and `go run
./examples/tally/cmd/tally`, then sign in as Sam, who sees the jobs
dashboard at `/admin/jobs`.

## Watching, not subscribing

Every signed-in route watches the tables it reads (`watched` in
`app.go`), and every page reads its data in `Assigns`, so a change from
another person, a job or psql re-renders it. There are no topics and no
`HandleInfo`. The app layout's func reads the sidebar's balances and
unread count the same way, so they update too. The cost: `Assigns`
queries on every render, including after events that only open a
dialog.

## A form the server computes

The add-expense dialog is a `form.Form[ExpenseInput]` (`forms.md`): its
`on:change="expense_form"` sends all its named fields to
`p.expense.Change`, and `Assigns` works out each person's share from
`p.expense.Value()`, or what is left to assign when splitting by exact
amounts. The split updates as the person types, with no client code.
`ExpenseInput.Validate` names each error by its field, and the kit's
input and select show them; who shares the expense and the exact
amounts are map fields, `expense.among[person.id]` and
`expense.exact[person.id]`. Both split lists stay in the form, one
hidden, since an unchecked box isn't sent. The settle-up dialog is a
`form.Form[SettleInput]` the same way.

## Restart-safe input

The group's search box is a named field in a `<form on:change>`, so
after a restart the browser restores it and sends it again, and the
list stays filtered. An input outside a form, or without a name, would
come back empty. A dialog's open state is page state, so a restart
closes it.

## Jobs

`Notify` writes a notification for each other member; its `(job_id,
person_id)` key makes a second run a no-op. `Remind` is enqueued with
`jobs.Unique(time.Hour)`, and the page says when it was a duplicate.
`PostRecurring` runs daily on cron and claims each monthly bill with
an `update ... where last_posted < $month` in the same transaction as
the expense, so running it twice posts once (`loom cron run 1` to try).

## Testing

`tally_test.go` drives each page on Postgres with the test's job
queue, as issues does. `TestFlowAddAnExpense` is a flow test whose
steps are in `testdata/flows/`, and `p.TableChanged` stands in for
another person's commit.
