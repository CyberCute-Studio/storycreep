package httpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/asset"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/entitlements"
	"github.com/CyberCute-Studio/storycreep/server/internal/i18n"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// assetsPath is the path of a project's assets, whose {project_id} is the
// project's.
const assetsPath = "/api/v1/projects/{project_id}/assets"

// uploadReadTimeout is how long an upload's file may take to arrive: long
// enough for the largest file that an instance may take, 50 MB, on a
// connection of 1.5 Mbit/s.
const uploadReadTimeout = 5 * time.Minute

// uploadMemory is how many bytes of files the uploads in progress may hold
// at once. An upload reads its file whole into memory, as the route's body,
// so an instance runs as many at once as this holds files of the largest
// size that it takes, and at least one, and each other upload waits for one
// of them to end before its file is read (holdSlot), so that a burst of
// large uploads, or many that send their files slowly, cannot make the
// server run out of memory. A file takes up to about twice its size while it
// arrives, and an SVG image a few times more while it is sanitized, as one
// whose path is a single attribute of megabytes, which the decoder holds
// twice, as it reads it and as the attribute's value, which the sanitizer
// checks without copying it, and the file that it writes, of at most the
// same size (asset.ErrTooLarge), but no more, whatever its elements and their
// attributes, which the sanitizer bounds before the decoder reads them.
const uploadMemory = 256 << 20

// uploadSlots returns how many uploads of files of at most maxBytes may hold
// their files at once (uploadMemory).
func uploadSlots(maxBytes int64) int {
	return int(max(1, uploadMemory/maxBytes))
}

// slots are the slots that the uploads hold their files in (holdSlot), all
// of them, of which each user may hold share at once, a quarter, and at
// least one, so that a user who holds theirs, as by sending files slowly,
// which keeps each slot for as long as the route waits for the file, leaves
// the rest to the other users.
type slots struct {
	all   chan struct{}
	share int
	mu    sync.Mutex
	// users are the slots of the users who hold one or wait for one.
	users map[ids.UUID]*userSlots
}

// userSlots are the slots of a user's requests.
type userSlots struct {
	held chan struct{}
	// waiting is how many of the user's requests hold a slot, or wait for
	// one, which keep the user's slots.
	waiting int
}

// newSlots returns n slots, of which each user holds a quarter at most.
func newSlots(n int) *slots {
	return &slots{all: make(chan struct{}, n), share: max(1, n/4), users: map[ids.UUID]*userSlots{}}
}

// take waits for a slot of the user's, and then for one of all, and returns
// the function that frees them, or the error of ctx if it ends first.
func (s *slots) take(ctx context.Context, user ids.UUID) (func(), error) {
	s.mu.Lock()
	u := s.users[user]
	if u == nil {
		u = &userSlots{held: make(chan struct{}, s.share)}
		s.users[user] = u
	}
	u.waiting++
	s.mu.Unlock()
	leave := func() {
		s.mu.Lock()
		if u.waiting--; u.waiting == 0 {
			delete(s.users, user)
		}
		s.mu.Unlock()
	}
	select {
	case u.held <- struct{}{}:
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
	select {
	case s.all <- struct{}{}:
	case <-ctx.Done():
		<-u.held
		leave()
		return nil, ctx.Err()
	}
	free := func() {
		<-s.all
		<-u.held
		leave()
	}
	// select picks at random between a free slot and an ended context, and a
	// request that nobody waits for would hold the slot from those who do.
	if err := ctx.Err(); err != nil {
		free()
		return nil, err
	}
	return free, nil
}

// holdSlot returns middleware for an operation that waits for a slot of s,
// of the user who makes the request, before the operation reads the
// request's body, and frees it once the operation has answered, so that no
// more requests than s has room for read their bodies, and hold them, at
// once, and no user's more than their share. A request whose context ends
// while it waits, as when its client goes away, is answered with the
// context's error.
func holdSlot(s *slots) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		// The route needs an authenticated user, whom the authenticator
		// found before this runs.
		cur, _ := currentFrom(ctx.Context())
		free, err := s.take(ctx.Context(), cur.userID)
		if err != nil {
			r, w := humachi.Unwrap(ctx)
			api.WriteError(w, r, err)
			return
		}
		defer free()
		next(ctx)
	}
}

// imageTypes are the types of the files that an upload takes and an asset is
// served as, which the OpenAPI document names.
var imageTypes = []string{asset.MimePNG, asset.MimeJPEG, asset.MimeGIF, asset.MimeWebP, asset.MimeSVG}

