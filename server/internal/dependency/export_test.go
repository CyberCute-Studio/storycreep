package dependency

import "github.com/CyberCute-Studio/storycreep/server/internal/commands"

// TextNeutral returns textNeutral, for the tests.
func TextNeutral() []commands.Type { return textNeutral }

// EdgeKinds returns edgeKinds, for the tests.
func EdgeKinds() []EdgeKind { return edgeKinds }

// EndSites returns endSites, for the tests.
func EndSites() []string { return endSites }
