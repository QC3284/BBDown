// Package proto contains hand-written protobuf types for Widevine DRM.
// Generated from widevine.proto — simplified for our usage.
package proto

// WidevineCencHeader inside PSSH box payload.
type WidevineCencHeader struct {
	Algorithm        *int32   `protobuf:"varint,1,opt,name=algorithm,enum=widevine_proto.WidevineCencHeader_Algorithm" json:"algorithm,omitempty"`
	KeyIds           [][]byte `protobuf:"bytes,2,rep,name=key_ids" json:"key_ids,omitempty"`
	Provider         *string  `protobuf:"bytes,3,opt,name=provider" json:"provider,omitempty"`
	ContentId        []byte   `protobuf:"bytes,4,opt,name=content_id" json:"content_id,omitempty"`
	ProtectionScheme *uint32  `protobuf:"varint,9,opt,name=protection_scheme" json:"protection_scheme,omitempty"`
}

// ClientIdentification embedded in .wvd file.
type ClientIdentification struct {
	Type                *int32  `protobuf:"varint,1,opt,name=type" json:"type,omitempty"`
	Token               []byte  `protobuf:"bytes,2,opt,name=token" json:"token,omitempty"`
	ClientInfo          []*ClientIdentification_NameValue `protobuf:"bytes,3,rep,name=client_info" json:"client_info,omitempty"`
	ProviderClientToken []byte  `protobuf:"bytes,4,opt,name=provider_client_token" json:"provider_client_token,omitempty"`
	LicenseCounter      *uint32 `protobuf:"varint,5,opt,name=license_counter" json:"license_counter,omitempty"`
	VmpData             []byte  `protobuf:"bytes,7,opt,name=vmp_data" json:"vmp_data,omitempty"`
}

type ClientIdentification_NameValue struct {
	Name  *string `protobuf:"bytes,1,opt,name=name" json:"name,omitempty"`
	Value *string `protobuf:"bytes,2,opt,name=value" json:"value,omitempty"`
}

// SignedMessage is the envelope for license requests and responses.
type SignedMessage struct {
	Type                 *int32 `protobuf:"varint,1,opt,name=type" json:"type,omitempty"`
	Msg                  []byte `protobuf:"bytes,2,opt,name=msg" json:"msg,omitempty"`
	Signature            []byte `protobuf:"bytes,3,opt,name=signature" json:"signature,omitempty"`
	SessionKey           []byte `protobuf:"bytes,4,opt,name=session_key" json:"session_key,omitempty"`
	RemoteAttestation    []byte `protobuf:"bytes,5,opt,name=remote_attestation" json:"remote_attestation,omitempty"`
	SessionKeyType       *int32 `protobuf:"varint,8,opt,name=session_key_type" json:"session_key_type,omitempty"`
	OemcryptoCoreMessage []byte `protobuf:"bytes,9,opt,name=oemcrypto_core_message" json:"oemcrypto_core_message,omitempty"`
}

// SignedMessage type constants.
const (
	SignedMessage_LICENSE_REQUEST = 1
	SignedMessage_LICENSE         = 2
	SignedMessage_ERROR_RESPONSE  = 3
)

// LicenseRequest for Widevine license acquisition.
type LicenseRequest struct {
	ClientId       *ClientIdentification                 `protobuf:"bytes,1,opt,name=client_id" json:"client_id,omitempty"`
	ContentId      *LicenseRequest_ContentIdentification `protobuf:"bytes,2,opt,name=content_id" json:"content_id,omitempty"`
	Type           *int32                                 `protobuf:"varint,3,opt,name=type" json:"type,omitempty"`
	RequestTime    *int64                                 `protobuf:"varint,4,opt,name=request_time" json:"request_time,omitempty"`
	ProtocolVersion *int32                                `protobuf:"varint,6,opt,name=protocol_version" json:"protocol_version,omitempty"`
	KeyControlNonce *uint32                               `protobuf:"varint,7,opt,name=key_control_nonce" json:"key_control_nonce,omitempty"`
}

type LicenseRequest_ContentIdentification struct {
	WidevinePsshData *LicenseRequest_ContentIdentification_WidevinePsshData `protobuf:"bytes,1,opt,name=widevine_pssh_data" json:"widevine_pssh_data,omitempty"`
}

type LicenseRequest_ContentIdentification_WidevinePsshData struct {
	PsshData    [][]byte `protobuf:"bytes,1,rep,name=pssh_data" json:"pssh_data,omitempty"`
	LicenseType *int32   `protobuf:"varint,2,opt,name=license_type" json:"license_type,omitempty"`
	RequestId   []byte   `protobuf:"bytes,3,opt,name=request_id" json:"request_id,omitempty"`
}

// LicenseRequest type constants.
const (
	LicenseRequest_NEW      = 1
	LicenseRequest_RENEWAL  = 2
	LicenseRequest_RELEASE  = 3
)

// Protocol version constants.
const (
	ProtocolVersion_V2_0 = 20
	ProtocolVersion_V2_1 = 21
)

// LicenseType constants.
const (
	LicenseType_STREAMING = 1
	LicenseType_OFFLINE   = 2
	LicenseType_AUTOMATIC = 3
)

// License is the decrypted license response content.
type License struct {
	Key []*License_KeyContainer `protobuf:"bytes,3,rep,name=key" json:"key,omitempty"`
}

type License_KeyContainer struct {
	Id  []byte `protobuf:"bytes,1,opt,name=id" json:"id,omitempty"`
	Iv  []byte `protobuf:"bytes,2,opt,name=iv" json:"iv,omitempty"`
	Key []byte `protobuf:"bytes,3,opt,name=key" json:"key,omitempty"`
	Type *int32 `protobuf:"varint,4,opt,name=type" json:"type,omitempty"`
}

// License_KeyContainer key type constants.
const (
	License_KeyContainer_SIGNING           = 1
	License_KeyContainer_CONTENT           = 2
	License_KeyContainer_KEY_CONTROL       = 3
	License_KeyContainer_OPERATOR_SESSION  = 4
	License_KeyContainer_ENTITLEMENT       = 5
	License_KeyContainer_OEM_CONTENT       = 6
)