// sandboxCSP is the Content-Security-Policy of an asset that the server
// serves (PRD FR-ASSET-02 AC3, NFR-SEC-10): the asset is a document of a
// unique origin, which runs no script, so that an SVG opened by itself can
// reach nothing of the application's.
const sandboxCSP = "sandbox"

type assetInput struct {
	ProjectID string `path:"project_id" doc:"The project's ID"`
	AssetID   string `path:"asset_id" doc:"The asset's ID"`
}

type assetOutput struct {
	Body asset.Asset
}

// assetFileInput is the input of the routes that serve an asset's file.
type assetFileInput struct {
	ProjectID   string `path:"project_id" doc:"The project's ID"`
	AssetID     string `path:"asset_id" doc:"The asset's ID"`
	IfNoneMatch string `header:"If-None-Match" doc:"The ETag of the file that the client has, which is answered with 304 Not Modified while it is current"`
}

// uploadAssetInput is the input of an upload, whose body the route reads
// whole, up to the instance's limit, once the guard has let it through.
type uploadAssetInput struct {
	SessionCSRFInput
	ProjectID string `path:"project_id" doc:"The project's ID"`
	RawBody   []byte `contentType:"application/octet-stream"`
}

type uploadedAssetOutput struct {
	Status   int
	Location string `header:"Location" doc:"The asset's URL"`
	Body     asset.Asset
}

// assetNotFoundDoc says, in the description of an operation on an asset,
// when the operation answers that it does not exist.
const assetNotFoundDoc = " An asset that the project does not have, as one of another project, is answered with 404 " +
	"not_found too, whatever its ID."

// fileCache is the Cache-Control of an asset's file that the server serves:
// a browser keeps its copy, for its user alone, for as long as a URL that the
// S3 driver signs works, as the bucket lets it keep a file that it reads
// through one, before it asks the server again, which checks the user's
// access again. A board that shows many images then asks for each once in
// that time, rather than each time that it shows it, which the read rate
// limit would count.
var fileCache = blob.CacheControl(asset.SignedURLLifetime)

// redirectCache is the Cache-Control of the redirect to a URL that the S3
// driver signs: a browser follows it again from its cache for a minute less
// than the URL works, so that the URL that it reaches that way still works,
// and reads the file that the URL answered with from its cache too.
var redirectCache = blob.CacheControl(asset.SignedURLLifetime - time.Minute)

// pendingCache is the Cache-Control of what the thumbnail route answers with
// while the asset's thumbnail job has not made its thumbnail yet: the file
// itself, or the redirect to it, which a browser keeps for half a minute
// rather than fileCache's 5 minutes, so that it asks for the thumbnail again
// soon after the job, which takes moments, has made it, rather than keep
// showing the large file, which it decodes whole, as a board of the images
// that were just uploaded would.
var pendingCache = blob.CacheControl(30 * time.Second)

// fileDoc says how an operation that serves an asset's file answers.
var fileDoc = " The answer depends on where the instance stores its files (STORYCREEP_ASSET_STORAGE). The local " +
	"driver's files are served by this route, with the file's type, Content-Security-Policy: sandbox, " +
	"X-Content-Type-Options: nosniff, and Content-Disposition: inline for an image, which every asset is, and " +
	"attachment for any other type (PRD FR-ASSET-02 AC3), an ETag, which If-None-Match revalidates with 304, and " +
	"Cache-Control: " + fileCache + ", so that a browser uses its copy for 5 minutes, as long as a URL of an S3 " +
	"bucket works, before it asks again, and the server checks the user's access again. An S3 bucket's are " +
	"answered with 302 Found, whose Location is a URL of the bucket that the server signs for 5 minutes, with the " +
	"file's type, its Content-Disposition, and Cache-Control: " + fileCache + " fixed in its signature, and the " +
	"redirect has Cache-Control: " + redirectCache + ", so that a browser that follows it again from its cache " +
	"reaches a URL that still works. The bucket's origin is not the application's, so a file read from it " +
	"reaches nothing of the application's (NFR-SEC-10); a client that reads the file with fetch, rather than " +
	"showing it as an image, follows the redirect there, so the bucket's CORS rules must then allow the " +
	"application's origin. Either way, the server checks the user's access to the project before it reads " +
	"anything of the file, so that no URL reads another workspace's file, whatever it guesses (FR-ASSET-01 AC2)."

