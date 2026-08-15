package appapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"

	"github.com/QC3284/BBDown/internal/util"
)

// Bilibili APP playurl gRPC endpoint constants (upstream AppHelper).
const (
	apiUGC    = "https://grpc.biliapi.net/bilibili.app.playurl.v1.PlayURL/PlayView"
	apiPGC    = "https://app.bilibili.com/bilibili.pgc.gateway.player.v2.PlayURL/PlayView"
	dalvikVer = "2.1.0"
	osVer     = "11"
	brand     = "M2012K11AC"
	model     = "Build/RKQ1.200826.002"
	appVer    = "7.32.0"
	build     = 7320200
	channel   = "xiaomi_cn_tv.danmaku.bili_zm20200902"
	cronet    = "1.36.1"
	mobiApp   = "android"
	appKey    = "android64"
	sessionID = "dedf8669"
	platform  = "android"
	env       = "prod"
	appID     = 1
	region    = "CN"
	language  = "zh"
)

// CodeType enum values (PlayViewReq.CodeType).
const (
	code264 = 1
	code265 = 2
	codeAV1 = 3
)

func getVideoCodeType(code string) int {
	switch code {
	case "AVC":
		return code264
	case "HEVC":
		return code265
	case "AV1":
		return codeAV1
	default:
		return code265
	}
}

// DoReq performs the APP playurl request and returns the web-style dash JSON
// (upstream AppHelper.DoReqAsync + ConvertToDashJson).
func DoReq(ctx context.Context, client *util.HTTPClient, aid, cid, epID, qn string, bangumi bool, encoding, token string) (string, error) {
	parseID := func(value, name string) (int64, error) {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s 必须是有效的数字 ID，当前值: %q", name, value)
		}
		return n, nil
	}

	var payload []byte
	var endpoint string
	if bangumi {
		if encoding != "" && encoding != "HEVC" {
			util.LogWarn("APP的番剧不支持 HEVC 以外的编码")
		}
		numEp, err := parseID(epID, "epID")
		if err != nil {
			return "", err
		}
		numCid, err := parseID(cid, "cid")
		if err != nil {
			return "", err
		}
		numQn, err := parseID(qn, "qn")
		if err != nil {
			return "", err
		}
		payload = encodePlayViewReq(numEp, numCid, numQn, code265)
		endpoint = apiPGC
	} else {
		numAid, err := parseID(aid, "aid")
		if err != nil {
			return "", err
		}
		numCid, err := parseID(cid, "cid")
		if err != nil {
			return "", err
		}
		numQn, err := parseID(qn, "qn")
		if err != nil {
			return "", err
		}
		payload = encodePlayViewReq(numAid, numCid, numQn, getVideoCodeType(encoding))
		endpoint = apiUGC
	}

	headers := getHeader(token)
	body := packMessage(payload)
	resp, err := client.PostResponse(ctx, endpoint, body, headers)
	if err != nil {
		return "", err
	}
	msg, err := readMessage(resp)
	if err != nil {
		return "", err
	}
	return convertToDashJSON(msg)
}

// encodePlayViewReq builds the PlayViewReq protobuf (fields upstream sets).
func encodePlayViewReq(id, cid, qn int64, codec int) []byte {
	var b []byte
	b = appendVarintField(b, 1, uint64(id))  // epId / aid
	b = appendVarintField(b, 2, uint64(cid)) // cid
	if qn == 0 {
		qn = 127
	}
	b = appendVarintField(b, 3, uint64(qn))                         // qn
	b = appendVarintField(b, 5, 4048)                               // fnval
	b = appendVarintField(b, 7, 2)                                  // forceHost = https
	b = appendVarintField(b, 8, 1)                                  // fourk
	b = appendBytesField(b, 9, []byte("main.ugc-video-detail.0.0")) // spmid
	b = appendBytesField(b, 10, []byte("main.my-history.0.0"))      // fromSpmid
	b = appendVarintField(b, 12, uint64(codec))                     // preferCodecType
	return b
}

// getHeader builds the gRPC binary headers (upstream GetHeader).
func getHeader(token string) map[string]string {
	ua := fmt.Sprintf("Dalvik/%s (Linux; U; Android %s; %s %s) %s os/android model/%s mobi_app/android build/%d channel/%s innerVer/%d osVer/%s network/2 grpc-java-cronet/%s", dalvikVer, osVer, brand, model, appVer, brand, build, channel, build, osVer, cronet)
	return map[string]string{
		"Host":                   "grpc.biliapi.net",
		"user-agent":             ua,
		"te":                     "trailers",
		"x-bili-fawkes-req-bin":  b64(fawkesReqBin()),
		"x-bili-metadata-bin":    b64(metadataBin(token)),
		"authorization":          "identify_v1 " + token,
		"x-bili-device-bin":      b64(deviceBin()),
		"x-bili-network-bin":     b64(networkBin()),
		"x-bili-restriction-bin": "",
		"x-bili-locale-bin":      b64(localeBin()),
		"x-bili-exps-bin":        "",
		"grpc-encoding":          "gzip",
		"grpc-accept-encoding":   "identity,gzip",
		"grpc-timeout":           "17996161u",
		"content-type":           "application/grpc",
	}
}

