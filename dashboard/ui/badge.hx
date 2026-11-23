component Badge(label string @preview("3", "New"), tone: :neutral | :accent | :success | :warning | :danger = :neutral)

<style>
  .badge { display: inline-block; padding: 0 token(space.2); border-radius: token(radius.full); font-size: token(text.sm); font-weight: 500; }
  .neutral { background: token(color.subtle); color: token(color.on-subtle); }
  .accent { background: token(color.accent); color: token(color.on-accent); }
  .success { background: token(color.success); color: token(color.on-success); }
  .warning { background: token(color.warning); color: token(color.on-warning); }
  .danger { background: token(color.danger); color: token(color.on-danger); }
</style>
<span class={"badge" tone}>{label}</span>