// registerAssets registers the routes of the projects' assets (PRD
// FR-ASSET-01, FR-ASSET-02): uploading one, whose file is at most the
// service's MaxBytes, as many at once as uploadMemory holds, reading one,
// and serving its file and its thumbnail.
func registerAssets(m *api.Mux, assets *asset.Service) {
	maxBytes := assets.MaxBytes()
	// checkUpload refuses an upload that the project would refuse whatever
	// its file holds before the route reads the file, so that a request that
	// may not upload does not send the server megabytes first, and is not
	// told what the file would have been answered with.
	checkUpload := api.GuardRequest(func(ctx huma.Context) error {
		id, actor, err := projectActor(ctx.Context(), ctx.Param("project_id"))
		if err == nil {
			err = assets.CheckUpload(ctx.Context(), actor, id)
		}
		if err != nil {
			return assetError(err)
		}
		return nil
	})
	registry := m.OpenAPI().Components.Schemas
	assetSchema := registry.Schema(reflect.TypeFor[asset.Asset](), true, "")
	uploadBody := &huma.RequestBody{Required: true, Description: "The file, as it is, whatever its name: the server " +
		"tells its type from its content, and ignores the request's Content-Type, which may be any.",
		Content: map[string]*huma.MediaType{}}
	for _, t := range imageTypes {
		uploadBody.Content[t] = &huma.MediaType{Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}
	}

	api.Register(m, api.UploadAssets, huma.Operation{
		OperationID: "uploadAsset",
		Method:      http.MethodPost,
		Path:        assetsPath,
		Summary:     "Upload an image to a project",
		Description: "Uploads an image, which the project's content can then refer to, as a node's cover or a " +
			"component's portrait (PRD FR-ASSET-02). The body is the file: a PNG, JPEG, GIF, WebP, or SVG image of " +
			"at most STORYCREEP_ASSET_MAX_BYTES, 10 MB unless the instance says otherwise, or the answer is 413 " +
			"request_too_large. The server tells the type from the file's content alone, and refuses any other " +
			"file, as text whose name ends in .png, with 422 image_invalid. An SVG image is stored without its " +
			"scripts, event handlers, and references to anything outside it, which the server removes; it is " +
			"never stored as it was uploaded, and one that would be stored larger than STORYCREEP_ASSET_MAX_BYTES, " +
			"as the server writes some of its characters as references, is answered with 413 request_too_large " +
			"too. A thumbnail of a large image is made soon after. The answer is 201 " +
			"with the new asset, or 200 with the asset that the project has of the same file already, which makes " +
			"an upload that a client sends again harmless. A file that the workspace does not store yet must fit " +
			"in its storage: an upload that would take the workspace past its plan's storage_bytes is answered " +
			"with 413 storage_limit_exceeded, with the workspace's usage and limit. Only an owner, an admin, or an " +
			"editor of the project may upload, and anyone else with a role on it is answered with 403 " +
			"insufficient_role, before the file is read. The server reads only so many files at once, and a " +
			"quarter of them of one user's, so an upload may wait for others to end before its file is read." +
			projectNotFoundDoc + tokenNotFoundDoc +
			archivedDoc + tokenChangeDoc(scope.ProjectWrite),
		Tags:            []string{"Assets"},
		DefaultStatus:   http.StatusCreated,
		RequestBody:     uploadBody,
		MaxBodyBytes:    maxBytes,
		BodyReadTimeout: uploadReadTimeout,
		// A request that the guard refuses takes no slot.
		Middlewares: huma.Middlewares{checkUpload, holdSlot(newSlots(uploadSlots(maxBytes)))},
		Responses: map[string]*huma.Response{
			"200": {Description: "The project's asset of the same file, which it has already",
				Content: map[string]*huma.MediaType{"application/json": {Schema: assetSchema}}},
		},
	}, func(ctx context.Context, in *uploadAssetInput) (*uploadedAssetOutput, error) {
		id, actor, err := projectActor(ctx, in.ProjectID)
		if err != nil {
			return nil, err
		}
		a, created, err := assets.Upload(ctx, actor, id, in.RawBody)
		if errors.Is(err, asset.ErrTooLarge) {
			return nil, &api.Error{Code: api.CodeRequestTooLarge, Details: map[string]any{"max_bytes": maxBytes}}
		}
		if err != nil {
			return nil, assetError(err)
		}
		out := &uploadedAssetOutput{Status: http.StatusOK, Body: a}
		if created {
			out.Status = http.StatusCreated
			out.Location = "/api/v1/projects/" + a.ProjectID + "/assets/" + a.ID
		}
		return out, nil
	}, api.TokenScope(scope.ProjectWrite))

	api.Register(m, api.ReadProject, huma.Operation{
		OperationID: "getAsset",
		Method:      http.MethodGet,
		Path:        assetsPath + "/{asset_id}",
		Summary:     "Get an asset",
		Description: "Returns an asset of a project: its type, size, and hash." + projectNotFoundDoc +
			tokenNotFoundDoc + assetNotFoundDoc + tokenAuthDoc(scope.ProjectRead),
		Tags: []string{"Assets"},
	}, func(ctx context.Context, in *assetInput) (*assetOutput, error) {
		pid, aid, actor, err := assetActor(ctx, in.ProjectID, in.AssetID)
		if err != nil {
			return nil, err
		}
		a, err := assets.Get(ctx, actor, pid, aid)
		if err != nil {
			return nil, assetError(err)
		}
		return &assetOutput{Body: a}, nil
	}, api.TokenScope(scope.ProjectRead))

	for _, file := range []struct {
		thumbnail                      bool
		id, path, summary, description string
	}{
		{false, "getAssetContent", "/content", "Get an asset's file",
			"Serves an asset's file, as it is stored."},
		{true, "getAssetThumbnail", "/thumbnail", "Get an asset's thumbnail",
			"Serves an asset's thumbnail: a PNG image that fits in 256 × 256 pixels, of a PNG, JPEG, GIF, or WebP " +
				"image larger than that, or else the file itself, which is also served until the thumbnail is made, " +
				"soon after the upload, and for an image whose thumbnail cannot be made. Until the thumbnail is " +
				"made, the file, the redirect to it, and a 304 have Cache-Control: " + pendingCache + " instead, so " +
				"that a browser asks for the thumbnail again soon after it is made."},
	} {
		api.Register(m, api.ReadProject, huma.Operation{
			OperationID: file.id,
			Method:      http.MethodGet,
			Path:        assetsPath + "/{asset_id}" + file.path,
			Summary:     file.summary,
			Description: file.description + fileDoc + projectNotFoundDoc + tokenNotFoundDoc + assetNotFoundDoc +
				tokenAuthDoc(scope.ProjectRead),
			Tags:      []string{"Assets"},
			Responses: fileResponses(file.thumbnail),
		}, func(ctx context.Context, in *assetFileInput) (*huma.StreamResponse, error) {
			return serveAsset(ctx, assets, in, file.thumbnail)
		}, api.TokenScope(scope.ProjectRead))
	}
}

