package teamspeak_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/teamspeak"
	"github.com/stretchr/testify/require"
)

func writeEnvelope(w http.ResponseWriter, code int, message string, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // TS3 WebQuery: HTTP status is ALWAYS 200 for a well-formed request
	rawBody, _ := json.Marshal(body)
	env := map[string]any{
		"status": map[string]any{"code": code, "message": message},
		"body":   json.RawMessage(rawBody),
	}
	_ = json.NewEncoder(w).Encode(env)
}

// TestTS3ErrorInsideHTTP200Detected (roadmap exit criterion): `error id=`
// in a 200 body is treated as a failure — Do never trusts resp.StatusCode.
func TestTS3ErrorInsideHTTP200Detected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 2568, "invalid clientID", []any{})
	}))
	defer server.Close()

	client := teamspeak.NewClient(server.URL, "test-key", 1, nil)
	_, err := client.Do(t.Context(), "clientgetdbidfromuid", map[string]string{"cluid": "abc"})
	require.Error(t, err)

	var tsErr *teamspeak.ErrTS3
	require.ErrorAs(t, err, &tsErr)
	require.Equal(t, 2568, tsErr.ID)
	require.Equal(t, "invalid clientID", tsErr.Msg)
}

// TestTS3SuccessCodeZeroReturnsBody: the control case — status.code == 0
// is success regardless of message text, and Do returns the body.
func TestTS3SuccessCodeZeroReturnsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 0, "ok", []map[string]string{{"cldbid": "42"}})
	}))
	defer server.Close()

	client := teamspeak.NewClient(server.URL, "test-key", 1, nil)
	body, err := client.Do(t.Context(), "clientgetdbidfromuid", map[string]string{"cluid": "abc"})
	require.NoError(t, err)
	require.Contains(t, string(body), "42")
}

// TestTS3ValuesEscapedBeforeSending: a parameter value containing a space
// arrives at the server TS3-escaped, proving Do applies Escape() to
// every value, not just ones a caller happens to pre-escape.
func TestTS3ValuesEscapedBeforeSending(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeEnvelope(w, 0, "ok", []any{})
	}))
	defer server.Close()

	client := teamspeak.NewClient(server.URL, "test-key", 1, nil)
	_, err := client.Do(t.Context(), "servergrouprename", map[string]string{"name": "Fleet Commanders"})
	require.NoError(t, err)
	require.Contains(t, gotQuery, "Fleet%5CsCommanders", "the space must have been TS3-escaped (\\s) before URL encoding, not sent as a literal space")
}

// TestTS3XAPIKeyHeaderSent: the x-api-key header is always present.
func TestTS3XAPIKeyHeaderSent(t *testing.T) {
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		writeEnvelope(w, 0, "ok", []any{})
	}))
	defer server.Close()

	client := teamspeak.NewClient(server.URL, "super-secret-key", 1, nil)
	_, err := client.Do(t.Context(), "whoami", nil)
	require.NoError(t, err)
	require.Equal(t, "super-secret-key", gotKey)
}
