package apicontract

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/CyberCute-Studio/storycreep/server/internal/gitexec"
)

// OverrideTrailer is the trailer with which a commit of a pull request says
// why the pull request may break the API. Until the first release, no
// client depends on v1, so a breaking change needs only a recorded reason
// (PRD FR-API-01 AC3); plan item 119 compares with the last release
// instead, and removes the override.
const OverrideTrailer = "API-Break"

// Override is a reason, which a commit gives, for breaking the API.
type Override struct {
	Commit string
	Reason string
}

// Result is the outcome of Check.
type Result struct {
	// Base is the commit that the document was compared with: where the
	// current branch forked from the main branch.
	Base string
	// Missing is set if the document did not exist at Base, so that there
	// was nothing to compare it with.
	Missing bool
	// Changes are the changes since Base that break a client.
	Changes []Change
	// Overrides are the reasons for breaking the API that the commits since
	// Base give in their API-Break trailers.
	Overrides []Override
}

// Check compares the OpenAPI document at path, as it is in the working tree,
// with the one at the commit where the current branch forked from base, such
// as origin/main, and returns the changes that break a client, with the
// overrides that the branch's commits give. Comparing with the fork point
// rather than base itself keeps what base gained since from counting as the
// branch's removals. path must be in a git repository with the history of
// both, so a shallow clone needs its full history fetched first.
func Check(ctx context.Context, path, base string) (Result, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, fmt.Errorf("the OpenAPI document %s does not exist; the API's clients depend on it, so "+
			"restore it", path)
	}
	if err != nil {
		return Result{}, err
	}
	head, err := Load(b)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}
	dir, file := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	fork, err := gitexec.ForkPoint(ctx, dir, base)
	if err != nil {
		return Result{}, err
	}
	res := Result{Base: fork}
	// A path in a revision is relative to the repository's root, unless it
	// starts with ./, which makes it relative to dir.
	listed, err := gitexec.Run(ctx, dir, "", "ls-tree", "--name-only", res.Base, "--", file)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(listed) == "" {
		res.Missing = true
		return res, nil
	}
	was, err := gitexec.Run(ctx, dir, "", "cat-file", "blob", res.Base+":./"+file)
	if err != nil {
		return Result{}, err
	}
	baseDoc, err := Load([]byte(was))
	if err != nil {
		return Result{}, fmt.Errorf("%s at %.12s: %w", path, res.Base, err)
	}
	res.Changes = Breaking(baseDoc, head)
	res.Overrides, err = overrides(ctx, dir, res.Base)
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// overrides returns the API-Break trailers of the commits since base, which
// git finds whatever their case.
func overrides(ctx context.Context, dir, base string) ([]Override, error) {
	out, err := gitexec.Run(ctx, dir, "", "log", "-z",
		"--format=%H%x1f%(trailers:key="+OverrideTrailer+",valueonly,separator=%x1e,unfold)", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	var found []Override
	for record := range strings.SplitSeq(out, "\x00") {
		commit, values, ok := strings.Cut(record, "\x1f")
		if !ok {
			continue
		}
		for reason := range strings.SplitSeq(values, "\x1e") {
			if reason = strings.TrimSpace(reason); reason != "" {
				found = append(found, Override{Commit: strings.TrimSpace(commit), Reason: reason})
			}
		}
	}
	return found, nil
}
