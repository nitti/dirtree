package views

import (
	"github.com/nitti/dirtree/internal/ignore"
	"github.com/nitti/dirtree/internal/index"
	"github.com/nitti/dirtree/internal/openfiles"
	"github.com/nitti/dirtree/internal/spinner"
	"github.com/nitti/dirtree/internal/tree"
	"github.com/nitti/dirtree/internal/ui/canvas"
)

// Shared bundles the state most views need to read, and occasionally
// write, that isn't specific to any one view: the open-files list, the
// background index, the tree root, and the drawing surface. It's a
// plain struct rather than an interface — everything it holds is
// already an exported type from another package, or (Overlay) a value
// shared by pointer the same way, so there's no second implementation to
// justify an interface boundary, and no test-double need either (this
// rendering layer is manually verified, not unit-tested, per CLAUDE.md).
// Every view embeds *Shared so its fields are reachable directly
// (v.Files, v.Idx, ...) with no extra indirection. Canvas is nil until
// App.Run() initializes the real terminal screen.
type Shared struct {
	Files     *openfiles.List
	Idx       *index.Index
	Root      *tree.Node
	RootPath  string
	Ignorer   *ignore.Multi
	BadgeSkip *spinner.MinDurationSkip
	Overlay   *Overlay
	Canvas    *canvas.Canvas
}
