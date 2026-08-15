package appapi

import (
	"encoding/json"
)

// JSON output shapes (upstream AppHelper DashJson classes).
type dashVideoOut struct {
	ID        uint32   `json:"id"`
	BaseURL   string   `json:"base_url"`
	BackupURL []string `json:"backup_url"`
	Bandwidth uint64   `json:"bandwidth"`
	Codecid   uint32   `json:"codecid"`
}

type dashAudioOut struct {
	ID        uint32   `json:"id"`
	BaseURL   string   `json:"base_url"`
	BackupURL []string `json:"backup_url"`
	Bandwidth uint32   `json:"bandwidth"`
	Codecs    string   `json:"codecs"`
}

type dashClipOut struct {
	Start     int    `json:"start"`
	End       int    `json:"end"`
	ToastText string `json:"toastText"`
}

type audioMaterialOut struct {
	AudioID    string         `json:"audio_id"`
	Title      string         `json:"title"`
	PersonName string         `json:"person_name"`
	Audio      []dashAudioOut `json:"audio"`
}

type dubbingInfoOut struct {
	BackgroundAudio []dashAudioOut     `json:"background_audio"`
	RoleAudioList   []audioMaterialOut `json:"role_audio_list"`
}

type dashInfoOut struct {
	Video []dashVideoOut `json:"video"`
	Audio []dashAudioOut `json:"audio"`
}

type dashDataOut struct {
	Timelength   uint64        `json:"timelength"`
	Dash         dashInfoOut   `json:"dash"`
	ClipInfoList []dashClipOut `json:"clip_info_list"`
}

type dashJSONOut struct {
	Code        int            `json:"code"`
	Message     string         `json:"message"`
	TTL         int            `json:"ttl"`
	Data        dashDataOut    `json:"data"`
	DubbingInfo dubbingInfoOut `json:"dubbing_info"`
}

// convertToDashJSON parses the PlayViewReply and serializes the web-style dash
// JSON (upstream ConvertToDashJson).
func convertToDashJSON(msg []byte) (string, error) {
	out := dashJSONOut{Code: 0, Message: "0", TTL: 1}
	out.Data.Dash.Video = []dashVideoOut{}
	out.Data.Dash.Audio = []dashAudioOut{}
	out.Data.ClipInfoList = []dashClipOut{}
	out.DubbingInfo.BackgroundAudio = []dashAudioOut{}
	out.DubbingInfo.RoleAudioList = []audioMaterialOut{}

	var timelength uint64
	var videoSizes []uint64

	walkFields(msg, func(fieldNum, wireType int, val []byte, varint uint64) bool {
		switch fieldNum {
		case 1: // videoInfo
			parseVideoInfo(val, &out, &timelength, &videoSizes)
		case 3: // business
			parseBusiness(val, &out)
		case 7: // playExtInfo
			parsePlayExtInfo(val, &out)
		}
		return true
	})

	out.Data.Timelength = timelength
	// Video bandwidth = size*8 / max(timelength/1000, 1) (upstream).
	durSec := timelength / 1000
	if durSec == 0 {
		durSec = 1
	}
	for i := range out.Data.Dash.Video {
		if i < len(videoSizes) {
			out.Data.Dash.Video[i].Bandwidth = videoSizes[i] * 8 / durSec
		}
	}

	enc, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(enc), nil
}

func parseVideoInfo(vi []byte, out *dashJSONOut, timelength *uint64, videoSizes *[]uint64) {
	walkFields(vi, func(fieldNum, wireType int, val []byte, varint uint64) bool {
		switch fieldNum {
		case 3: // timelength
			*timelength = varint
		case 5: // streamList
			parseStreamItem(val, out, videoSizes)
		case 6: // dashAudio
			out.Data.Dash.Audio = append(out.Data.Dash.Audio, parseDashItem(val, "M4A", nil))
		case 7: // dolby
			parseDolbyItem(val, out, "E-AC-3", nil)
		case 9: // flac
			parseDolbyItem(val, out, "FLAC", nil)
		}
		return true
	})
}

