// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.1
// 	protoc        (unknown)
// source: envd/network/v1/network.proto

package networkv1

import (
	_ "github.com/e2b-dev/infra/packages/envd/internal/services/spec/envd/permissions/v1"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type PortState int32

const (
	PortState_PORT_STATE_UNSPECIFIED PortState = 0
	PortState_PORT_STATE_OPEN        PortState = 1
	PortState_PORT_STATE_CLOSED      PortState = 2
)

// Enum value maps for PortState.
var (
	PortState_name = map[int32]string{
		0: "PORT_STATE_UNSPECIFIED",
		1: "PORT_STATE_OPEN",
		2: "PORT_STATE_CLOSED",
	}
	PortState_value = map[string]int32{
		"PORT_STATE_UNSPECIFIED": 0,
		"PORT_STATE_OPEN":        1,
		"PORT_STATE_CLOSED":      2,
	}
)

func (x PortState) Enum() *PortState {
	p := new(PortState)
	*p = x
	return p
}

func (x PortState) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (PortState) Descriptor() protoreflect.EnumDescriptor {
	return file_envd_network_v1_network_proto_enumTypes[0].Descriptor()
}

func (PortState) Type() protoreflect.EnumType {
	return &file_envd_network_v1_network_proto_enumTypes[0]
}

func (x PortState) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use PortState.Descriptor instead.
func (PortState) EnumDescriptor() ([]byte, []int) {
	return file_envd_network_v1_network_proto_rawDescGZIP(), []int{0}
}

type ListPortsRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *ListPortsRequest) Reset() {
	*x = ListPortsRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_envd_network_v1_network_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ListPortsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListPortsRequest) ProtoMessage() {}

func (x *ListPortsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_envd_network_v1_network_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListPortsRequest.ProtoReflect.Descriptor instead.
func (*ListPortsRequest) Descriptor() ([]byte, []int) {
	return file_envd_network_v1_network_proto_rawDescGZIP(), []int{0}
}

type ListPortsResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Ports []*Port `protobuf:"bytes,1,rep,name=ports,proto3" json:"ports,omitempty"`
}

func (x *ListPortsResponse) Reset() {
	*x = ListPortsResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_envd_network_v1_network_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ListPortsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListPortsResponse) ProtoMessage() {}

func (x *ListPortsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_envd_network_v1_network_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListPortsResponse.ProtoReflect.Descriptor instead.
func (*ListPortsResponse) Descriptor() ([]byte, []int) {
	return file_envd_network_v1_network_proto_rawDescGZIP(), []int{1}
}

func (x *ListPortsResponse) GetPorts() []*Port {
	if x != nil {
		return x.Ports
	}
	return nil
}

type WatchPortsRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Filter *PortFilter `protobuf:"bytes,1,opt,name=filter,proto3" json:"filter,omitempty"`
}

func (x *WatchPortsRequest) Reset() {
	*x = WatchPortsRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_envd_network_v1_network_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *WatchPortsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*WatchPortsRequest) ProtoMessage() {}

func (x *WatchPortsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_envd_network_v1_network_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use WatchPortsRequest.ProtoReflect.Descriptor instead.
func (*WatchPortsRequest) Descriptor() ([]byte, []int) {
	return file_envd_network_v1_network_proto_rawDescGZIP(), []int{2}
}

func (x *WatchPortsRequest) GetFilter() *PortFilter {
	if x != nil {
		return x.Filter
	}
	return nil
}

type PortFilter struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Ports []uint32 `protobuf:"varint,1,rep,packed,name=ports,proto3" json:"ports,omitempty"`
}

func (x *PortFilter) Reset() {
	*x = PortFilter{}
	if protoimpl.UnsafeEnabled {
		mi := &file_envd_network_v1_network_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PortFilter) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PortFilter) ProtoMessage() {}

func (x *PortFilter) ProtoReflect() protoreflect.Message {
	mi := &file_envd_network_v1_network_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PortFilter.ProtoReflect.Descriptor instead.
func (*PortFilter) Descriptor() ([]byte, []int) {
	return file_envd_network_v1_network_proto_rawDescGZIP(), []int{3}
}

func (x *PortFilter) GetPorts() []uint32 {
	if x != nil {
		return x.Ports
	}
	return nil
}

type WatchPortsResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Event *Port `protobuf:"bytes,1,opt,name=event,proto3" json:"event,omitempty"`
}

func (x *WatchPortsResponse) Reset() {
	*x = WatchPortsResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_envd_network_v1_network_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *WatchPortsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*WatchPortsResponse) ProtoMessage() {}

