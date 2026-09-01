// zones.go wires this package's widgets, and internal/form's own
// Section/Model rendering (form.go's compose/ViewAt/Update, field_*.go's
// MarkedView/SelectAt call sites), to a single, shared bubblezone/v2
// Manager (github.com/lrstanley/bubblezone/v2) -- task 21's mouse
// support: every focusable section, chip, picker row, and the Create
// button registers a zone via Zones.Mark, form.Model.ViewAt strips them
// back out via Zones.Scan before any golden-frame test (or a real
// render) ever sees the composed string, and form.Model.Update looks
// click/wheel coordinates up against them via Zones.Get(...).InBounds.
//
// Zones is a package-level, NON-global Manager (zone.New(), never
// zone.NewGlobal()/the package-level zone.Mark/zone.Scan/zone.Get
// functions) -- a deliberate choice, not an oversight. bubblezone's own
// global functions all call DefaultManager.checkInitialized() first,
// which PANICS if zone.NewGlobal() was never called first (verified
// against the vendored v2.0.0 source, manager_global.go's own
// checkInitialized). This package's own _test.go files (chiprow_test.go,
// picker_test.go) exercise ChipRow/Picker directly and have no reason to
// import internal/form or run any init-time setup of their own, and
// nothing in internal/form's own construction path (New, NewDirField,
// ...) performs a separate "initialize bubblezone" step either -- a
// package-level Manager built via zone.New() is ready to use the moment
// this package is imported (its own zoneWorker goroutine already
// started, enabled by default), so there is no "forgot to call
// NewGlobal() first" panic surface at all, matching this project's own
// well-established "degrade/no-op rather than panic" convention already
// documented throughout this package (e.g. Picker.View/ChipRow.View's
// own degenerate-dimension doc comments).
package widgets

import zone "github.com/lrstanley/bubblezone/v2"

// Zones is the single bubblezone/v2 Manager every zone-aware render
// (ChipRow.MarkedView, Picker.MarkedView, form.go's compose and
// createSection.View) and every mouse-hit lookup (form.go's
// handleMouseClick/handleMouseWheel, field_*.go's own click/wheel
// handling) shares -- see the package doc for why it is a package-level,
// non-global instance rather than bubblezone's own DefaultManager.
var Zones = zone.New()
