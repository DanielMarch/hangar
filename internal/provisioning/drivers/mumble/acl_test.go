package mumble_test

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/mumble"
	"github.com/hangar-project/hangar/internal/provisioning/drivers/mumble/murmurrpc"
)

// stubMurmur is a hand-rolled MurmurRPC V1 server — driver.go's real
// caller shape, run over a real (in-memory) gRPC transport via bufconn,
// not a mocked Go interface standing in for gRPC itself.
type stubMurmur struct {
	murmurrpc.UnimplementedV1Server

	mu      sync.Mutex
	rootID  uint32
	acl     *murmurrpc.ACL
	aclSets int
}

func newStubMurmur(rootID uint32) *stubMurmur {
	return &stubMurmur{
		rootID: rootID,
		acl:    &murmurrpc.ACL{Channel: &murmurrpc.Channel{Id: rootID}},
	}
}

func (s *stubMurmur) TreeQuery(ctx context.Context, in *murmurrpc.Server) (*murmurrpc.Tree, error) {
	return &murmurrpc.Tree{Channel: &murmurrpc.Channel{Server: in, Id: s.rootID, Name: "Root"}}, nil
}

func (s *stubMurmur) ACLQuery(ctx context.Context, in *murmurrpc.Channel) (*murmurrpc.ACL, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Return a deep-enough copy that the caller's local edits (in
	// acl.go's findOrCreateGroup) don't alias the stub's own state until
	// ACLSet is actually called — matching a real server's read/write
	// separation.
	groupsCopy := make([]*murmurrpc.ACL_Group, len(s.acl.Groups))
	for i, g := range s.acl.Groups {
		addCopy := append([]uint32(nil), g.Add...)
		removeCopy := append([]uint32(nil), g.Remove...)
		groupsCopy[i] = &murmurrpc.ACL_Group{Name: g.Name, Inheritable: g.Inheritable, Add: addCopy, Remove: removeCopy}
	}
	return &murmurrpc.ACL{Channel: in, Groups: groupsCopy}, nil
}

func (s *stubMurmur) ACLSet(ctx context.Context, in *murmurrpc.ACL) (*murmurrpc.Void, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acl = in
	s.aclSets++
	return &murmurrpc.Void{}, nil
}

func (s *stubMurmur) groupMembers(name string) (add, remove []uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.acl.Groups {
		if g.Name == name {
			return append([]uint32(nil), g.Add...), append([]uint32(nil), g.Remove...)
		}
	}
	return nil, nil
}

// newStubClient starts stub over an in-memory bufconn listener and
// returns a real *mumble.Client dialed against it.
func newStubClient(t *testing.T, stub murmurrpc.V1Server) *mumble.Client {
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

	return &mumble.Client{
		Conn:   conn,
		RPC:    murmurrpc.NewV1Client(conn),
		Server: &murmurrpc.Server{Id: 1},
	}
}

// TestMumbleGRPCAddsAndRemovesACLGroups (roadmap exit criterion): against
// a stub MurmurRPC server, driver.go's Grant/Revoke correctly perform the
// read-modify-write ACL group membership dance.
func TestMumbleGRPCAddsAndRemovesACLGroups(t *testing.T) {
	stub := newStubMurmur(42)
	client := newStubClient(t, stub)
	driver := mumble.NewDriver(client, 0) // root channel resolved via TreeQuery

	require.NoError(t, driver.Grant(t.Context(), "1001", "officers"))
	add, remove := stub.groupMembers("officers")
	require.Contains(t, add, uint32(1001))
	require.NotContains(t, remove, uint32(1001))

	require.NoError(t, driver.Revoke(t.Context(), "1001", "officers"))
	add, remove = stub.groupMembers("officers")
	require.NotContains(t, add, uint32(1001))
	require.Contains(t, remove, uint32(1001))

	require.Equal(t, uint32(42), driver.ChannelID, "the root channel discovered via TreeQuery must be cached")
}

// TestMumbleGrantIsIdempotent: granting a user already in the Add list a
// second time must not duplicate the entry.
func TestMumbleGrantIsIdempotent(t *testing.T) {
	stub := newStubMurmur(1)
	client := newStubClient(t, stub)
	driver := mumble.NewDriver(client, 1)

	require.NoError(t, driver.Grant(t.Context(), "2002", "members"))
	require.NoError(t, driver.Grant(t.Context(), "2002", "members"))

	add, _ := stub.groupMembers("members")
	count := 0
	for _, v := range add {
		if v == 2002 {
			count++
		}
	}
	require.Equal(t, 1, count, "the user id must appear exactly once, not duplicated across two Grant calls")
}

// TestMumbleGrantRejectsNonNumericIdentity: a malformed remote identity
// fails cleanly rather than panicking or silently sending a zero id.
func TestMumbleGrantRejectsNonNumericIdentity(t *testing.T) {
	stub := newStubMurmur(1)
	client := newStubClient(t, stub)
	driver := mumble.NewDriver(client, 1)

	err := driver.Grant(t.Context(), "not-a-number", "members")
	require.Error(t, err)
}

// TestMumbleExplicitChannelSkipsTreeQuery: a Driver constructed with a
// non-zero channel id never calls TreeQuery at all.
func TestMumbleExplicitChannelSkipsTreeQuery(t *testing.T) {
	stub := newStubMurmur(999) // if TreeQuery were called, root would resolve to 999
	client := newStubClient(t, stub)
	driver := mumble.NewDriver(client, 5) // explicit channel 5, not the root

	require.NoError(t, driver.Grant(t.Context(), "1", "grp"))
	require.Equal(t, uint32(5), driver.ChannelID)
}
