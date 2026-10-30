package expression

// Neutral are the types of command that the derivation passes over
// (neutral), which TestNeutral checks are registered.
var Neutral = neutral

// Rewriting are the types of command whose changes of a node the derivation
// passes over (rewriting), which TestNeutral checks are registered.
var Rewriting = rewriting

// Sites are the types of the sites of the expressions of each field of each
// kind of owner (sites), and OwnerFields those fields (fields), which
// TestSites holds to each other.
var Sites, OwnerFields = sites, fields
