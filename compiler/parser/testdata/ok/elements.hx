component Elements(title string, disabled bool)

<!DOCTYPE html>
<!-- a comment with {braces} and <tags> -->
<div id="main" class='single' data-x={title} hidden>
  <br>
  <br/>
  <img src="/a.png" alt={title} />
  <button disabled={disabled} on:click="save" val-kind="draft" val-n={1}>Save</button>
  <div />
  <my-element some-attr="x"></my-element>
</div>
<script>
  if (a < b) { console.log("{not an expression}") }
</script>
<style>p { color: red }</style>
