package entity

// VInfo represents parsed video information.
type VInfo struct {
	// Video title
	Title string `json:"title"`
	// Video description
	Desc string `json:"desc"`
	// Cover image URL
	Pic string `json:"pic"`
	// Publish timestamp
	PubTime int64 `json:"pub_time"`
	// Is a bangumi/anime
	IsBangumi bool `json:"is_bangumi"`
	// Is a cheese/course
	IsCheese bool `json:"is_cheese"`
	// Whether the bangumi has ended
	IsBangumiEnd bool `json:"is_bangumi_end"`
	// Bangumi/course episode index
	Index string `json:"index,omitempty"`
	// Page information
	PagesInfo []Page `json:"pages_info"`
	// Interactive video (SteinGate)
	IsSteinGate bool `json:"is_stein_gate"`
	// UP main charging exclusive video
	IsUpowerExclusive bool `json:"is_upower_exclusive"`
	// Whether current identity has preview access
	IsUpowerPreview bool `json:"is_upower_preview"`
	// Whether current identity has full playback permission
	IsUpowerPlay bool `json:"is_upower_play"`
}