func parseStreamItem(si []byte, out *dashJSONOut, videoSizes *[]uint64) {
	var quality uint32
	var baseURL string
	var backup []string
	var codecid uint32
	var size uint64
	walkFields(si, func(fieldNum, wireType int, val []byte, varint uint64) bool {
		switch fieldNum {
		case 1: // streamInfo
			walkFields(val, func(fn, wt int, v []byte, vv uint64) bool {
				if fn == 1 {
					quality = uint32(vv)
				}
				return true
			})
		case 2: // dashVideo
			walkFields(val, func(fn, wt int, v []byte, vv uint64) bool {
				switch fn {
				case 1:
					baseURL = string(v)
				case 2:
					backup = append(backup, string(v))
				case 4:
					codecid = uint32(vv)
				case 6:
					size = vv
				}
				return true
			})
		}
		return true
	})
	if baseURL != "" {
		out.Data.Dash.Video = append(out.Data.Dash.Video, dashVideoOut{
			ID:        quality,
			BaseURL:   baseURL,
			BackupURL: backup,
			Codecid:   codecid,
		})
		*videoSizes = append(*videoSizes, size)
	}
}

func parseDolbyItem(d []byte, out *dashJSONOut, codecs string, extra *[]dashAudioOut) {
	walkFields(d, func(fieldNum, wireType int, val []byte, varint uint64) bool {
		if fieldNum == 2 { // audio
			a := parseDashItem(val, codecs, extra)
			if extra == nil {
				out.Data.Dash.Audio = append(out.Data.Dash.Audio, a)
			}
		}
		return true
	})
}

func parseDashItem(d []byte, codecs string, extra *[]dashAudioOut) dashAudioOut {
	a := dashAudioOut{BackupURL: []string{}, Codecs: codecs}
	walkFields(d, func(fieldNum, wireType int, val []byte, varint uint64) bool {
		switch fieldNum {
		case 1:
			a.ID = uint32(varint)
		case 2:
			a.BaseURL = string(val)
		case 3:
			a.BackupURL = append(a.BackupURL, string(val))
		case 4:
			a.Bandwidth = uint32(varint)
		}
		return true
	})
	if extra != nil {
		*extra = append(*extra, a)
	}
	return a
}

func parseBusiness(b []byte, out *dashJSONOut) {
	walkFields(b, func(fieldNum, wireType int, val []byte, varint uint64) bool {
		if fieldNum == 6 { // clipInfo
			c := dashClipOut{}
			walkFields(val, func(fn, wt int, v []byte, vv uint64) bool {
				switch fn {
				case 2:
					c.Start = int(vv)
				case 3:
					c.End = int(vv)
				case 5:
					c.ToastText = string(v)
				}
				return true
			})
			out.Data.ClipInfoList = append(out.Data.ClipInfoList, c)
		}
		return true
	})
}

func parsePlayExtInfo(p []byte, out *dashJSONOut) {
	walkFields(p, func(fieldNum, wireType int, val []byte, varint uint64) bool {
		if fieldNum == 1 { // playDubbingInfo
			parsePlayDubbingInfo(val, out)
		}
		return true
	})
}

func parsePlayDubbingInfo(d []byte, out *dashJSONOut) {
	walkFields(d, func(fieldNum, wireType int, val []byte, varint uint64) bool {
		switch fieldNum {
		case 1: // backgroundAudio (AudioMaterialProto)
			parseAudioMaterialProto(val, &out.DubbingInfo.BackgroundAudio)
		case 2: // roleAudioList
			walkFields(val, func(fn, wt int, v []byte, vv uint64) bool {
				if fn == 4 { // audioMaterialList
					var audios []dashAudioOut
					m := parseAudioMaterialProtoFull(v, &audios)
					out.DubbingInfo.RoleAudioList = append(out.DubbingInfo.RoleAudioList, m.toOut(audios))
				}
				return true
			})
		}
		return true
	})
}

type audioMaterialFields struct {
	audioID, title, edition, personName string
}

func (m audioMaterialFields) toOut(audios []dashAudioOut) audioMaterialOut {
	title := m.title
	if title == "" {
		title = m.audioID
	}
	person := m.personName
	if person == "" {
		person = m.edition
	}
	return audioMaterialOut{AudioID: m.audioID, Title: title, PersonName: person, Audio: audios}
}

func parseAudioMaterialProto(d []byte, audios *[]dashAudioOut) {
	parseAudioMaterialProtoFull(d, audios)
}

func parseAudioMaterialProtoFull(d []byte, audios *[]dashAudioOut) audioMaterialFields {
	var m audioMaterialFields
	walkFields(d, func(fieldNum, wireType int, val []byte, varint uint64) bool {
		switch fieldNum {
		case 1:
			m.audioID = string(val)
		case 2:
			m.title = string(val)
		case 3:
			m.edition = string(val)
		case 5:
			m.personName = string(val)
		case 7: // audio (repeated DashItem)
			parseDashItem(val, "M4A", audios)
		}
		return true
	})
	return m
}