// fileResponses documents the answers of a route that serves an asset's
// file, or, if thumbnail is set, its thumbnail.
func fileResponses(thumbnail bool) map[string]*huma.Response {
	header := func(doc string) *huma.Header {
		return &huma.Header{Schema: &huma.Schema{Type: huma.TypeString, Description: doc}}
	}
	// pending is what the thumbnail route says of the Cache-Control of its
	// answers while the thumbnail is not made yet.
	pending := ""
	if thumbnail {
		pending = ", or " + pendingCache + " while the thumbnail is not made yet"
	}
	ok := &huma.Response{
		Description: "The file, which the local driver keeps",
		Headers: map[string]*huma.Header{
			"Content-Security-Policy": header("sandbox"),
			"X-Content-Type-Options":  header("nosniff"),
			"Content-Disposition":     header("inline for an image, and attachment for any other type, with the file's name"),
			"ETag":                    header("The file's entity tag, which If-None-Match revalidates"),
			"Cache-Control":           header(fileCache + pending),
		},
		Content: map[string]*huma.MediaType{},
	}
	for _, t := range imageTypes {
		ok.Content[t] = &huma.MediaType{Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}
	}
	return map[string]*huma.Response{
		"200": ok,
		"302": {
			Description: "Found: the file is in an S3 bucket, and Location is a URL of it that the server signed " +
				"for 5 minutes",
			Headers: map[string]*huma.Header{
				"Location":      header("The signed URL of the file"),
				"Cache-Control": header(redirectCache + ": a minute less than the URL works" + pending),
			},
		},
		"304": api.NotModifiedResponseWith(fileCache + ": the client keeps its copy for 5 minutes again" + pending),
	}
}

