// Package client embeds loom.js, the browser runtime for live pages.
package client

import _ "embed"

// JS is the source of loom.js. It is hand-written and served as is: no
// build step.
//
//go:embed loom.js
var JS []byte

// Dev is the source of dev.js, which the server appends to loom.js in
// dev mode: it reports the page's script errors to /_loom/dev/errors.
//
//go:embed dev.js
var Dev []byte

// Navigate is the source of navigate.js, which navigates between pages
// with the same root layout by swapping the live root
// (docs/layouts.md). Every page loads it after loom.js.
//
//go:embed navigate.js
var Navigate []byte

// Behaviors is the source of behaviors.js, the ES module that runs
// behaviors' lifecycles on pages that attach any (docs/components.md).
// Like loom.js, it is hand-written and served as is.
//
//go:embed behaviors.js
var Behaviors []byte
