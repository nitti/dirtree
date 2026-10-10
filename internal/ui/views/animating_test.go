package views

import (
	"context"
	"testing"
	"time"

	"github.com/nitti/dirtree/internal/find"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// Tests for each view's Animating method, which drives the main loop's
// choice between its idle and animation tick cadences (SPEC.md §6.2).
// Flash timestamps are synthetic, per §5.3's elapsed-time-driven
// testing discipline, rather than real sleeps.

var (
	animNow        = time.Now()
	animFreshFlash = animNow.Add(-canvas.FlashDuration / 2)
	animStaleFlash = animNow.Add(-canvas.FlashDuration)
)

func TestPreviewAnimating(t *testing.T) {
	tests := []struct {
		name  string
		setup func(v *Preview, files *openfiles.List)
		want  bool
	}{
		{name: "no displayed entry", setup: func(*Preview, *openfiles.List) {}, want: false},
		{name: "fresh top bump flash", setup: func(v *Preview, _ *openfiles.List) { v.TopBumpFlashStart = animFreshFlash }, want: true},
		{name: "fresh bottom bump flash", setup: func(v *Preview, _ *openfiles.List) { v.BottomBumpFlashStart = animFreshFlash }, want: true},
		{name: "fresh goto-blocked flash", setup: func(v *Preview, _ *openfiles.List) { v.GotoBlockedFlashStart = animFreshFlash }, want: true},
		{name: "expired bump flash", setup: func(v *Preview, _ *openfiles.List) { v.TopBumpFlashStart = animStaleFlash }, want: false},
		{
			name: "idle displayed entry",
			setup: func(_ *Preview, files *openfiles.List) {
				files.Entries = []*openfiles.Entry{{Path: "/a"}}
				files.Displayed = 0
			},
			want: false,
		},
		{
			name: "displayed entry with find scan running",
			setup: func(_ *Preview, files *openfiles.List) {
				files.Entries = []*openfiles.Entry{{Path: "/a", FindScan: &find.Scan{}}}
				files.Displayed = 0
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := openfiles.New()
			v := &Preview{Shared: &Shared{Files: files}}
			tt.setup(v, files)
			if got := v.Animating(animNow); got != tt.want {
				t.Errorf("Animating() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBrowserAnimating(t *testing.T) {
	tests := []struct {
		name         string
		flashStart   time.Time
		errorFlashes map[string]time.Time
		want         bool
	}{
		{name: "nothing flashing", want: false},
		{name: "fresh open flash", flashStart: animFreshFlash, want: true},
		{name: "expired open flash", flashStart: animStaleFlash, want: false},
		{name: "fresh error flash", errorFlashes: map[string]time.Time{"/a": animStaleFlash, "/b": animFreshFlash}, want: true},
		{name: "expired error flashes", errorFlashes: map[string]time.Time{"/a": animStaleFlash}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &Browser{FlashStart: tt.flashStart, ErrorFlashes: tt.errorFlashes}
			if got := v.Animating(animNow); got != tt.want {
				t.Errorf("Animating() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQuickOpenAnimating(t *testing.T) {
	tests := []struct {
		name       string
		flashStart time.Time
		want       bool
	}{
		{name: "nothing flashing", want: false},
		{name: "fresh error flash", flashStart: animFreshFlash, want: true},
		{name: "expired error flash", flashStart: animStaleFlash, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &QuickOpen{ErrorFlashStart: tt.flashStart}
			if got := v.Animating(animNow); got != tt.want {
				t.Errorf("Animating() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSearchAnimating(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	tests := []struct {
		name string
		v    Search
		want bool
	}{
		{name: "idle", want: false},
		{name: "scan in flight", v: Search{Cancel: cancel}, want: true},
		{name: "fresh open flash", v: Search{FlashStart: animFreshFlash}, want: true},
		{name: "fresh error flash", v: Search{ErrorFlashStart: animFreshFlash}, want: true},
		{name: "expired flashes", v: Search{FlashStart: animStaleFlash, ErrorFlashStart: animStaleFlash}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.Animating(animNow); got != tt.want {
				t.Errorf("Animating() = %v, want %v", got, tt.want)
			}
		})
	}
}
