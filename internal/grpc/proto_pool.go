package grpcutil

import (
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// ProtoMsgPool is a sync.Pool wrapper for dynamicpb.Message instances
// described by a specific protoreflect.MessageDescriptor. Each pool handles
// one message type to ensure schema correctness.
//
// Get() returns a message pre-cleared by proto.Reset. Put() calls proto.Reset
// before returning the message to the pool to clear any Value interfaces that
// might hold non-pooled allocations.
type ProtoMsgPool struct {
	desc protoreflect.MessageDescriptor
	pool sync.Pool
}

// NewProtoMsgPool creates a new pool for messages with the given descriptor.
func NewProtoMsgPool(desc protoreflect.MessageDescriptor) *ProtoMsgPool {
	return &ProtoMsgPool{
		desc: desc,
		pool: sync.Pool{},
	}
}

// Get returns a reset dynamicpb.Message from the pool, or allocates a new one
// if the pool is empty.
func (p *ProtoMsgPool) Get() *dynamicpb.Message {
	v := p.pool.Get()
	if v == nil {
		return dynamicpb.NewMessage(p.desc)
	}
	msg := v.(*dynamicpb.Message)
	proto.Reset(msg)
	return msg
}

// Put returns the message to the pool after calling proto.Reset to clear all
// fields and Value interface references.
func (p *ProtoMsgPool) Put(msg *dynamicpb.Message) {
	proto.Reset(msg)
	p.pool.Put(msg)
}
