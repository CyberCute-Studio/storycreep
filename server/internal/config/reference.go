package config

import (
	"fmt"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// Variables returns the names of the environment variables that the
// configuration reads, every key and STORYCREEP_CONFIG_FILE, in the order of
// the reference, so that a deployment's own list of them, such as the
// published compose file's, can be checked against it.
func Variables() []string {
	names := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		names = append(names, k.name)
	}
	return append(names, fileKey)
}

// Reference returns the configuration reference, docs/configuration.md, which
// is generated from the key declarations so that the documented defaults
// cannot drift from the real ones.
func Reference() string {
	var b strings.Builder
	b.WriteString(referenceIntro)

	b.WriteString("\n## Keys\n\n")
	b.WriteString("| Key | Values | Default | Description |\n| --- | --- | --- | --- |\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", k.name, k.valuesCell(), k.defaultCell(), k.descriptionCell())
	}
	fmt.Fprintf(&b, "| `%s` | A file path | Not set | The [configuration file](#the-configuration-file) to read. "+
		"It is read from the environment only, and `--config` overrides it. |\n", fileKey)

	b.WriteString(capabilitiesIntro)
	changedBy := map[string]string{}
	for _, k := range keys {
		if k.capability == "" {
			continue
		}
		cell := "`" + k.name + "`"
		if k.capabilityLimit != "" {
			cell += " (" + k.capabilityLimit + ")"
		}
		changedBy[k.capability] = cell
	}
	self, hosted := modeCapabilities(modeSelfHosted).Describe(), modeCapabilities(modeHosted).Describe()
	fmt.Fprintf(&b, "| Capability | `%s` | `%s` | Changed by |\n| --- | --- | --- | --- |\n", modeSelfHosted, modeHosted)
	for i, c := range self {
		by, ok := changedBy[c.Name]
		if !ok {
			by = "—"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %s |\n", c.Name, c.Value, hosted[i].Value, by)
	}
	return b.String()
}

func (k key) valuesCell() string {
	if len(k.values) == 0 {
		return k.valuesDoc
	}
	quoted := make([]string, len(k.values))
	for i, v := range k.values {
		quoted[i] = "`" + v + "`"
	}
	return strings.Join(quoted, ", ")
}

func (k key) defaultCell() string {
	switch {
	case k.required:
		return "Required"
	case k.def == nil:
		return "Not set"
	}
	self, hosted := k.def(modeSelfHosted), k.def(modeHosted)
	if self == hosted {
		return "`" + self + "`"
	}
	return fmt.Sprintf("`%s` (%s), `%s` (%s)", self, modeSelfHosted, hosted, modeHosted)
}

func (k key) descriptionCell() string {
	d := k.doc
	if k.example != "" {
		d += fmt.Sprintf(" Example: `%s`.", k.example)
	}
	if k.secret {
		d += fmt.Sprintf(" Secret: shown as `%s`.", secret.Redacted)
	}
	return d
}

const referenceIntro = `<!-- Generated from server/internal/config. Do not edit; run
     "go test ./internal/config -run TestReference -update" in server/ instead. -->

# Configuration reference

Storycreep reads its configuration from environment variables and an optional
configuration file. It validates every key when it starts, and if any key is
missing or invalid it exits with a non-zero status and a message naming each
one. To check a configuration without starting anything, run:

` + "```sh" + `
storycreep validate-config
` + "```" + `

It prints each key's effective value and where the value came from, with
secrets redacted, followed by the capabilities they resolve to.

## Where values come from

For each key, the first of these that is set wins:

1. The environment variable of the same name.
2. The configuration file.
3. The key's default.

An empty value counts as unset. A required key without a value is an error.

An environment variable that starts with ` + "`STORYCREEP_`" + ` but is not a key
below is reported as a warning and otherwise ignored.

## The configuration file

The file is optional. Name it with ` + "`--config <path>`" + ` or with the
` + "`STORYCREEP_CONFIG_FILE`" + ` environment variable; the flag wins. It is UTF-8
text of ` + "`KEY=value`" + ` lines with the key names below:

` + "```sh" + `
# /etc/storycreep/storycreep.env
STORYCREEP_PUBLIC_URL=https://storycreep.example.com
STORYCREEP_SIGNUP=invite
` + "```" + `

- Blank lines and lines starting with ` + "`#`" + ` are ignored.
- Whitespace around the key and the value is ignored.
- A value wrapped in a matching pair of ` + "`\"`" + ` or ` + "`'`" + ` is unwrapped.
- Nothing is unescaped or expanded, and a ` + "`#`" + ` after a value is part of
  the value.
- Every key must be one of the keys below and appear at most once.
  ` + "`STORYCREEP_CONFIG_FILE`" + ` cannot be set in the file.

This format is simpler than the environment files of Docker Compose
(` + "`env_file`" + `) and systemd (` + "`EnvironmentFile`" + `). A file written for either is
read the same way here only if it has no ` + "`export`" + ` in front of a key, no comment
after a value, no escape sequence, and no variable reference such as
` + "`${HOME}`" + `. systemd also ignores lines that start with ` + "`;`" + ` and lines without
an ` + "`=`" + `, which are errors here.
`

const capabilitiesIntro = `
## Capabilities

` + "`STORYCREEP_MODE`" + ` resolves into the capabilities below (PRD §10.3). Code
that behaves differently in the two modes reads a capability, never the mode.
A capability with a key in the last column starts from the mode's value and
can be changed with that key.

`
