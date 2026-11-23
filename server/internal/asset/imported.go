package asset

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// ErrDuplicateContent is AddImported's error for a file that the project has
// an asset of already, as a project has one asset of each content.
var ErrDuplicateContent = errors.New("asset: the project has an asset of this content already")

// ImportedFile is an imported asset's file, once it is checked and stored
// (StoreImported).
type ImportedFile struct {
	image     Image
	sum       []byte
	key       string
	thumbnail bool
}

// Sum returns the SHA-256 of the file as it is stored, which sanitizing an SVG
// may have changed.
func (f ImportedFile) Sum() []byte { return f.sum }

// Size returns the size of the file as it is stored.
func (f ImportedFile) Size() int64 { return int64(len(f.image.Data)) }

// StoreImported checks data, an imported asset's file, as an upload's is: it
// tells its type from its content, refuses what is not an image that the
// server takes, sanitizes an SVG (FR-ASSET-01), and stores it as a blob of the
// workspace, outside any transaction. AddImported then adds its asset, and
// the orphan reconciliation deletes the blob if none ever does.
func (s *Service) StoreImported(ctx context.Context, workspaceID ids.UUID, data []byte) (ImportedFile, error) {
	img, err := Sniff(data, int(s.maxBytes))
	if err != nil {
		return ImportedFile{}, err
	}
	sum := sha256.Sum256(img.Data)
	key := blobKey(workspaceID, sum[:], img.Ext)
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return ImportedFile{}, err
	}
	if err := ensureBlob(ctx, store, key, img, true); err != nil {
		return ImportedFile{}, err
	}
	return ImportedFile{image: img, sum: sum[:], key: key, thumbnail: needsThumbnail(img)}, nil
}

// AddImported adds the asset assetID of the file f to the new project
// projectID, in tx, the import's transaction, as an upload adds one: under the
// blob's lock, which keeps the orphan reconciliation from deleting it, storing
// it again if it has, and checking the workspace's storage for a blob that is
// new to it. It returns ErrDuplicateContent for a file that the project has
// already, and an *entitlements.LimitError for a workspace without the room.
func (s *Service) AddImported(ctx context.Context, tx pgx.Tx, workspaceID, projectID, assetID, uploadedBy ids.UUID,
	f ImportedFile) error {
	q := queries.New(tx)
	if err := lockBlob(ctx, q, workspaceID, f.sum); err != nil {
		return err
	}
	st, err := s.checkStorage(ctx, tx, workspaceID, projectID, f.sum, f.key, f.image)
	if err != nil {
		return err
	}
	if st.found {
		return ErrDuplicateContent
	}
	store, err := s.storage.FinalStore(ctx)
	if err != nil {
		return err
	}
	storeCtx, cancel := context.WithTimeout(ctx, lockedStoreTimeout)
	err = ensureBlob(storeCtx, store, f.key, f.image, !st.referenced)
	cancel()
	if err != nil {
		return err
	}
	if _, err := s.addRow(ctx, tx, workspaceID, projectID, assetID, uploadedBy, f.image, f.sum, f.key, f.thumbnail,
		st.referenced); err != nil {
		return fmt.Errorf("asset: add the imported asset: %w", err)
	}
	return nil
}
