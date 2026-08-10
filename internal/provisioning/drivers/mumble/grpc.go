// Package mumble is HANGAR's Mumble provisioning driver
// (01_ARCHITECTURE.md §9.5, Phase 13): gRPC MurmurRPC for ACL group
// add/remove, plus an optional external-authenticator mode for absolute
// connection denial. See murmurrpc/murmurrpc.proto's header for an
// important caveat about this package's proto contract.
package mumble

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/mumble/murmurrpc"
)

// Config is the Mumble driver's configuration — internal/config wires
// this from HANGAR_MUMBLE_* (.env.example, Phase 0-scaffolded, parsed
// into internal/config.Config starting this phase).
type Config struct {
	Enabled bool
	// GRPCAddr is Murmur's own gRPC listen address — HANGAR dials OUT to
	// it as a client for both the admin surface (TreeQuery/ACLQuery/
	// ACLSet) and, in external-authenticator mode, the bidirectional
	// Authenticate stream (Murmur pushes requests to HANGAR over the
	// stream HANGAR opened, never the other way around — HANGAR never
	// listens for inbound gRPC itself).
	GRPCAddr string
	ServerID uint32
	// TLSCAPath is a PEM CA file path; empty means an insecure (plaintext)
	// channel — §9.4/.env.example: "empty = insecure channel (LAN only)".
	TLSCAPath string

	ExternalAuthenticator bool
	// FailClosed governs authenticator.go's behaviour when HANGAR itself
	// cannot resolve an auth decision (e.g. its own database is
	// unreachable) — see authenticator.go's doc comment. Defaulting this
	// to false is deliberate (roadmap edge case): failing closed by
	// default would lock out the entire Mumble server on every HANGAR
	// restart, which is worse than the problem it solves.
	FailClosed bool
	// AuthSharedSecret signs POST /api/v1/public/mumble/auth
	// (internal/api/v1/public_mumble_auth.go) — the HTTP-side counterpart
	// for authenticator deployments that call out to HANGAR (e.g. the
	// out-of-process Ice bridge, Phase 13's own scope explicitly excludes
	// building that bridge, but this contract is what it will call).
	AuthSharedSecret string
}

// Validate checks cfg against every hard requirement the driver cannot
// safely start without.
func (c Config) Validate() error {
	var errs []error
	if c.GRPCAddr == "" {
		errs = append(errs, errors.New("mumble: grpc address is required"))
	}
	if c.ServerID == 0 {
		errs = append(errs, errors.New("mumble: server id is required"))
	}
	if c.ExternalAuthenticator && c.AuthSharedSecret == "" {
		errs = append(errs, errors.New("mumble: external-authenticator mode requires HANGAR_MUMBLE_AUTH_SHARED_SECRET"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("mumble: invalid configuration: %w", errors.Join(errs...))
	}
	return nil
}

// Client wraps the murmurrpc gRPC connection.
type Client struct {
	Conn   *grpc.ClientConn
	RPC    murmurrpc.V1Client
	Server *murmurrpc.Server
}

// NewClient dials Murmur's gRPC endpoint. TLSCAPath empty means an
// insecure (plaintext) channel — matching HANGAR_MUMBLE_TLS_CA's own
// documented "empty = insecure channel (LAN only)" default; this is an
// explicit administrator choice, not a fallback HANGAR silently picks
// when TLS setup fails.
func NewClient(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	var creds credentials.TransportCredentials
	if cfg.TLSCAPath != "" {
		pem, err := os.ReadFile(cfg.TLSCAPath)
		if err != nil {
			return nil, fmt.Errorf("mumble: reading TLS CA %s: %w", cfg.TLSCAPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("mumble: TLS CA %s contains no usable certificates", cfg.TLSCAPath)
		}
		creds = credentials.NewTLS(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
	} else {
		creds = insecure.NewCredentials()
	}

	conn, err := grpc.NewClient(cfg.GRPCAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("mumble: dialing %s: %w", cfg.GRPCAddr, err)
	}
	return &Client{
		Conn:   conn,
		RPC:    murmurrpc.NewV1Client(conn),
		Server: &murmurrpc.Server{Id: cfg.ServerID},
	}, nil
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	return c.Conn.Close()
}
