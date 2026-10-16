package diag

// Code is a stable identifier for a kind of error. Codes never change
// meaning once released; retired codes are not reused.
//
// Prefixes: LX = lexing, PA = parsing, TC = type checking, ST = styles
// and design tokens.
type Code string

// Lexing errors: the source can't be split into markup and expression tokens.
const (
	LX101 Code = "LX101" // stray '}' in text
	LX102 Code = "LX102" // HTML comment never closed
	LX103 Code = "LX103" // <! declaration never closed
	LX104 Code = "LX104" // malformed closing tag
	LX105 Code = "LX105" // open tag missing its '>'
	LX106 Code = "LX106" // unexpected character inside a tag
	LX107 Code = "LX107" // attribute has '=' but no value
	LX108 Code = "LX108" // quoted attribute value never closed
	LX109 Code = "LX109" // attribute value neither quoted nor in braces
	LX110 Code = "LX110" // '{' never closed
	LX111 Code = "LX111" // <script> or <style> never closed
	LX112 Code = "LX112" // single-quoted string in an expression
	LX113 Code = "LX113" // unexpected character in an expression
	LX114 Code = "LX114" // string never closed
	LX115 Code = "LX115" // unknown escape sequence
	LX116 Code = "LX116" // EEx tag like <%= ... %>
	LX117 Code = "LX117" // '@' before an assign name
)

// Parsing errors: the tokens don't form a valid template.
const (
	PA201 Code = "PA201" // markup before the first component declaration
	PA202 Code = "PA202" // no component declaration
	PA203 Code = "PA203" // tag never closed
	PA204 Code = "PA204" // component declared twice
	PA205 Code = "PA205" // invalid component call name
	PA206 Code = "PA206" // invalid slot name
	PA207 Code = "PA207" // slot outside a component call
	PA208 Code = "PA208" // invalid HTML tag name
	PA209 Code = "PA209" // attribute set twice
	PA210 Code = "PA210" // directive on a slot
	PA211 Code = "PA211" // directive without a braced expression
	PA212 Code = "PA212" // unknown directive
	PA213 Code = "PA213" // malformed on:event binding
	PA214 Code = "PA214" // :key without :for
	PA215 Code = "PA215" // closing tag on a void element
	PA216 Code = "PA216" // closing tag with no opening tag
	PA217 Code = "PA217" // closing tag while an inner tag is still open
	PA218 Code = "PA218" // closing tag matches no open tag
	PA219 Code = "PA219" // empty expression {}
	PA220 Code = "PA220" // assignment in an expression
	PA221 Code = "PA221" // unexpected token after an expression
	PA222 Code = "PA222" // expression ends too early
	PA223 Code = "PA223" // expected a value
	PA224 Code = "PA224" // '<-' outside :for
	PA225 Code = "PA225" // calling something other than a helper
	PA226 Code = "PA226" // expected punctuation, e.g. ')' or ']'
	PA227 Code = "PA227" // malformed inline if
	PA228 Code = "PA228" // malformed :for
	PA229 Code = "PA229" // malformed component declaration
	PA230 Code = "PA230" // component name not capitalized
	PA231 Code = "PA231" // parameter declared twice
	PA232 Code = "PA232" // parameter without a type
	PA233 Code = "PA233" // invalid type
	PA234 Code = "PA234" // invalid number
	PA235 Code = "PA235" // phx-* attribute
	PA236 Code = "PA236" // key= instead of :key
	PA237 Code = "PA237" // Elixir's <> instead of + for strings
	PA238 Code = "PA238" // malformed enum, default or event parameter
	PA239 Code = "PA239" // malformed import line
	PA240 Code = "PA240" // malformed behavior declaration
	PA241 Code = "PA241" // malformed use: attribute
	PA242 Code = "PA242" // malformed @preview
)