// serveAsset answers a request of an asset's file, or of its thumbnail, once
// the service has checked the actor's access, and before it reads anything
// of the file: with a redirect to the URL that the store signs, or else, for
// the local driver, with 304 if the client has the file, whose tag the key
// gives, without opening it, or with the file, which a HEAD request, which
// the route answers as GET (middleware.GetHead), is answered without. A
// thumbnail that is not made yet, whose file is served instead, is answered
// with pendingCache rather than fileCache or redirectCache.
func serveAsset(ctx context.Context, assets *asset.Service, in *assetFileInput, thumbnail bool) (
	*huma.StreamResponse, error) {
	pid, aid, actor, err := assetActor(ctx, in.ProjectID, in.AssetID)
	if err != nil {
		return nil, err
	}
	loc, err := assets.Locate(ctx, actor, pid, aid, thumbnail)
	if err != nil {
		return nil, assetError(err)
	}
	// The key is the content's hash, so its tag changes exactly with the
	// file, as when the thumbnail is made.
	etag := api.ETag([]byte(loc.Key))
	cache, redirect := fileCache, redirectCache
	if loc.Pending {
		cache, redirect = pendingCache, pendingCache
	}
	url, signed, err := assets.SignedURL(ctx, loc)
	switch {
	case err != nil:
		return nil, assetError(err)
	case signed:
		return &huma.StreamResponse{Body: func(hc huma.Context) {
			hc.SetHeader("Content-Security-Policy", sandboxCSP)
			hc.SetHeader("Location", url)
			hc.SetHeader("Cache-Control", redirect)
			hc.SetStatus(http.StatusFound)
		}}, nil
	case api.IfNoneMatch(in.IfNoneMatch, etag):
		return nil, api.NotModifiedWith(etag, cache)
	}
	body, size, err := assets.Open(ctx, loc)
	if err != nil {
		return nil, assetError(err)
	}
	return &huma.StreamResponse{Body: func(hc huma.Context) {
		defer body.Close()
		hc.SetHeader("Content-Type", loc.ContentType)
		hc.SetHeader("Content-Length", strconv.FormatInt(size, 10))
		hc.SetHeader("Content-Security-Policy", sandboxCSP)
		hc.SetHeader("X-Content-Type-Options", "nosniff")
		hc.SetHeader("Content-Disposition", asset.Disposition(loc))
		hc.SetHeader("ETag", etag)
		hc.SetHeader("Cache-Control", cache)
		hc.SetStatus(http.StatusOK)
		if hc.Method() == http.MethodHead {
			return
		}
		// The status and the headers are sent, so a failure from here on can
		// only end the answer early, which the client sees as a body shorter
		// than its Content-Length.
		_, _ = io.Copy(hc.BodyWriter(), body)
	}}, nil
}

// assetActor returns the project and the asset that the path names, and the
// user who makes the request, with their API token, as the actor of the
// asset's read.
func assetActor(ctx context.Context, projectID, assetID string) (ids.UUID, ids.UUID, commands.Actor, error) {
	pid, actor, err := projectActor(ctx, projectID)
	if err != nil {
		return ids.UUID{}, ids.UUID{}, commands.Actor{}, err
	}
	aid, err := ids.Decode(ids.Asset, assetID)
	if err != nil {
		return ids.UUID{}, ids.UUID{}, commands.Actor{}, err
	}
	return pid, aid, actor, nil
}

// assetError returns the error that the API answers err with, an error of
// package asset, of package project, which reads the asset's project, or of
// the entitlements, or err itself if it is none of them: the storage limit
// with 413 storage_limit_exceeded (FR-ASSET-02 AC4), and a file that is not
// an image that an upload takes with 422 image_invalid.
func assetError(err error) error {
	var img *asset.ImageError
	var limit *entitlements.LimitError
	switch {
	case errors.Is(err, asset.ErrNotFound):
		return api.NewError(api.CodeNotFound)
	case errors.As(err, &img):
		return &api.Error{
			Code:    api.CodeImageInvalid,
			Message: imageMessage(img),
			Details: map[string]any{"reason": img.Reason,
				"errors": []api.Problem{{Location: "body", Message: img.Detail}}},
		}
	case errors.As(err, &limit) && limit.Key == entitlements.StorageBytes:
		return storageLimitError(limit)
	}
	return projectError(err)
}

// imageMessage returns the message of an answer of image_invalid, which says
// why the file is refused.
func imageMessage(e *asset.ImageError) i18n.Message {
	switch e.Reason {
	case asset.ReasonType:
		return i18n.Message{Key: i18n.K("error.image_invalid.type")}
	case asset.ReasonMalformed:
		return i18n.Message{Key: i18n.K("error.image_invalid.malformed")}
	case asset.ReasonUnsupported:
		return i18n.Message{Key: i18n.K("error.image_invalid.unsupported")}
	case asset.ReasonDimensions:
		return i18n.Message{Key: i18n.K("error.image_invalid.dimensions"),
			Args: i18n.Args{"pixels": asset.MaxPixels, "side": asset.MaxSide}}
	}
	return i18n.Message{}
}