func (x *WatchPortsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_envd_network_v1_network_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use WatchPortsResponse.ProtoReflect.Descriptor instead.
func (*WatchPortsResponse) Descriptor() ([]byte, []int) {
	return file_envd_network_v1_network_proto_rawDescGZIP(), []int{4}
}

func (x *WatchPortsResponse) GetEvent() *Port {
	if x != nil {
		return x.Event
	}
	return nil
}

type Port struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Port  uint32    `protobuf:"varint,1,opt,name=port,proto3" json:"port,omitempty"`
	State PortState `protobuf:"varint,2,opt,name=state,proto3,enum=envd.network.v1.PortState" json:"state,omitempty"`
}

func (x *Port) Reset() {
	*x = Port{}
	if protoimpl.UnsafeEnabled {
		mi := &file_envd_network_v1_network_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Port) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Port) ProtoMessage() {}

func (x *Port) ProtoReflect() protoreflect.Message {
	mi := &file_envd_network_v1_network_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Port.ProtoReflect.Descriptor instead.
func (*Port) Descriptor() ([]byte, []int) {
	return file_envd_network_v1_network_proto_rawDescGZIP(), []int{5}
}

func (x *Port) GetPort() uint32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *Port) GetState() PortState {
	if x != nil {
		return x.State
	}
	return PortState_PORT_STATE_UNSPECIFIED
}

var File_envd_network_v1_network_proto protoreflect.FileDescriptor

