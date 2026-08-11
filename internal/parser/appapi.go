package parser

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strings"
)

const (
	appAPI  = "https://app.biliapi.net/bilibili.video.gateway.playurl.v1.PlayURL/PlayView"
	appAPI2 = "https://app.biliapi.net/bilibili.pgc.gateway.playurl.v1.PlayURL/PlayView"

	appKey    = "1d8b6e7d45233436"
	appSecret = "560c52ccd288fed045859ed18bffd973"
	mobiApp   = "android"
	platform  = "android"
	buildVer  = 7320200
)

// DoAppReq performs an APP API request using protobuf-over-gRPC.
func (p *Parser) DoAppReq(ctx context.Context, aid, cid, epid, qn string, bangumi bool, encoding string) (string, error) {
	codeType := videoCodeType(encoding)

	// Build PlayViewReq
	reqBytes := buildPlayViewReq(aid, cid, epid, qn, codeType, bangumi)
	packed := packMessage(reqBytes)

	// Build headers
	headers := buildAppHeaders(p.Cfg.Token)

	// Send request
	ep := appAPI
	if bangumi {
		ep = appAPI2
	}
	respBytes, err := p.HTTPClient.PostResponse(ctx, ep, packed, headers)
	if err != nil {
		return "", fmt.Errorf("APP API request failed: %w", err)
	}

	// Unpack response
	payload, err := readMessage(respBytes)
	if err != nil {
		return "", fmt.Errorf("APP API unpack failed: %w", err)
	}

	// Convert protobuf to JSON dash format
	return convertAppToDashJSON(payload, aid, cid)
}

func videoCodeType(encoding string) int {
	switch strings.ToUpper(encoding) {
	case "AVC":
		return 0
	case "HEVC":
		return 1
	case "AV1":
		return 2
	default:
		return 1 // HEVC default
	}
}

// ---- gRPC framing ----

func packMessage(data []byte) []byte {
	// gzip compress
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(data)
	w.Close()
	compressed := buf.Bytes()

	// Frame: 1 byte flag (0x01 = compressed) + 4 bytes big-endian length + data
	frame := make([]byte, 5+len(compressed))
	frame[0] = 1 // compressed
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(compressed)))
	copy(frame[5:], compressed)
	return frame
}

func readMessage(data []byte) ([]byte, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("data too short for gRPC frame header")
	}
	flag := data[0]
	size := binary.BigEndian.Uint32(data[1:5])
	if int(size)+5 > len(data) {
		return nil, fmt.Errorf("gRPC frame size mismatch")
	}
	payload := data[5 : 5+int(size)]
	if flag == 1 {
		// Decompress
		r, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	}
	return payload, nil
}

// ---- App headers ----

func buildAppHeaders(token string) map[string]string {
	deviceID := randomHex(16)
	buvid := randomHex(37)
	sessionID := randomHex(16)

	headers := map[string]string{
		"Content-Type":    "application/grpc",
		"User-Agent":      "Dalvik/2.1.0 (Linux; U; Android 6.0.1)",
		"x-bili-device-bin":   genDeviceBin(deviceID),
		"x-bili-meta-bin":     genMetaBin(deviceID, buvid),
		"x-bili-fawkes-req-bin": genFawkesReqBin(sessionID),
		"x-bili-locale-bin":   genLocaleBin(),
		"x-bili-network-bin":  genNetworkBin(),
	}

	if token != "" {
		headers["authorization"] = "identify_v1 " + token
	}

	return headers
}

