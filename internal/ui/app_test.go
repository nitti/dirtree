package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nitti/dirtree/internal/asyncjob"
	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/ui/views"
)

// TestHandleKeyQQuitsImmediately guards tap-to-quit (SPEC.md §7): a
// single `q` at the primary preview view quits, with no hold or
// confirmation, since the session is restored on the next start
// (§6.5).
func TestHandleKeyQQuitsImmediately(t *testing.T) {
	a := newTestApp(t, 60, 15)
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	if !a.quit {
		t.Fatal("a single `q` did not quit")
	}
}

// TestHandleKeyQDoesNotQuitFromOverlay guards that `q` only quits from
// the primary preview view with no overlay active (SPEC.md §7): in an
// overlay it's handled (or typed) there instead.
func TestHandleKeyQDoesNotQuitFromOverlay(t *testing.T) {
	a := newTestApp(t, 60, 15)
	a.overlay = views.OverlayOpenFiles
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	if a.quit {
		t.Fatal("`q` quit from the open-files overlay")
	}
}

// TestTickInterval guards the main loop's two-cadence tick (SPEC.md
// §6.2): the fast animation cadence while anything on screen is
// time-driven, the idle resize-poll cadence otherwise. The index is
// built by hand rather than via index.Start so its done state is
// deterministic, and it finishes "instantly", under the badge's
// perceptibility threshold, so the badge itself stays hidden once done.
func TestTickInterval(t *testing.T) {
	tests := []struct {
		name      string
		indexDone bool
		setup     func(a *App)
		want      time.Duration
	}{
		{name: "idle", indexDone: true, setup: func(*App) {}, want: resizePollInterval},
		{name: "index still building", indexDone: false, setup: func(*App) {}, want: animationFrameInterval},
		{name: "toast showing", indexDone: true, setup: func(a *App) { a.showToast("x reloaded") }, want: animationFrameInterval},
		{name: "preview flash", indexDone: true, setup: func(a *App) { a.Preview.TopBumpFlashStart = time.Now() }, want: animationFrameInterval},
		{
			name:      "flash on a view that isn't showing",
			indexDone: true,
			setup:     func(a *App) { a.Browser.FlashStart = time.Now() },
			want:      resizePollInterval,
		},
		{
			name:      "flash on the active overlay",
			indexDone: true,
			setup: func(a *App) {
				a.overlay = views.OverlayBrowser
				a.Browser.FlashStart = time.Now()
			},
			want: animationFrameInterval,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t, 60, 15)
			a.idx = &index.Index{State: asyncjob.New()}
			if tt.indexDone {
				a.idx.MarkDone()
			}
			tt.setup(a)
			if got := a.tickInterval(time.Now()); got != tt.want {
				t.Errorf("tickInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}
