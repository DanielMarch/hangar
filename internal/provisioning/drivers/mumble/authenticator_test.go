package mumble_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/mumble"
	"github.com/hangar-project/hangar/internal/provisioning/drivers/mumble/murmurrpc"
)

// stubAuthenticatorServer drives the Authenticate stream from the SERVER
// side (Murmur's real role): it sends one Authenticator_Request down to
// the connected client (HANGAR) and captures whatever
// Authenticator_Response comes back.
type stubAuthenticatorServer struct {
	murmurrpc.UnimplementedV1Server
	Request  *murmurrpc.Authenticator_Request
	Response chan *murmurrpc.Authenticator_Response
}

func (s *stubAuthenticatorServer) Authenticate(stream grpc.BidiStreamingServer[murmurrpc.Authenticator_Response, murmurrpc.Authenticator_Request]) error {
	if err := stream.Send(s.Request); err != nil {
		return err
	}
	resp, err := stream.Recv()
	if err != nil {
		return err
	}
	s.Response <- resp
	<-stream.Context().Done()
	return nil
}

func newStubAuthClient(t *testing.T, stub murmurrpc.V1Server) *mumble.Client {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	murmurrpc.RegisterV1Server(server, stub)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return &mumble.Client{Conn: conn, RPC: murmurrpc.NewV1Client(conn), Server: &murmurrpc.Server{Id: 1}}
}

// denyingDecider always denies — the specific test double
// TestExternalAuthenticatorDeniesConnection needs.
type denyingDecider struct{}

func (denyingDecider) Decide(ctx context.Context, certificateHash string) (mumble.Decision, error) {
	return mumble.Decision{Allow: false}, nil
}

// allowingDecider always allows, echoing back a fixed identity.
type allowingDecider struct{}

func (allowingDecider) Decide(ctx context.Context, certificateHash string) (mumble.Decision, error) {
	return mumble.Decision{Allow: true, UserID: 7, Name: "Approved Pilot"}, nil
}

// erroringDecider always errors — exercises the fail-open/fail-closed
// branch.
type erroringDecider struct{}

func (erroringDecider) Decide(ctx context.Context, certificateHash string) (mumble.Decision, error) {
	return mumble.Decision{}, errors.New("simulated lookup failure")
}

// TestExternalAuthenticatorDeniesConnection (roadmap exit criterion):
// denial mode refuses the connection outright — STATUS_FAIL, not merely
// an absent group.
func TestExternalAuthenticatorDeniesConnection(t *testing.T) {
	stub := &stubAuthenticatorServer{
		Request:  &murmurrpc.Authenticator_Request{Authenticate: &murmurrpc.Authenticator_Request_Authenticate{CertificateHash: "deadbeef"}},
		Response: make(chan *murmurrpc.Authenticator_Response, 1),
	}
	client := newStubAuthClient(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	auth := &mumble.Authenticator{Client: client, Decider: denyingDecider{}}
	go func() { _ = auth.Run(ctx) }()

	select {
	case resp := <-stub.Response:
		require.Equal(t, murmurrpc.Authenticator_Response_STATUS_FAIL, resp.Status, "a denied identity must get STATUS_FAIL — absolute denial, not silence")
	case <-ctx.Done():
		t.Fatal("never received a response from the authenticator")
	}
}

// TestExternalAuthenticatorAllowsConnection is the control case.
func TestExternalAuthenticatorAllowsConnection(t *testing.T) {
	stub := &stubAuthenticatorServer{
		Request:  &murmurrpc.Authenticator_Request{Authenticate: &murmurrpc.Authenticator_Request_Authenticate{CertificateHash: "goodhash"}},
		Response: make(chan *murmurrpc.Authenticator_Response, 1),
	}
	client := newStubAuthClient(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	auth := &mumble.Authenticator{Client: client, Decider: allowingDecider{}}
	go func() { _ = auth.Run(ctx) }()

	select {
	case resp := <-stub.Response:
		require.Equal(t, murmurrpc.Authenticator_Response_STATUS_OK, resp.Status)
		require.Equal(t, int32(7), resp.UserId)
		require.Equal(t, "Approved Pilot", resp.Name)
	case <-ctx.Done():
		t.Fatal("never received a response from the authenticator")
	}
}

// TestExternalAuthenticatorFailClosedOnDeciderError: with FailClosed=true,
// a Decider error becomes STATUS_FAIL — the roadmap's explicit
// administrator opt-in path.
func TestExternalAuthenticatorFailClosedOnDeciderError(t *testing.T) {
	stub := &stubAuthenticatorServer{
		Request:  &murmurrpc.Authenticator_Request{Authenticate: &murmurrpc.Authenticator_Request_Authenticate{CertificateHash: "x"}},
		Response: make(chan *murmurrpc.Authenticator_Response, 1),
	}
	client := newStubAuthClient(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	auth := &mumble.Authenticator{Client: client, Decider: erroringDecider{}, FailClosed: true}
	go func() { _ = auth.Run(ctx) }()

	select {
	case resp := <-stub.Response:
		require.Equal(t, murmurrpc.Authenticator_Response_STATUS_FAIL, resp.Status)
	case <-ctx.Done():
		t.Fatal("never received a response")
	}
}

// TestExternalAuthenticatorFailOpenByDefaultOnDeciderError: FailClosed's
// zero value (false) — the roadmap's explicit default — turns a Decider
// error into STATUS_TEMPORARY (Murmur falls back to local auth for that
// one connection) rather than locking the whole server out.
func TestExternalAuthenticatorFailOpenByDefaultOnDeciderError(t *testing.T) {
	stub := &stubAuthenticatorServer{
		Request:  &murmurrpc.Authenticator_Request{Authenticate: &murmurrpc.Authenticator_Request_Authenticate{CertificateHash: "x"}},
		Response: make(chan *murmurrpc.Authenticator_Response, 1),
	}
	client := newStubAuthClient(t, stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	auth := &mumble.Authenticator{Client: client, Decider: erroringDecider{}} // FailClosed defaults to false
	go func() { _ = auth.Run(ctx) }()

	select {
	case resp := <-stub.Response:
		require.Equal(t, murmurrpc.Authenticator_Response_STATUS_TEMPORARY, resp.Status)
	case <-ctx.Done():
		t.Fatal("never received a response")
	}
}
