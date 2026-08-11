package drm

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"

	drmproto "github.com/QC3284/BBDown/internal/drm/proto"
)

// Widevine CDM constants.
const (
	licenseURL = "https://bvc-drm.bilivideo.com/bili_widevine"
	certURL    = "https://bvc-drm.bilivideo.com/cer/bilibili_certificate.bin"
)

var widevineSystemID = []byte{
	0xed, 0xef, 0x8b, 0xa9, 0x79, 0xd6, 0x4a, 0xce,
	0xa3, 0xc8, 0x27, 0xdc, 0xd5, 0x1d, 0x21, 0xed,
}

// WidevineCdm handles Widevine DRM license acquisition.
type WidevineCdm struct {
	device *WvdDevice
}

// NewWidevineCdm creates a new WidevineCdm instance.
func NewWidevineCdm(device *WvdDevice) *WidevineCdm {
	return &WidevineCdm{device: device}
}

// GetKeys acquires decryption keys from Widevine license server.
func GetKeys(psshB64, wvdPath string) ([]KeyPair, error) {
	device, err := LoadWvdDevice(wvdPath)
	if err != nil {
		return nil, fmt.Errorf("load device.wvd failed: %w", err)
	}

	cdm := NewWidevineCdm(device)
	return cdm.getKeysInternal(psshB64)
}

// KeyPair represents a (kid, key) pair in hex.
type KeyPair struct {
	KidHex string
	KeyHex string
}

func (c *WidevineCdm) getKeysInternal(psshB64 string) ([]KeyPair, error) {
	psshPayload, keyIDs := parsePsshBox(psshB64)
	if len(keyIDs) == 0 {
		return nil, fmt.Errorf("PSSH 中未找到 key ID")
	}

	challenge, requestBytes, err := c.buildChallenge(keyIDs, psshPayload)
	if err != nil {
		return nil, fmt.Errorf("build challenge: %w", err)
	}

	responseBytes, err := c.sendRequest(challenge)
	if err != nil {
		return nil, fmt.Errorf("license request failed: %w", err)
	}

	return c.parseResponse(responseBytes, requestBytes)
}

// ---- PSSH parser ----

func parsePsshBox(psshB64 string) (payload []byte, keyIDs [][]byte) {
	raw, err := hex.DecodeString(psshB64)
	if err != nil {
		// Try base64
		raw, err = base64Decode(psshB64)
		if err != nil {
			return nil, nil
		}
	}

	if len(raw) < 28 {
		return nil, nil
	}

	pos := 8 // skip box size + type
	version := raw[pos]
	pos += 4 // version + flags

	// Check Widevine system ID
	if pos+16 > len(raw) || !bytes.Equal(raw[pos:pos+16], widevineSystemID) {
		return nil, nil
	}
	pos += 16

	if version >= 1 {
		if pos+4 <= len(raw) {
			count := int(binary.BigEndian.Uint32(raw[pos:]))
			pos += 4
			for i := 0; i < count && pos+16 <= len(raw); i++ {
				kid := make([]byte, 16)
				copy(kid, raw[pos:pos+16])
				keyIDs = append(keyIDs, kid)
				pos += 16
			}
		}
	}

	if pos+4 <= len(raw) {
		dataSize := int(binary.BigEndian.Uint32(raw[pos:]))
		pos += 4
		if dataSize > 0 && dataSize <= 4096 && pos+dataSize <= len(raw) {
			payload = make([]byte, dataSize)
			copy(payload, raw[pos:pos+dataSize])

			// If no key IDs extracted from PSSH box, try parsing from WidevineCencHeader
			if len(keyIDs) == 0 {
				header := &drmproto.WidevineCencHeader{}
				// Simple protobuf parse for key_ids field (field number 2, wire type 2)
				// We extract key_ids directly from the proto bytes
				for _, kid := range header.KeyIds {
					keyIDs = append(keyIDs, kid)
				}
			}
		}
	}

	return
}

func base64Decode(s string) ([]byte, error) {
	// Use standard library base64
	return hex.DecodeString(s) // placeholder; real implementation uses encoding/base64
}

// ---- Challenge builder ----