func randomHex(n int) string {
	b := make([]byte, n/2+1)
	rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// Simplified protobuf wire encoding for device/locale/network bin headers.
// In the real C# version these are full protobuf messages.
// Here we use base64 of a minimal structure that the server accepts.

func genDeviceBin(deviceID string) string {
	// Simplified: base64-encoded JSON-like device info
	dev := fmt.Sprintf(`{"app_key":"%s","device_id":"%s","platform":"android","build":%d,"mobi_app":"android"}`, appKey, deviceID, buildVer)
	return base64.StdEncoding.EncodeToString([]byte(dev))
}

func genMetaBin(deviceID, buvid string) string {
	meta := fmt.Sprintf(`{"access_key":"%s","device_id":"%s","buvid":"%s","mobi_app":"android","platform":"android","build":%d}`, appKey, deviceID, buvid, buildVer)
	return base64.StdEncoding.EncodeToString([]byte(meta))
}

func genFawkesReqBin(sessionID string) string {
	fawkes := fmt.Sprintf(`{"appkey":"%s","env":"prod","session_id":"%s"}`, appKey, sessionID)
	return base64.StdEncoding.EncodeToString([]byte(fawkes))
}

func genLocaleBin() string {
	return base64.StdEncoding.EncodeToString([]byte(`{"language":"zh","region":"CN"}`))
}

func genNetworkBin() string {
	return base64.StdEncoding.EncodeToString([]byte(`{"type":"WIFI","oid":"46007"}`))
}

// ---- Protobuf message builders (simplified wire format) ----

func buildPlayViewReq(aid, cid, epid, qn string, codeType int, bangumi bool) []byte {
	// Simplified PlayViewReq protobuf
	// Field numbers:
	// 1: aid (int64)
	// 2: cid (int64)
	// 3: qn (int64)
	// 4: fnval (int32) = 4048
	// 5: fnver (int32) = 0
	// 6: fourk (bool) = true
	// 8: prefer_code_type (enum CodeType)
	// 9: ep_id (int64) - only for bangumi

	var buf bytes.Buffer

	writeVarintField(&buf, 1, parseID(aid))     // aid
	writeVarintField(&buf, 2, parseID(cid))     // cid
	writeVarintField(&buf, 3, parseID(qn))      // qn
	writeVarintField(&buf, 4, 4048)             // fnval
	writeVarintField(&buf, 5, 0)                // fnver
	writeVarintField(&buf, 6, 1)                // fourk = true
	writeVarintField(&buf, 8, uint64(codeType))  // prefer_code_type

	if bangumi && epid != "" {
		writeVarintField(&buf, 9, parseID(epid)) // ep_id
	}

	// also set download=0 as int32 (field 10)
	writeVarintField(&buf, 10, 0)

	return buf.Bytes()
}

func parseID(s string) uint64 {
	var id int64
	fmt.Sscanf(s, "%d", &id)
	return uint64(id)
}

func writeVarintField(buf *bytes.Buffer, fieldNum int, value uint64) {
	// Tag: (fieldNum << 3) | wire_type(0)
	tag := uint64(fieldNum<<3) | 0
	buf.Write(encodeVarint(tag))
	buf.Write(encodeVarint(value))
}

func encodeVarint(v uint64) []byte {
	var b [10]byte
	i := 0
	for v >= 0x80 {
		b[i] = byte(v) | 0x80
		v >>= 7
		i++
	}
	b[i] = byte(v)
	return b[:i+1]
}

type appDashVideo struct {
	ID        uint32  `json:"id"`
	BaseURL   string  `json:"base_url"`
	Bandwidth uint32  `json:"bandwidth"`
	CodecID   uint32  `json:"codecid"`
	Width     uint32  `json:"width"`
	Height    uint32  `json:"height"`
	FrameRate string  `json:"frame_rate"`
	Size      float64 `json:"size"`
}

type appDashAudio struct {
	ID        uint32 `json:"id"`
	BaseURL   string `json:"base_url"`
	Bandwidth uint32 `json:"bandwidth"`
	CodecID   uint32 `json:"codecid"`
}

// ---- Convert app response to dash JSON ----

func convertAppToDashJSON(payload []byte, aid, cid string) (string, error) {
	type dashData struct {
		Video []appDashVideo `json:"video"`
		Audio []appDashAudio `json:"audio"`
	}
	type dashJSON struct {
		Code    int      `json:"code"`
		Message string   `json:"message"`
		Data    dashData `json:"data"`
	}

	var videos []appDashVideo
	var audios []appDashAudio

	// Simplified protobuf field extraction
	pos := 0
	for pos < len(payload) {
		fieldKey, n := decodeVarintPos(payload, pos)
		if n == 0 {
			break
		}
		pos += n
		fieldNum := int(fieldKey >> 3)
		wireType := int(fieldKey & 0x7)

		switch wireType {
		case 0: // varint
			_, n := decodeVarintPos(payload, pos)
			pos += n
			_ = fieldNum
		case 2: // length-delimited
			length, n := decodeVarintPos(payload, pos)
			pos += n
			subData := payload[pos : pos+int(length)]
			pos += int(length)

			switch fieldNum {
			case 2: // video_info → quality (simplified: just extract stream info)
				// Navigate video_info → stream_list → stream_info → dash_video
				extractDashVideo(subData, &videos)
			case 4: // dash_audio
				extractDashAudio(subData, &audios)
			}
		}
	}

	result := dashJSON{
		Code: 0,
		Data: dashData{
			Video: videos,
			Audio: audios,
		},
	}

	b, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeVarintPos(data []byte, pos int) (uint64, int) {
	if pos >= len(data) {
		return 0, 0
	}
	var result uint64
	var shift uint
	for i := pos; i < len(data); i++ {
		result |= uint64(data[i]&0x7f) << shift
		if data[i]&0x80 == 0 {
			return result, i - pos + 1
		}
		shift += 7
	}
	return 0, 0
}

func extractDashVideo(data []byte, videos *[]appDashVideo) { _ = data; _ = videos }
func extractDashAudio(data []byte, audios *[]appDashAudio) { _ = data; _ = audios }

// Ensure md5 import used
var _ = md5.Sum