var file_envd_network_v1_network_proto_rawDesc = []byte{
	0x0a, 0x1d, 0x65, 0x6e, 0x76, 0x64, 0x2f, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2f, 0x76,
	0x31, 0x2f, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12,
	0x0f, 0x65, 0x6e, 0x76, 0x64, 0x2e, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2e, 0x76, 0x31,
	0x1a, 0x25, 0x65, 0x6e, 0x76, 0x64, 0x2f, 0x70, 0x65, 0x72, 0x6d, 0x69, 0x73, 0x73, 0x69, 0x6f,
	0x6e, 0x73, 0x2f, 0x76, 0x31, 0x2f, 0x70, 0x65, 0x72, 0x6d, 0x69, 0x73, 0x73, 0x69, 0x6f, 0x6e,
	0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x12, 0x0a, 0x10, 0x4c, 0x69, 0x73, 0x74, 0x50,
	0x6f, 0x72, 0x74, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x22, 0x40, 0x0a, 0x11, 0x4c,
	0x69, 0x73, 0x74, 0x50, 0x6f, 0x72, 0x74, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x12, 0x2b, 0x0a, 0x05, 0x70, 0x6f, 0x72, 0x74, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32,
	0x15, 0x2e, 0x65, 0x6e, 0x76, 0x64, 0x2e, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2e, 0x76,
	0x31, 0x2e, 0x50, 0x6f, 0x72, 0x74, 0x52, 0x05, 0x70, 0x6f, 0x72, 0x74, 0x73, 0x22, 0x48, 0x0a,
	0x11, 0x57, 0x61, 0x74, 0x63, 0x68, 0x50, 0x6f, 0x72, 0x74, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x12, 0x33, 0x0a, 0x06, 0x66, 0x69, 0x6c, 0x74, 0x65, 0x72, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x1b, 0x2e, 0x65, 0x6e, 0x76, 0x64, 0x2e, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72,
	0x6b, 0x2e, 0x76, 0x31, 0x2e, 0x50, 0x6f, 0x72, 0x74, 0x46, 0x69, 0x6c, 0x74, 0x65, 0x72, 0x52,
	0x06, 0x66, 0x69, 0x6c, 0x74, 0x65, 0x72, 0x22, 0x22, 0x0a, 0x0a, 0x50, 0x6f, 0x72, 0x74, 0x46,
	0x69, 0x6c, 0x74, 0x65, 0x72, 0x12, 0x14, 0x0a, 0x05, 0x70, 0x6f, 0x72, 0x74, 0x73, 0x18, 0x01,
	0x20, 0x03, 0x28, 0x0d, 0x52, 0x05, 0x70, 0x6f, 0x72, 0x74, 0x73, 0x22, 0x41, 0x0a, 0x12, 0x57,
	0x61, 0x74, 0x63, 0x68, 0x50, 0x6f, 0x72, 0x74, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x12, 0x2b, 0x0a, 0x05, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x15, 0x2e, 0x65, 0x6e, 0x76, 0x64, 0x2e, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2e,
	0x76, 0x31, 0x2e, 0x50, 0x6f, 0x72, 0x74, 0x52, 0x05, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x22, 0x4c,
	0x0a, 0x04, 0x50, 0x6f, 0x72, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x0d, 0x52, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x12, 0x30, 0x0a, 0x05, 0x73, 0x74,
	0x61, 0x74, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x1a, 0x2e, 0x65, 0x6e, 0x76, 0x64,
	0x2e, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2e, 0x76, 0x31, 0x2e, 0x50, 0x6f, 0x72, 0x74,
	0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x05, 0x73, 0x74, 0x61, 0x74, 0x65, 0x2a, 0x53, 0x0a, 0x09,
	0x50, 0x6f, 0x72, 0x74, 0x53, 0x74, 0x61, 0x74, 0x65, 0x12, 0x1a, 0x0a, 0x16, 0x50, 0x4f, 0x52,
	0x54, 0x5f, 0x53, 0x54, 0x41, 0x54, 0x45, 0x5f, 0x55, 0x4e, 0x53, 0x50, 0x45, 0x43, 0x49, 0x46,
	0x49, 0x45, 0x44, 0x10, 0x00, 0x12, 0x13, 0x0a, 0x0f, 0x50, 0x4f, 0x52, 0x54, 0x5f, 0x53, 0x54,
	0x41, 0x54, 0x45, 0x5f, 0x4f, 0x50, 0x45, 0x4e, 0x10, 0x01, 0x12, 0x15, 0x0a, 0x11, 0x50, 0x4f,
	0x52, 0x54, 0x5f, 0x53, 0x54, 0x41, 0x54, 0x45, 0x5f, 0x43, 0x4c, 0x4f, 0x53, 0x45, 0x44, 0x10,
	0x02, 0x32, 0xbd, 0x01, 0x0a, 0x0e, 0x4e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x53, 0x65, 0x72,
	0x76, 0x69, 0x63, 0x65, 0x12, 0x52, 0x0a, 0x09, 0x4c, 0x69, 0x73, 0x74, 0x50, 0x6f, 0x72, 0x74,
	0x73, 0x12, 0x21, 0x2e, 0x65, 0x6e, 0x76, 0x64, 0x2e, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b,
	0x2e, 0x76, 0x31, 0x2e, 0x4c, 0x69, 0x73, 0x74, 0x50, 0x6f, 0x72, 0x74, 0x73, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x1a, 0x22, 0x2e, 0x65, 0x6e, 0x76, 0x64, 0x2e, 0x6e, 0x65, 0x74, 0x77,
	0x6f, 0x72, 0x6b, 0x2e, 0x76, 0x31, 0x2e, 0x4c, 0x69, 0x73, 0x74, 0x50, 0x6f, 0x72, 0x74, 0x73,
	0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x57, 0x0a, 0x0a, 0x57, 0x61, 0x74, 0x63,
	0x68, 0x50, 0x6f, 0x72, 0x74, 0x73, 0x12, 0x22, 0x2e, 0x65, 0x6e, 0x76, 0x64, 0x2e, 0x6e, 0x65,
	0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2e, 0x76, 0x31, 0x2e, 0x57, 0x61, 0x74, 0x63, 0x68, 0x50, 0x6f,
	0x72, 0x74, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x23, 0x2e, 0x65, 0x6e, 0x76,
	0x64, 0x2e, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2e, 0x76, 0x31, 0x2e, 0x57, 0x61, 0x74,
	0x63, 0x68, 0x50, 0x6f, 0x72, 0x74, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x30,
	0x01, 0x42, 0xda, 0x01, 0x0a, 0x13, 0x63, 0x6f, 0x6d, 0x2e, 0x65, 0x6e, 0x76, 0x64, 0x2e, 0x6e,
	0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2e, 0x76, 0x31, 0x42, 0x0c, 0x4e, 0x65, 0x74, 0x77, 0x6f,
	0x72, 0x6b, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x57, 0x67, 0x69, 0x74, 0x68, 0x75,
	0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x65, 0x32, 0x62, 0x2d, 0x64, 0x65, 0x76, 0x2f, 0x69, 0x6e,
	0x66, 0x72, 0x61, 0x2f, 0x70, 0x61, 0x63, 0x6b, 0x61, 0x67, 0x65, 0x73, 0x2f, 0x65, 0x6e, 0x76,
	0x64, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x73, 0x65, 0x72, 0x76, 0x69,
	0x63, 0x65, 0x73, 0x2f, 0x73, 0x70, 0x65, 0x63, 0x2f, 0x65, 0x6e, 0x76, 0x64, 0x2f, 0x6e, 0x65,
	0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2f, 0x76, 0x31, 0x3b, 0x6e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b,
	0x76, 0x31, 0xa2, 0x02, 0x03, 0x45, 0x4e, 0x58, 0xaa, 0x02, 0x0f, 0x45, 0x6e, 0x76, 0x64, 0x2e,
	0x4e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x2e, 0x56, 0x31, 0xca, 0x02, 0x0f, 0x45, 0x6e, 0x76,
	0x64, 0x5c, 0x4e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x5c, 0x56, 0x31, 0xe2, 0x02, 0x1b, 0x45,
	0x6e, 0x76, 0x64, 0x5c, 0x4e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x5c, 0x56, 0x31, 0x5c, 0x47,
	0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x11, 0x45, 0x6e, 0x76,
	0x64, 0x3a, 0x3a, 0x4e, 0x65, 0x74, 0x77, 0x6f, 0x72, 0x6b, 0x3a, 0x3a, 0x56, 0x31, 0x62, 0x06,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_envd_network_v1_network_proto_rawDescOnce sync.Once
	file_envd_network_v1_network_proto_rawDescData = file_envd_network_v1_network_proto_rawDesc
)