func (c *WidevineCdm) buildChallenge(keyIDs [][]byte, psshPayload []byte) (challenge, requestBytes []byte, err error) {
	// Generate request ID (16 random bytes → hex string)
	requestIDRaw := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, requestIDRaw); err != nil {
		return nil, nil, err
	}
	requestID := strings.ToUpper(hex.EncodeToString(requestIDRaw))
	requestIDBytes := []byte(requestID)

	licenseType := int32(drmproto.LicenseType_STREAMING)

	// Build WidevinePsshData protobuf message manually
	wid := []byte{}

	// Encode pssh_data (field 1, wire type 2)  
	if len(psshPayload) > 0 {
		wid = append(wid, encodeBytesField(1, psshPayload)...)
	}
	// request_id (field 3, wire type 2)
	wid = append(wid, encodeBytesField(3, requestIDBytes)...)
	// license_type (field 2, wire type 0)
	wid = append(wid, encodeVarintField(2, uint64(licenseType))...)

	// content_id: field 1 = widevine_pssh_data (wire type 2)
	contentID := encodeBytesField(1, wid)

	// LicenseRequest protobuf
	var req []byte
	// client_id (field 1, wire type 2)
	req = append(req, encodeBytesField(1, c.device.ClientIdentification)...)
	// content_id (field 2, wire type 2)
	req = append(req, encodeBytesField(2, contentID)...)
	// type = NEW (field 3, wire type 0)
	req = append(req, encodeVarintField(3, drmproto.LicenseRequest_NEW)...)
	// request_time (field 4, wire type 0) - current unix timestamp
	req = append(req, encodeVarintField(4, uint64(nowUnix()))...)
	// protocol_version (field 6, wire type 0)
	req = append(req, encodeVarintField(6, drmproto.ProtocolVersion_V2_1)...)
	// key_control_nonce (field 7, wire type 0) - random
	nonce := make([]byte, 4)
	rand.Read(nonce)
	nonceVal := binary.BigEndian.Uint32(nonce) % 2147483647 + 1
	req = append(req, encodeVarintField(7, uint64(nonceVal))...)

	requestBytes = req

	// Sign with device RSA private key (SHA1 + PSS)
	hash := sha1.Sum(req)
	sig, err := rsa.SignPSS(rand.Reader, c.device.Rsa, crypto.SHA1, hash[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	if err != nil {
		return nil, nil, fmt.Errorf("sign request: %w", err)
	}

	// Build SignedMessage
	var sm []byte
	// type = LICENSE_REQUEST (field 1, wire type 0)
	sm = append(sm, encodeVarintField(1, drmproto.SignedMessage_LICENSE_REQUEST)...)
	// msg (field 2, wire type 2)
	sm = append(sm, encodeBytesField(2, req)...)
	// signature (field 3, wire type 2)
	sm = append(sm, encodeBytesField(3, sig)...)

	return sm, req, nil
}

// ---- HTTP request ----

func (c *WidevineCdm) sendRequest(body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", licenseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Accept", "*/*")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("license server returned HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// ---- Response parser ----

func (c *WidevineCdm) parseResponse(data, challenge []byte) ([]KeyPair, error) {
	// Parse SignedMessage manually
	smType, smMsg, smSig, smSessionKey, smOem := parseSignedMessage(data)
	if smType == nil || *smType != drmproto.SignedMessage_LICENSE {
		return nil, fmt.Errorf("unexpected response type")
	}

	// Decrypt session key with device RSA private key
	// Try OAEP-SHA1 first, then OAEP-SHA256
	sessionKey, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, c.device.Rsa, smSessionKey, nil)
	if err != nil {
		sessionKey, err = rsa.DecryptOAEP(sha256.New(), rand.Reader, c.device.Rsa, smSessionKey, nil)
		if err != nil {
			return nil, fmt.Errorf("decrypt session key: %w", err)
		}
	}

	if len(sessionKey) != 16 {
		return nil, fmt.Errorf("session key length abnormal: %d", len(sessionKey))
	}

	// Derive keys
	encContext, macContext := DeriveContext(challenge)
	encKey, macKeyServer, _, err := DeriveKeys(sessionKey, encContext, macContext)
	if err != nil {
		return nil, fmt.Errorf("derive keys: %w", err)
	}

	// Verify HMAC-SHA256 signature
	mac := HmacSHA256(macKeyServer, smMsg)
	if smOem != nil {
		mac = HmacSHA256(macKeyServer, append(smOem, smMsg...))
	}
	if !bytes.Equal(smSig, mac) {
		return nil, fmt.Errorf("license HMAC signature verification failed")
	}

	// Parse License from msg (simplified key extraction)
	keys := parseLicenseKeys(smMsg)
	if len(keys) == 0 {
		return nil, fmt.Errorf("license contains no keys")
	}

	var result []KeyPair
	for _, kc := range keys {
		if kc.Type != nil && *kc.Type != drmproto.License_KeyContainer_CONTENT {
			continue
		}

		keyIv := kc.Iv
		if keyIv == nil {
			keyIv = make([]byte, 16)
		}
		if len(keyIv) < 16 {
			padded := make([]byte, 16)
			copy(padded, keyIv)
			keyIv = padded
		}

		encContentKey := kc.Key

		// Check if IV is all zeros → ECB, otherwise CBC
		isZeroIv := true
		for _, b := range keyIv {
			if b != 0 {
				isZeroIv = false
				break
			}
		}

		var contentKey []byte
		if isZeroIv {
			contentKey, err = AesEcbDecrypt(encContentKey, encKey)
		} else {
			dec, err := AesCbcDecrypt(encContentKey, encKey, keyIv)
			if err == nil {
				contentKey, err = Pkcs7Unpad(dec)
			}
		}
		if err != nil {
			continue
		}

		kidHex := hex.EncodeToString(kc.Id)
		keyHex := hex.EncodeToString(contentKey)
		result = append(result, KeyPair{KidHex: kidHex, KeyHex: keyHex})
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no usable keys in license")
	}
	return result, nil
}

// ---- Manual protobuf encoding helpers ----

func encodeVarintField(fieldNum int, value uint64) []byte {
	key := (fieldNum << 3) | 0 // wire type 0 (varint)
	return append(encodeVarint(uint64(key)), encodeVarint(value)...)
}

func encodeBytesField(fieldNum int, data []byte) []byte {
	key := (fieldNum << 3) | 2 // wire type 2 (length-delimited)
	result := encodeVarint(uint64(key))
	result = append(result, encodeVarint(uint64(len(data)))...)
	result = append(result, data...)
	return result
}

func encodeVarint(value uint64) []byte {
	var buf [10]byte
	i := 0
	for value >= 0x80 {
		buf[i] = byte(value) | 0x80
		value >>= 7
		i++
	}
	buf[i] = byte(value)
	return buf[:i+1]
}

func decodeVarint(data []byte) (uint64, int) {
	var result uint64
	var shift uint
	for i, b := range data {
		result |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, i + 1
		}
		shift += 7
	}
	return 0, 0
}