// Type checking errors: the template is well formed but doesn't fit its
// types, its package or the components it calls.
const (
	TC301 Code = "TC301" // unknown name
	TC302 Code = "TC302" // unknown field
	TC303 Code = "TC303" // field access on a value that isn't a struct
	TC304 Code = "TC304" // unknown or invalid parameter type
	TC305 Code = "TC305" // operator applied to the wrong types
	TC306 Code = "TC306" // condition isn't a bool
	TC307 Code = "TC307" // inline if branches have different types
	TC308 Code = "TC308" // invalid indexing
	TC309 Code = "TC309" // :for over something that isn't a list
	TC310 Code = "TC310" // unknown helper
	TC311 Code = "TC311" // wrong number of helper arguments
	TC312 Code = "TC312" // wrong helper argument type
	TC313 Code = "TC313" // value can't be rendered as text
	TC314 Code = "TC314" // value can't be used as an attribute
	TC315 Code = "TC315" // unknown component
	TC316 Code = "TC316" // component has no such parameter
	TC317 Code = "TC317" // missing component parameter
	TC318 Code = "TC318" // component argument has the wrong type
	TC319 Code = "TC319" // slot not declared by the component
	TC320 Code = "TC320" // named slot given twice
	TC321 Code = "TC321" // attributes on a named slot
	TC322 Code = "TC322" // field's Go type isn't usable in templates
	TC323 Code = "TC323" // :key isn't a string or int
	TC324 Code = "TC324" // slot used as a value
	TC325 Code = "TC325" // component declared in two files
	TC326 Code = "TC326" // invalid helper declaration
	TC327 Code = "TC327" // event binding on a component call
	TC328 Code = "TC328" // name already defined
	TC329 Code = "TC329" // root layout without <.loom_scripts />
	TC330 Code = "TC330" // root layout doesn't render its inner_block
	TC331 Code = "TC331" // root layout isn't a complete HTML document
	TC332 Code = "TC332" // a root layout builtin (<.live_title>, <.loom_scripts />, ...) used wrongly
	TC333 Code = "TC333" // a root layout builtin outside a root layout
	TC334 Code = "TC334" // component name reserved for a builtin
	TC335 Code = "TC335" // invalid path() or asset() argument
	TC336 Code = "TC336" // warning: bare absolute URL in a package's templates
	TC337 Code = "TC337" // root layout without <.loom_styles />
	TC338 Code = "TC338" // value isn't one of an enum's values
	TC339 Code = "TC339" // default doesn't fit the parameter's type
	TC340 Code = "TC340" // event parameter misused: forwarding or val-* without one
	TC341 Code = "TC341" // class names joined with + instead of listed
	TC342 Code = "TC342" // import can't be resolved, is unused, or clashes
	TC343 Code = "TC343" // behavior declaration or module problem
	TC344 Code = "TC344" // use: unknown behavior, unknown or missing prop, wrong prop type
	TC345 Code = "TC345" // element doesn't meet a behavior's requires
	TC346 Code = "TC346" // on:x is neither a DOM event nor emitted by the element's behavior
	TC347 Code = "TC347" // @preview on a parameter that can't take one, or of the wrong type
	TC348 Code = "TC348" // :for over records with no :key (warning)
	TC349 Code = "TC349" // unknown optimistic:* hint
	TC350 Code = "TC350" // optimistic:* hint with the wrong kind of value
	TC351 Code = "TC351" // optimistic:* hint on a tag with no on:* event
	TC352 Code = "TC352" // form[T] whose input has a field a form can't decode, or no exported fields
	TC353 Code = "TC353" // inline script the Content-Security-Policy blocks (warning)
	TC354 Code = "TC354" // root layout without <.live_meta />
)

// Style errors: a component's <style> block or the app's tokens.toml.
const (
	ST701 Code = "ST701" // tokens.toml syntax error
	ST702 Code = "ST702" // token value doesn't fit its group
	ST703 Code = "ST703" // unknown token, or token() without tokens.toml
	ST704 Code = "ST704" // raw value where a token is required
	ST705 Code = "ST705" // unsafe style block (warning)
	ST706 Code = "ST706" // X / on-X color pair below WCAG AA contrast (warning)
	ST707 Code = "ST707" // style block in the wrong place or with attributes
	ST708 Code = "ST708" // CSS syntax error
	ST709 Code = "ST709" // :global(...) outside a root layout
	ST710 Code = "ST710" // token from the wrong group for the property
	ST711 Code = "ST711" // a theme in tokens.toml with a bad name or an unknown token
	ST712 Code = "ST712" // data-theme names no theme, or isn't written out
)

// Codes lists every code, for documentation checks.
var Codes = []Code{
	LX101, LX102, LX103, LX104, LX105, LX106, LX107, LX108, LX109, LX110,
	LX111, LX112, LX113, LX114, LX115, LX116, LX117,
	PA201, PA202, PA203, PA204, PA205, PA206, PA207, PA208, PA209, PA210,
	PA211, PA212, PA213, PA214, PA215, PA216, PA217, PA218, PA219, PA220,
	PA221, PA222, PA223, PA224, PA225, PA226, PA227, PA228, PA229, PA230,
	PA231, PA232, PA233, PA234, PA235, PA236, PA237, PA238, PA239, PA240, PA241, PA242,
	TC301, TC302, TC303, TC304, TC305, TC306, TC307, TC308, TC309, TC310,
	TC311, TC312, TC313, TC314, TC315, TC316, TC317, TC318, TC319, TC320,
	TC321, TC322, TC323, TC324, TC325, TC326, TC327, TC328, TC329, TC330,
	TC331, TC332, TC333, TC334, TC335, TC336, TC337, TC338, TC339, TC340,
	TC341, TC342, TC343, TC344, TC345, TC346, TC347, TC348, TC349, TC350,
	TC351, TC352, TC353, TC354,
	ST701, ST702, ST703, ST704, ST705, ST706, ST707, ST708, ST709, ST710, ST711, ST712,
}
