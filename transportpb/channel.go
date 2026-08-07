package transportpb

// ChannelKindMetadataKey distinguishes long-lived client streams from
// long-lived node-to-node streams on ClientNodeChannel. Keeping this in the
// transport package prevents the client and node packages from duplicating
// wire-level metadata values.
const ChannelKindMetadataKey = "pbft-channel-kind"

const (
	ChannelKindClient = "client"
	ChannelKindNode   = "node"
)
