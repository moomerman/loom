package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Chrome returns the path of a Chrome or Chromium binary: $LOOM_CHROME,
// or the first of google-chrome, chromium, chromium-browser and chrome
// on the PATH.
func Chrome() (string, error) {
	if p := os.Getenv("LOOM_CHROME"); p != "" {
		return p, nil
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no Chrome found: install Chrome or Chromium, or set LOOM_CHROME to its path")
}

// Width and Height are the browser window's size for screenshots, in CSS
// pixels. Taller content is cut off.
const (
	Width  = 800
	Height = 1200
)

// Screenshot renders an HTML document in headless Chrome and returns the
// image, trimmed to its content (anything that isn't the background
// color) plus a margin. It runs Chrome's own --screenshot mode, so it
// needs nothing but the binary.
func Screenshot(ctx context.Context, chrome, document string) (image.Image, error) {
	// Headless Chrome now and then starts and never takes the
	// screenshot (seen on busy CI machines), so a run that times out is
	// tried once more, in a fresh profile.
	img, err := screenshot(ctx, chrome, document, 20*time.Second)
	if errors.Is(err, errTimedOut) && ctx.Err() == nil {
		img, err = screenshot(ctx, chrome, document, 30*time.Second)
	}
	return img, err
}

var errTimedOut = errors.New("timed out")

// screenshot is one run of Chrome, given timeout to take the screenshot.
func screenshot(ctx context.Context, chrome, document string, timeout time.Duration) (image.Image, error) {
	dir, err := os.MkdirTemp("", "loom-preview-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	page := filepath.Join(dir, "page.html")
	shot := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(page, []byte(document), 0o644); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome,
		"--headless", "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
		"--no-first-run", "--disable-extensions", "--user-data-dir="+filepath.Join(dir, "profile"),
		// Nothing but the page: no update checks, sync or other background
		// work, which on a busy CI machine kept Chrome past the timeout
		// without a screenshot. (Puppeteer's defaults.)
		"--disable-background-networking", "--disable-component-update", "--disable-sync",
		"--disable-default-apps", "--no-default-browser-check", "--metrics-recording-only",
		"--disable-features=Translate,OptimizationHints,MediaRouter,DialMediaRouteProvider",
		// Take the screenshot once the page has had 5 seconds of virtual
		// time, rather than waiting for the network to go quiet.
		"--virtual-time-budget=5000", "--run-all-compositor-stages-before-draw",
		// Steadier text rendering across machines.
		"--force-device-scale-factor=1", "--font-render-hinting=none", "--disable-lcd-text",
		fmt.Sprintf("--window-size=%d,%d", Width, Height),
		"--screenshot="+shot, "file://"+page)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("running %s: %v", chrome, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// Chrome sometimes writes the screenshot and then doesn't exit (seen
	// on CI under load), so the screenshot is taken as soon as the file
	// is a whole PNG, and Chrome is stopped.
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			img, perr := readPNG(shot)
			switch {
			case perr == nil:
				return Trim(img, 8), nil
			case ctx.Err() == context.DeadlineExceeded:
				return nil, fmt.Errorf("running %s: %w after %v\n%s", chrome, errTimedOut, timeout, out.String())
			case err != nil:
				return nil, fmt.Errorf("running %s: %v\n%s", chrome, err, out.String())
			default:
				return nil, fmt.Errorf("%s wrote no screenshot: %v\n%s", chrome, perr, out.String())
			}
		case <-tick.C:
			if img, err := readPNG(shot); err == nil {
				cmd.Process.Kill()
				<-done
				return Trim(img, 8), nil
			}
		}
	}
}

// readPNG decodes the PNG file at path; a file Chrome is still writing
// fails to decode.
func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// Trim crops img to the pixels that differ from its bottom-right pixel
// (the page background), plus margin pixels on every side.
func Trim(img image.Image, margin int) image.Image {
	b := img.Bounds()
	bg := img.At(b.Max.X-1, b.Max.Y-1)
	r := image.Rectangle{Min: b.Max, Max: b.Min}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if !sameColor(img.At(x, y), bg) {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if r.Empty() {
		r = image.Rect(b.Min.X, b.Min.Y, b.Min.X+1, b.Min.Y+1)
	}
	r = image.Rect(r.Min.X-margin, r.Min.Y-margin, r.Max.X+margin, r.Max.Y+margin).Intersect(b)
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)
	return out
}

func sameColor(a, b color.Color) bool {
	r1, g1, b1, a1 := a.RGBA()
	r2, g2, b2, a2 := b.RGBA()
	return r1 == r2 && g1 == g2 && b1 == b2 && a1 == a2
}