func file_envd_network_v1_network_proto_rawDescGZIP() []byte {
	file_envd_network_v1_network_proto_rawDescOnce.Do(func() {
		file_envd_network_v1_network_proto_rawDescData = protoimpl.X.CompressGZIP(file_envd_network_v1_network_proto_rawDescData)
	})
	return file_envd_network_v1_network_proto_rawDescData
}

var file_envd_network_v1_network_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_envd_network_v1_network_proto_msgTypes = make([]protoimpl.MessageInfo, 6)
var file_envd_network_v1_network_proto_goTypes = []interface{}{
	(PortState)(0),             // 0: envd.network.v1.PortState
	(*ListPortsRequest)(nil),   // 1: envd.network.v1.ListPortsRequest
	(*ListPortsResponse)(nil),  // 2: envd.network.v1.ListPortsResponse
	(*WatchPortsRequest)(nil),  // 3: envd.network.v1.WatchPortsRequest
	(*PortFilter)(nil),         // 4: envd.network.v1.PortFilter
	(*WatchPortsResponse)(nil), // 5: envd.network.v1.WatchPortsResponse
	(*Port)(nil),               // 6: envd.network.v1.Port
}
var file_envd_network_v1_network_proto_depIdxs = []int32{
	6, // 0: envd.network.v1.ListPortsResponse.ports:type_name -> envd.network.v1.Port
	4, // 1: envd.network.v1.WatchPortsRequest.filter:type_name -> envd.network.v1.PortFilter
	6, // 2: envd.network.v1.WatchPortsResponse.event:type_name -> envd.network.v1.Port
	0, // 3: envd.network.v1.Port.state:type_name -> envd.network.v1.PortState
	1, // 4: envd.network.v1.NetworkService.ListPorts:input_type -> envd.network.v1.ListPortsRequest
	3, // 5: envd.network.v1.NetworkService.WatchPorts:input_type -> envd.network.v1.WatchPortsRequest
	2, // 6: envd.network.v1.NetworkService.ListPorts:output_type -> envd.network.v1.ListPortsResponse
	5, // 7: envd.network.v1.NetworkService.WatchPorts:output_type -> envd.network.v1.WatchPortsResponse
	6, // [6:8] is the sub-list for method output_type
	4, // [4:6] is the sub-list for method input_type
	4, // [4:4] is the sub-list for extension type_name
	4, // [4:4] is the sub-list for extension extendee
	0, // [0:4] is the sub-list for field type_name
}

func init() { file_envd_network_v1_network_proto_init() }
func file_envd_network_v1_network_proto_init() {
	if File_envd_network_v1_network_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_envd_network_v1_network_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ListPortsRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_envd_network_v1_network_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ListPortsResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_envd_network_v1_network_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*WatchPortsRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_envd_network_v1_network_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PortFilter); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_envd_network_v1_network_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*WatchPortsResponse); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_envd_network_v1_network_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Port); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_envd_network_v1_network_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   6,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_envd_network_v1_network_proto_goTypes,
		DependencyIndexes: file_envd_network_v1_network_proto_depIdxs,
		EnumInfos:         file_envd_network_v1_network_proto_enumTypes,
		MessageInfos:      file_envd_network_v1_network_proto_msgTypes,
	}.Build()
	File_envd_network_v1_network_proto = out.File
	file_envd_network_v1_network_proto_rawDesc = nil
	file_envd_network_v1_network_proto_goTypes = nil
	file_envd_network_v1_network_proto_depIdxs = nil
}