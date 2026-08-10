package mumble

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/mumble/murmurrpc"
)

// Decision is one Authenticate request's outcome.
type Decision struct {
	Allow  bool
	UserID int32
	Name   string
}

// Decider resolves one Mumble connection attempt (identified by its
// client certificate hash) to an allow/deny Decision. This package has
// no opinion on HOW that's decided — internal/api/v1's DecideMumbleAuth
// (shared by both this gRPC path and the HTTP
// POST /api/v1/public/mumble/auth path, so the two authenticator
// deployment modes never disagree) implements it, keeping this package
// free of any internal/store dependency, the same separation
// internal/provisioning/entitlement keeps from its own callers.
type Decider interface {
	Decide(ctx context.Context, certificateHash string) (Decision, error)
}

// Authenticator runs Murmur's external-authenticator bidirectional
// stream (01_ARCHITECTURE.md §9.5: "the only mechanism that achieves
// absolute denial rather than group removal"). HANGAR is the gRPC
// CLIENT here — it opens the Authenticate stream against Murmur's own
// gRPC server (Client.RPC), and Murmur pushes one Request per connecting
// client over that stream; HANGAR answers each with a Response.
type Authenticator struct {
	Client  *Client
	Decider Decider
	// FailClosed governs what happens when Decider.Decide itself errors
	// (e.g. HANGAR's own database is unreachable) — roadmap edge case:
	// "External-authenticator mode must fail closed on HANGAR being
	// unreachable only if the administrator opts in — failing closed by
	// default locks everyone out during a HANGAR restart." true responds
	// STATUS_FAIL (deny); false responds STATUS_TEMPORARY, which tells
	// Murmur to fall back to its own local authentication for that one
	// connection rather than denying it outright.
	FailClosed bool
	Log        *slog.Logger
}

// Run opens the stream and answers requests until ctx is cancelled or the
// stream errors — callers run this in a long-lived goroutine, reconnecting
// (a fresh Run call) on a returned error per their own backoff policy;
// Run itself does not retry.
func (a *Authenticator) Run(ctx context.Context) error {
	stream, err := a.Client.RPC.Authenticate(ctx)
	if err != nil {
		return fmt.Errorf("mumble: opening authenticator stream: %w", err)
	}

	for {
		req, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("mumble: authenticator stream recv: %w", err)
		}
		if req.Authenticate == nil {
			// An Initialize message (or any future request kind this
			// minimal proto doesn't model) carries nothing to answer —
			// per MurmurRPC's own contract, only Authenticate requests
			// expect a Response back on the stream.
			continue
		}

		resp := a.decide(ctx, req.Authenticate)
		if err := stream.Send(resp); err != nil {
			return fmt.Errorf("mumble: authenticator stream send: %w", err)
		}
	}
}

func (a *Authenticator) decide(ctx context.Context, req *murmurrpc.Authenticator_Request_Authenticate) *murmurrpc.Authenticator_Response {
	log := a.Log
	if log == nil {
		log = slog.Default()
	}

	decision, err := a.Decider.Decide(ctx, req.CertificateHash)
	if err != nil {
		log.ErrorContext(ctx, "mumble: authenticator decision failed", "error", err, "fail_closed", a.FailClosed)
		if a.FailClosed {
			return &murmurrpc.Authenticator_Response{Status: murmurrpc.Authenticator_Response_STATUS_FAIL}
		}
		return &murmurrpc.Authenticator_Response{Status: murmurrpc.Authenticator_Response_STATUS_TEMPORARY}
	}
	if !decision.Allow {
		return &murmurrpc.Authenticator_Response{Status: murmurrpc.Authenticator_Response_STATUS_FAIL}
	}
	return &murmurrpc.Authenticator_Response{
		Status: murmurrpc.Authenticator_Response_STATUS_OK,
		UserId: decision.UserID,
		Name:   decision.Name,
	}
}
