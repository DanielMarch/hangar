package mumble

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hangar-project/hangar/internal/provisioning/drivers/mumble/murmurrpc"
)

// Driver implements internal/provisioning.Driver against one Mumble
// server. remoteIdentity is the linked Mumble registered user id
// (app.provisioning_state.remote_identity, decimal string — Murmur's
// registered-user-id namespace, distinct from a connected session's
// ephemeral id); groupRef is the ACL group NAME
// (app.platform_group.remote_ref) on the managed channel.
type Driver struct {
	Client    *Client
	ChannelID uint32 // 0 means "not yet resolved — RootChannel() fills it in"
}

// NewDriver constructs a Driver. channelID of 0 defers root-channel
// discovery to the first Grant/Revoke call (RootChannel), matching
// 01_ARCHITECTURE.md §9.5: "HANGAR manages the root channel unless
// configured otherwise."
func NewDriver(client *Client, channelID uint32) *Driver {
	return &Driver{Client: client, ChannelID: channelID}
}

// RootChannel discovers the server's root channel id via TreeQuery — the
// channel HANGAR manages by default when no explicit channel is
// configured. Cached on the Driver once resolved (a server's root
// channel id never changes).
func (d *Driver) RootChannel(ctx context.Context) (uint32, error) {
	if d.ChannelID != 0 {
		return d.ChannelID, nil
	}
	tree, err := d.Client.RPC.TreeQuery(ctx, d.Client.Server)
	if err != nil {
		return 0, fmt.Errorf("mumble: querying tree for root channel: %w", err)
	}
	if tree.Channel == nil {
		return 0, fmt.Errorf("mumble: TreeQuery returned no root channel")
	}
	d.ChannelID = tree.Channel.Id
	return d.ChannelID, nil
}

// Grant adds remoteIdentity to Mumble ACL group groupRef on the managed
// channel — implements provisioning.Driver. Murmur exposes no
// single-member add/remove RPC (murmurrpc.proto's own doc comment): the
// whole ACL is read via ACLQuery, the target Group's Add/Remove lists are
// edited in place, and the WHOLE ACL is written back via ACLSet — this is
// the read-modify-write pattern that RPC shape forces.
func (d *Driver) Grant(ctx context.Context, remoteIdentity, groupRef string) error {
	return d.setMembership(ctx, remoteIdentity, groupRef, true)
}

// Revoke removes remoteIdentity from Mumble ACL group groupRef —
// implements provisioning.Driver.
func (d *Driver) Revoke(ctx context.Context, remoteIdentity, groupRef string) error {
	return d.setMembership(ctx, remoteIdentity, groupRef, false)
}

func (d *Driver) setMembership(ctx context.Context, remoteIdentity, groupRef string, add bool) error {
	userID, err := strconv.ParseUint(remoteIdentity, 10, 32)
	if err != nil {
		return fmt.Errorf("mumble: remote identity %q is not a valid Mumble user id: %w", remoteIdentity, err)
	}

	channelID, err := d.RootChannel(ctx)
	if err != nil {
		return err
	}
	channel := &murmurrpc.Channel{Server: d.Client.Server, Id: channelID}

	acl, err := d.Client.RPC.ACLQuery(ctx, channel)
	if err != nil {
		return fmt.Errorf("mumble: querying ACL for channel %d: %w", channelID, err)
	}

	group := findOrCreateGroup(acl, groupRef)
	if add {
		group.Add = addUint32(group.Add, uint32(userID))
		group.Remove = removeUint32(group.Remove, uint32(userID))
	} else {
		group.Remove = addUint32(group.Remove, uint32(userID))
		group.Add = removeUint32(group.Add, uint32(userID))
	}

	acl.Channel = channel
	if _, err := d.Client.RPC.ACLSet(ctx, acl); err != nil {
		return fmt.Errorf("mumble: setting ACL for channel %d: %w", channelID, err)
	}
	return nil
}

// findOrCreateGroup returns the Group named name within acl, appending a
// new empty one (inheritable, so it behaves like a normal Mumble group
// rather than a one-off) if none exists yet.
func findOrCreateGroup(acl *murmurrpc.ACL, name string) *murmurrpc.ACL_Group {
	for _, g := range acl.Groups {
		if g.Name == name {
			return g
		}
	}
	g := &murmurrpc.ACL_Group{Name: name, Inheritable: true}
	acl.Groups = append(acl.Groups, g)
	return g
}

func addUint32(list []uint32, v uint32) []uint32 {
	for _, existing := range list {
		if existing == v {
			return list // idempotent — provisioning.Driver's contract
		}
	}
	return append(list, v)
}

func removeUint32(list []uint32, v uint32) []uint32 {
	out := make([]uint32, 0, len(list))
	for _, existing := range list {
		if existing != v {
			out = append(out, existing)
		}
	}
	return out
}
