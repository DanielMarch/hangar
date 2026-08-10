package teamspeak_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/teamspeak"
	"github.com/stretchr/testify/require"
)

// mockTS3Server is a hand-rolled TS3 WebQuery stub — driver.go's real
// caller shape, not a mocked Go interface.
type mockTS3Server struct {
	AddResultCode int // 0 = success; non-zero simulates a TS3 error
	DelResultCode int
	Calls         []string
}

func (m *mockTS3Server) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/1/")
		m.Calls = append(m.Calls, path+"?"+r.URL.RawQuery)
		switch path {
		case "clientgetdbidfromuid":
			writeEnvelope(w, 0, "ok", []map[string]string{{"cldbid": "77"}})
		case "servergroupaddclient":
			writeEnvelope(w, m.AddResultCode, resultMessage(m.AddResultCode), []any{})
		case "servergroupdelclient":
			writeEnvelope(w, m.DelResultCode, resultMessage(m.DelResultCode), []any{})
		default:
			writeEnvelope(w, 1, "unknown command", []any{})
		}
	}
}

func resultMessage(code int) string {
	if code == 0 {
		return "ok"
	}
	return "simulated error"
}

func TestDriverGrantResolvesAndAddsMember(t *testing.T) {
	m := &mockTS3Server{}
	server := httptest.NewServer(m.handler())
	defer server.Close()

	client := teamspeak.NewClient(server.URL, "key", 1, nil)
	driver := teamspeak.NewDriver(client)

	require.NoError(t, driver.Grant(t.Context(), "base64uid==", "6"))
	require.Len(t, m.Calls, 2)
	require.Contains(t, m.Calls[0], "clientgetdbidfromuid")
	require.Contains(t, m.Calls[1], "servergroupaddclient")
	require.Contains(t, m.Calls[1], "cldbid=77")
	require.Contains(t, m.Calls[1], "sgid=6")
}

// TestDriverGrantIdempotentOnDuplicateEntry: adding a client already in
// the group must not surface as an error (provisioning.Driver's
// idempotency contract).
func TestDriverGrantIdempotentOnDuplicateEntry(t *testing.T) {
	m := &mockTS3Server{AddResultCode: 2560} // "duplicate entry"
	server := httptest.NewServer(m.handler())
	defer server.Close()

	driver := teamspeak.NewDriver(teamspeak.NewClient(server.URL, "key", 1, nil))
	require.NoError(t, driver.Grant(t.Context(), "uid", "6"))
}

// TestDriverRevokeIdempotentOnEmptyResult: removing a client that was
// never in the group must not surface as an error.
func TestDriverRevokeIdempotentOnEmptyResult(t *testing.T) {
	m := &mockTS3Server{DelResultCode: 1281} // "database empty result set"
	server := httptest.NewServer(m.handler())
	defer server.Close()

	driver := teamspeak.NewDriver(teamspeak.NewClient(server.URL, "key", 1, nil))
	require.NoError(t, driver.Revoke(t.Context(), "uid", "6"))
}

// TestDriverGrantSurfacesRealErrors: an error that ISN'T the known
// idempotent case must still be reported.
func TestDriverGrantSurfacesRealErrors(t *testing.T) {
	m := &mockTS3Server{AddResultCode: 2568} // "invalid clientID" — not idempotent
	server := httptest.NewServer(m.handler())
	defer server.Close()

	driver := teamspeak.NewDriver(teamspeak.NewClient(server.URL, "key", 1, nil))
	require.Error(t, driver.Grant(t.Context(), "uid", "6"))
}
