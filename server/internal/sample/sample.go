// Package sample holds the sample project, The Lighthouse Keeper (PRD
// FR-PROJ-05, plan item 85, note N92): an authoring export of its draft,
// content-only and without assets, which a user opens in one click, as an
// import with fresh IDs, and which the docs, the demo, and the end-to-end
// tests use. The archive is the sample's source: README.md says what it holds
// and how to change it.
package sample

import (
	"bytes"
	_ "embed"
)

// Name names the sample in the audit entry of its import.
const Name = "the-lighthouse-keeper"

//go:embed the-lighthouse-keeper.zip
var archive []byte

// Reader returns a reader of the sample's archive.
func Reader() *bytes.Reader { return bytes.NewReader(archive) }

// Archive returns a copy of the sample's archive.
func Archive() []byte { return bytes.Clone(archive) }
