package fixture

// nouns are the words that variables' keys, props' keys, titles, and string
// values are made of. None is a word of the expression language or a
// built-in's name, and none is a word of names, so that no variable has a
// cast member's key, which it would shadow (PRD §5.8.3).
var nouns = []string{
	"anchor", "lantern", "harbour", "ember", "tide", "compass", "ledger", "rope", "beacon", "cellar",
	"orchard", "bridge", "furnace", "letter", "mirror", "candle", "ferry", "garden", "tower", "market",
	"chapel", "forge", "meadow", "quarry", "river", "saddle", "tavern", "violin", "wagon", "whistle",
	"archive", "banner", "cipher", "dagger", "engine", "feather", "glacier", "helmet", "island", "jewel",
	"kettle", "ladder", "monument", "needle", "oracle", "parcel", "relic", "sextant", "thicket", "umbrella",
	"village", "warden", "lighthouse", "mill", "pier", "reef", "storm", "vault", "well", "crown",
}

// names are the words that the Cast's keys and names are made of.
var names = []string{
	"elara", "mira", "bran", "tamsin", "oren", "isolde", "corin", "wren", "dagny", "fenwick",
	"lysa", "matthias", "nell", "piet", "quill", "rowan", "sabine", "tobias", "ulla", "vesna",
}

// prose are the words that sentences are made of.
var prose = []string{
	"the", "a", "old", "quiet", "storm", "rolls", "over", "harbour", "and", "lantern", "flickers",
	"in", "cold", "wind", "she", "he", "they", "remembers", "promise", "made", "long", "ago",
	"under", "grey", "sky", "ferry", "waits", "for", "no", "one", "while", "bells", "ring",
	"across", "water", "every", "door", "hides", "secret", "worth", "keeping", "someone", "laughs",
	"near", "market", "gate", "map", "shows", "road", "nobody", "walks", "anymore", "tonight",
}
