// Package logging builds the server's structured JSON logger (PRD NFR-OBS-01).
//
// Request-scoped fields travel in the context: WithRequestID, WithWorkspaceID,
// and WithUserID attach them, and every record logged with that context
// carries them as top-level fields, whatever groups the logger has opened.
// Secrets are kept out by their type (package secret), not by the logger.
package logging

import (
	"context"
	"io"
	"log/slog"
)

// The field names of the request-scoped values.
const (
	KeyRequestID   = "request_id"
	KeyWorkspaceID = "workspace_id"
	KeyUserID      = "user_id"
)

// New returns a logger that writes one JSON object per record to w, dropping
// records below level.
func New(w io.Writer, level slog.Leveler) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(&handler{root: h, h: h})
}

type fieldsKey struct{}

// fields are the request-scoped values of a context. An empty string is
// unset.
type fields struct {
	requestID, workspaceID, userID string
}

func fromContext(ctx context.Context) fields {
	f, _ := ctx.Value(fieldsKey{}).(fields)
	return f
}

func (f fields) attrs() []slog.Attr {
	var as []slog.Attr
	if f.requestID != "" {
		as = append(as, slog.String(KeyRequestID, f.requestID))
	}
	if f.workspaceID != "" {
		as = append(as, slog.String(KeyWorkspaceID, f.workspaceID))
	}
	if f.userID != "" {
		as = append(as, slog.String(KeyUserID, f.userID))
	}
	return as
}

// WithRequestID returns a context whose records carry the request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	f := fromContext(ctx)
	f.requestID = id
	return context.WithValue(ctx, fieldsKey{}, f)
}

// RequestID returns the request ID that WithRequestID attached to ctx, or ""
// if there is none.
func RequestID(ctx context.Context) string {
	return fromContext(ctx).requestID
}

// WithWorkspaceID returns a context whose records carry the workspace ID.
func WithWorkspaceID(ctx context.Context, id string) context.Context {
	f := fromContext(ctx)
	f.workspaceID = id
	return context.WithValue(ctx, fieldsKey{}, f)
}

// WithUserID returns a context whose records carry the user ID.
func WithUserID(ctx context.Context, id string) context.Context {
	f := fromContext(ctx)
	f.userID = id
	return context.WithValue(ctx, fieldsKey{}, f)
}

// handler adds the context's fields to each record.
//
// Attributes added to a record land inside the groups the logger has opened,
// so once a group is open the fields cannot simply be appended. The handler
// therefore keeps root, the handler as it was before the first group, and
// derivations, the WithAttrs and WithGroup calls made since. A record logged
// inside a group with fields in its context is handled by root with the fields
// added, followed by the derivations replayed.
type handler struct {
	root        slog.Handler
	derivations []func(slog.Handler) slog.Handler
	// h is root with the derivations applied, for records without fields.
	h slog.Handler
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.h.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	as := fromContext(ctx).attrs()
	if len(as) == 0 {
		return h.h.Handle(ctx, r)
	}
	if len(h.derivations) == 0 {
		// r may share its attributes with a copy held by the caller, such as
		// a handler that passes one record to several handlers.
		r = r.Clone()
		r.AddAttrs(as...)
		return h.h.Handle(ctx, r)
	}
	d := h.root.WithAttrs(as)
	for _, derive := range h.derivations {
		d = derive(d)
	}
	return d.Handle(ctx, r)
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	if len(h.derivations) == 0 {
		root := h.root.WithAttrs(as)
		return &handler{root: root, h: root}
	}
	return h.derive(func(d slog.Handler) slog.Handler { return d.WithAttrs(as) })
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.derive(func(d slog.Handler) slog.Handler { return d.WithGroup(name) })
}

func (h *handler) derive(f func(slog.Handler) slog.Handler) *handler {
	return &handler{
		root:        h.root,
		derivations: append(h.derivations[:len(h.derivations):len(h.derivations)], f),
		h:           f(h.h),
	}
}