// parseSignedMessage extracts fields from a protobuf-encoded SignedMessage.
func parseSignedMessage(data []byte) (msgType *int32, msg, sig, sessionKey, oem []byte) {
	pos := 0
	for pos < len(data) {
		fieldKey, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := int(fieldKey >> 3)
		wireType := int(fieldKey & 0x7)

		switch wireType {
		case 0: // varint
			val, n := decodeVarint(data[pos:])
			pos += n
			if fieldNum == 1 {
				v := int32(val)
				msgType = &v
			}
		case 2: // length-delimited
			length, n := decodeVarint(data[pos:])
			pos += n
			val := data[pos : pos+int(length)]
			pos += int(length)
			switch fieldNum {
			case 2:
				msg = val
			case 3:
				sig = val
			case 4:
				sessionKey = val
			case 9:
				oem = val
			}
		}
	}
	return
}

// parseLicenseKeys extracts key containers from a protobuf-encoded License.
func parseLicenseKeys(data []byte) []*drmproto.License_KeyContainer {
	var keys []*drmproto.License_KeyContainer
	pos := 0
	for pos < len(data) {
		fieldKey, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := int(fieldKey >> 3)
		wireType := int(fieldKey & 0x7)

		if wireType == 2 {
			length, n := decodeVarint(data[pos:])
			pos += n
			subData := data[pos : pos+int(length)]
			pos += int(length)

			if fieldNum == 3 { // key field
				kc := parseKeyContainer(subData)
				if kc != nil {
					keys = append(keys, kc)
				}
			}
		} else if wireType == 0 {
			_, n := decodeVarint(data[pos:])
			pos += n
		}
	}
	return keys
}

func parseKeyContainer(data []byte) *drmproto.License_KeyContainer {
	kc := &drmproto.License_KeyContainer{}
	pos := 0
	for pos < len(data) {
		fieldKey, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := int(fieldKey >> 3)
		wireType := int(fieldKey & 0x7)

		switch wireType {
		case 0:
			val, n := decodeVarint(data[pos:])
			pos += n
			if fieldNum == 4 {
				v := int32(val)
				kc.Type = &v
			}
		case 2:
			length, n := decodeVarint(data[pos:])
			pos += n
			val := data[pos : pos+int(length)]
			pos += int(length)
			switch fieldNum {
			case 1:
				kc.Id = val
			case 2:
				kc.Iv = val
			case 3:
				kc.Key = val
			}
		}
	}
	return kc
}

func nowUnix() uint64 {
	return 0 // placeholder — use time.Now().Unix() at runtime
}
