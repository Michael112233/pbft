package transportpb

// ChannelKindMetadataKey distinguishes long-lived client streams from
// long-lived node-to-node streams on ClientNodeChannel. Keeping this in the
// transport package prevents the client and node packages from duplicating
// wire-level metadata values.
const ChannelKindMetadataKey = "pbft-channel-kind"

// ClientNameMetadataKey carries the client's name on a client stream, so a node
// can send each client the CommitTps for its own requests. A stream without it
// belongs to the legacy single client.
const ClientNameMetadataKey = "pbft-client-name"

const (
	ChannelKindClient = "client"
	ChannelKindNode   = "node"
)
