package teamspeak

import (
	"context"
	"encoding/json"
	"fmt"
)

// Driver implements internal/provisioning.Driver against one TS3 virtual
// server. remoteIdentity is the linked client_unique_identifier (the
// base64 client UID bound via a redeemed challenge token);
// groupRef is the TS3 server group id (app.platform_group.remote_ref),
// as a decimal string.
type Driver struct {
	Client *Client
}

// NewDriver constructs a Driver.
func NewDriver(client *Client) *Driver {
	return &Driver{Client: client}
}

// resolveCldbid resolves a client_unique_identifier to the TS3 client
// database id (cldbid) that servergroupaddclient/servergroupdelclient
// actually operate on — uid and cldbid are different TS3 namespaces
// (clientgetdbidfromuid's whole reason to exist).
func (d *Driver) resolveCldbid(ctx context.Context, uid string) (string, error) {
	body, err := d.Client.Do(ctx, "clientgetdbidfromuid", map[string]string{"cluid": uid})
	if err != nil {
		return "", fmt.Errorf("teamspeak: resolving cldbid for uid %s: %w", uid, err)
	}
	var rows []struct {
		Cldbid string `json:"cldbid"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return "", fmt.Errorf("teamspeak: decoding clientgetdbidfromuid response: %w", err)
	}
	if len(rows) == 0 || rows[0].Cldbid == "" {
		return "", fmt.Errorf("teamspeak: no cldbid found for uid %s", uid)
	}
	return rows[0].Cldbid, nil
}

// Grant adds remoteIdentity to TS3 server group groupRef — implements
// provisioning.Driver. Idempotent: a "duplicate entry" TS3 error (the
// client already belongs to the group) is treated as success.
func (d *Driver) Grant(ctx context.Context, remoteIdentity, groupRef string) error {
	cldbid, err := d.resolveCldbid(ctx, remoteIdentity)
	if err != nil {
		return err
	}
	_, err = d.Client.Do(ctx, "servergroupaddclient", map[string]string{"sgid": groupRef, "cldbid": cldbid})
	if err != nil {
		if isIdempotentGrantError(err) {
			return nil
		}
		return fmt.Errorf("teamspeak: grant: %w", err)
	}
	return nil
}

// Revoke removes remoteIdentity from TS3 server group groupRef —
// implements provisioning.Driver. Idempotent: an "empty result" TS3
// error (the client was never in the group) is treated as success.
func (d *Driver) Revoke(ctx context.Context, remoteIdentity, groupRef string) error {
	cldbid, err := d.resolveCldbid(ctx, remoteIdentity)
	if err != nil {
		return err
	}
	_, err = d.Client.Do(ctx, "servergroupdelclient", map[string]string{"sgid": groupRef, "cldbid": cldbid})
	if err != nil {
		if isIdempotentRevokeError(err) {
			return nil
		}
		return fmt.Errorf("teamspeak: revoke: %w", err)
	}
	return nil
}
