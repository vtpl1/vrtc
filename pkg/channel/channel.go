// Package channel provides types and interfaces for resolving camera/stream
// channel details from pluggable sources (JSON file, database, etc.).
//
// Core types are defined in the SDK (github.com/vtpl1/vrtc-sdk/config/channel)
// and re-exported here for backward compatibility. Provider implementations
// (file, MySQL, MongoDB) remain in this package.
package channel

import sdkchannel "github.com/vtpl1/vrtc-sdk/config/channel"

// Channel describes one camera or stream source.
type Channel = sdkchannel.Channel

// ChannelProvider is the single interface all channel sources must satisfy.
type ChannelProvider = sdkchannel.ChannelProvider

// ChannelWriter extends ChannelProvider with create/update/delete operations.
type ChannelWriter = sdkchannel.ChannelWriter

// ErrChannelNotFound is returned by ChannelProvider.GetChannel when the
// requested ID does not exist in the source.
var ErrChannelNotFound = sdkchannel.ErrChannelNotFound
