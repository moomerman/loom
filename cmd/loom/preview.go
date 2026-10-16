package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/preview"
)

// loom preview lists the app's preview variants, and loom preview
// screenshot renders them to PNG files in headless Chrome, from the
// running app's gallery pages (see docs/components.md).

func runPreview(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "screenshot" {
		return runScreenshot(args[1:], stdout, stderr)
	}
	return introspect("preview", "preview [--json] [--url URL] | preview screenshot ...", args, stdout, stderr, 0, func(c *devClient, _ []string) ([]byte, func(io.Writer) error, error) {
		data, err := c.do("GET", "/_loom/dev/previews", nil)
		return data, func(w io.Writer) error {
			var list []loom.PreviewInfo
			if err := json.Unmarshal(data, &list); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "COMPONENT\tVARIANTS")
			for _, p := range list {
				v := strings.Join(p.Variants, ", ")
				if p.NoPreview != "" {
					v = "none: " + p.NoPreview
				}
				fmt.Fprintf(tw, "%s\t%s\n", p.Component, v)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintf(w, "\nSee them at %s/_loom/preview\n", c.base)
			return nil
		}, err
	})
}

// Screenshot describes a PNG written by loom preview screenshot.
type Screenshot struct {
	Component string `json:"component"`
	Variant   string `json:"variant"` // "" for every variant in one image
	Theme     string `json:"theme"`
	File      string `json:"file"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

func runScreenshot(args []string, stdout, stderr io.Writer) int {
	var variant, theme, out string
	return introspectFlags("preview screenshot", "preview screenshot [--json] [--url URL] [--variant NAME] [--theme NAME] [--out DIR] [COMPONENT]",
		args, stdout, stderr, -1, func(fs *flag.FlagSet) {
			fs.StringVar(&variant, "variant", "", "one variant, by name (default: every variant, in one image)")
			fs.StringVar(&theme, "theme", "", "light, dark or a theme from tokens.toml (default: each)")
			fs.StringVar(&out, "out", "previews", "the directory to write PNG files to")
		}, func(c *devClient, args []string) ([]byte, func(io.Writer) error, error) {
			if len(args) > 1 {
				return nil, nil, fmt.Errorf("%w: name one component, or none for all", errNoApp)
			}
			component := ""
			if len(args) == 1 {
				component = args[0]
			}
			shots, err := screenshots(c, component, variant, theme, func(s Screenshot, png []byte) error {
				s.File = filepath.Join(out, s.File)
				if err := os.MkdirAll(out, 0o755); err != nil {
					return err
				}
				return os.WriteFile(s.File, png, 0o644)
			})
			for i := range shots {
				shots[i].File = filepath.Join(out, shots[i].File)
			}
			data, _ := json.Marshal(map[string]any{"screenshots": shots})
			return append(data, '\n'), func(w io.Writer) error {
				for _, s := range shots {
					fmt.Fprintf(w, "wrote %s (%dx%d)\n", s.File, s.Width, s.Height)
				}
				return nil
			}, err
		})
}

var unsafeFile = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// screenshots renders the previews of a component (or every component
// with previews) in headless Chrome, one image per theme, and passes each
// PNG to save. Its File is a name to save it under.
func screenshots(c *devClient, component, variant, theme string, save func(Screenshot, []byte) error) ([]Screenshot, error) {
	data, err := c.do("GET", "/_loom/dev/previews", nil)
	if err != nil {
		return nil, err
	}
	var list []loom.PreviewInfo
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("%w: %v", errNoApp, err)
	}
	var targets []loom.PreviewInfo
	var names []string
	for _, p := range list {
		short := p.Component[strings.LastIndex(p.Component, ".")+1:]
		if component == "" && p.NoPreview == "" || component == p.Component || component == short {
			targets = append(targets, p)
		}
		names = append(names, p.Component)
	}
	switch {
	case component != "" && len(targets) == 0:
		return nil, fmt.Errorf("%w: no component %q; components are %s", errNoApp, component, strings.Join(names, ", "))
	case component != "" && len(targets) > 1:
		return nil, fmt.Errorf("%w: component name %q is ambiguous: use a qualified name", errNoApp, component)
	case component != "" && targets[0].NoPreview != "":
		return nil, fmt.Errorf("%w: %s has no previews: %s", errNoApp, targets[0].Component, targets[0].NoPreview)
	}
	for _, p := range targets {
		if variant != "" && !contains(p.Variants, variant) {
			return nil, fmt.Errorf("%w: %s has no variant %q; its variants are %q", errNoApp, p.Component, variant, p.Variants)
		}
		if theme != "" && !contains(themesOf(p), theme) {
			return nil, fmt.Errorf("%w: %s has no theme %q; its themes are %q", errNoApp, p.Component, theme, themesOf(p))
		}
	}
	chrome, err := preview.Chrome()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoApp, err)
	}
	shots := []Screenshot{}
	for _, p := range targets {
		themes := themesOf(p)
		if theme != "" {
			themes = []string{theme}
		}
		for _, th := range themes {
			q := url.Values{"theme": {th}}
			file := p.Component
			if variant != "" {
				q.Set("variant", variant)
				file += "." + unsafeFile.ReplaceAllString(variant, "_")
			}
			page, err := c.do("GET", "/_loom/preview/"+url.PathEscape(p.Component)+"?"+q.Encode(), nil)
			if err != nil {
				return shots, err
			}
			img, err := preview.Screenshot(context.Background(), chrome, string(page))
			if err != nil {
				return shots, fmt.Errorf("%w: %v", errNoApp, err)
			}
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				return shots, err
			}
			s := Screenshot{Component: p.Component, Variant: variant, Theme: th, File: file + "." + th + ".png", Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}
			if err := save(s, buf.Bytes()); err != nil {
				return shots, fmt.Errorf("%w: %v", errNoApp, err)
			}
			shots = append(shots, s)
		}
	}
	return shots, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// themesOf returns the themes a component's previews are shown in; an
// older app's list has none, so light and dark.
func themesOf(p loom.PreviewInfo) []string {
	if len(p.Themes) == 0 {
		return preview.Modes
	}
	return p.Themes
}