func fawkesReqBin() []byte {
	var b []byte
	b = appendBytesField(b, 1, []byte(appKey))
	b = appendBytesField(b, 2, []byte(env))
	b = appendBytesField(b, 3, []byte(sessionID))
	return b
}

func metadataBin(token string) []byte {
	var b []byte
	b = appendBytesField(b, 1, []byte(token))
	b = appendBytesField(b, 2, []byte(mobiApp))
	b = appendVarintField(b, 4, uint64(build))
	b = appendBytesField(b, 5, []byte(channel))
	b = appendBytesField(b, 6, []byte(""))
	b = appendBytesField(b, 7, []byte(platform))
	return b
}

func deviceBin() []byte {
	var b []byte
	b = appendVarintField(b, 1, uint64(appID))
	b = appendVarintField(b, 2, uint64(build))
	b = appendBytesField(b, 3, []byte(""))
	b = appendBytesField(b, 4, []byte(mobiApp))
	b = appendBytesField(b, 5, []byte(platform))
	b = appendBytesField(b, 7, []byte(channel))
	b = appendBytesField(b, 8, []byte(brand))
	b = appendBytesField(b, 9, []byte(model))
	b = appendBytesField(b, 10, []byte(osVer))
	return b
}

func networkBin() []byte {
	var b []byte
	b = appendVarintField(b, 1, 1) // WIFI
	b = appendBytesField(b, 3, []byte("46007"))
	return b
}

func localeBin() []byte {
	var ids []byte
	ids = appendBytesField(ids, 1, []byte(language))
	ids = appendBytesField(ids, 3, []byte(region))
	return appendBytesField(nil, 1, ids)
}

func b64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// packMessage frames + gzips the request body (upstream PackMessage).
func packMessage(input []byte) []byte {
	comp := gzipCompress(input)
	out := make([]byte, 5+len(comp))
	out[0] = 1
	binary.BigEndian.PutUint32(out[1:5], uint32(len(comp)))
	copy(out[5:], comp)
	return out
}

// readMessage deframes + ungzips the response (upstream ReadMessage).
func readMessage(data []byte) ([]byte, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("gRPC response too short: %d bytes", len(data))
	}
	first := data[0]
	size := int(binary.BigEndian.Uint32(data[1:5]))
	if first == 1 {
		return gzipDecompress(data[5:])
	}
	payloadLen := size
	if payloadLen > len(data)-5 {
		payloadLen = len(data) - 5
	}
	if payloadLen <= 0 {
		return nil, fmt.Errorf("invalid gRPC payload length: %d", payloadLen)
	}
	return data[5 : 5+payloadLen], nil
}

func gzipCompress(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

func gzipDecompress(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// ---- protobuf wire helpers ----

func appendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

func appendVarintField(buf []byte, fieldNum int, v uint64) []byte {
	buf = appendVarint(buf, uint64(fieldNum<<3))
	return appendVarint(buf, v)
}

func appendBytesField(buf []byte, fieldNum int, data []byte) []byte {
	buf = appendVarint(buf, uint64(fieldNum<<3|2))
	buf = appendVarint(buf, uint64(len(data)))
	return append(buf, data...)
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
		if shift >= 64 {
			break
		}
	}
	return 0, 0
}

// walkFields iterates protobuf fields, calling fn per field.
func walkFields(data []byte, fn func(fieldNum, wireType int, val []byte, varint uint64) bool) {
	for pos := 0; pos < len(data); {
		key, n := decodeVarint(data[pos:])
		if n == 0 {
			break
		}
		pos += n
		fieldNum := int(key >> 3)
		wireType := int(key & 0x7)
		switch wireType {
		case 0:
			v, n := decodeVarint(data[pos:])
			pos += n
			if !fn(fieldNum, wireType, nil, v) {
				return
			}
		case 1:
			if pos+8 > len(data) {
				return
			}
			if !fn(fieldNum, wireType, data[pos:pos+8], 0) {
				return
			}
			pos += 8
		case 2:
			length, n := decodeVarint(data[pos:])
			if n == 0 || pos+n+int(length) > len(data) {
				return
			}
			pos += n
			if !fn(fieldNum, wireType, data[pos:pos+int(length)], 0) {
				return
			}
			pos += int(length)
		case 5:
			if pos+4 > len(data) {
				return
			}
			if !fn(fieldNum, wireType, data[pos:pos+4], 0) {
				return
			}
			pos += 4
		default:
			return
		}
	}
}
