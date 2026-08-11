package entity

// ParsedResult holds parsed track information from playurl API.
type ParsedResult struct {
	// Raw JSON response string
	WebJSONString string `json:"web_json_string"`

	// Video tracks
	VideoTracks []Video `json:"video_tracks"`
	// Audio tracks
	AudioTracks []Audio `json:"audio_tracks"`
	// Background audio tracks (for dubbing)
	BackgroundAudioTracks []Audio `json:"background_audio_tracks,omitempty"`
	// Role audio list (multi-language dubbing)
	RoleAudioList []AudioMaterialInfo `json:"role_audio_list,omitempty"`

	// Chapter/section markers
	ExtraPoints []ViewPoint `json:"extra_points,omitempty"`

	// For FLV streams
	Clips []string `json:"clips,omitempty"`     // Segment URLs
	Dfns  []string `json:"dfns,omitempty"`      // Available quality IDs

	// Actual media duration in seconds (may differ from claimed duration for preview content)
	ActualDurationSec int `json:"actual_duration_sec"`

	// DRM fields
	IsDrm       bool   `json:"is_drm"`
	DrmTechType int    `json:"drm_tech_type"`
	DrmType     string `json:"drm_type"`
	KidHex      string `json:"kid_hex"`
	KeyHex      string `json:"key_hex"`
	PsshBase64  string `json:"pssh_base64"`
}
