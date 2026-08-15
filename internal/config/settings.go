package config

// QualityMap maps Bilibili quality IDs to display names.
var QualityMap = map[string]string{
	"127": "8K 超高清",
	"126": "杜比视界",
	"125": "HDR 真彩",
	"120": "4K 超清",
	"116": "1080P 高帧率",
	"112": "1080P 高码率",
	"100": "智能修复",
	"80":  "1080P 高清",
	"74":  "720P 高帧率",
	"64":  "720P 高清",
	"48":  "720P 高清",
	"32":  "480P 清晰",
	"16":  "360P 流畅",
	"5":   "144P 流畅",
	"6":   "240P 流畅",
}

// AppSettings holds global application configuration.
type AppSettings struct {
	Cookie              string `json:"cookie" mapstructure:"cookie"`
	Token               string `json:"token" mapstructure:"token"`
	DebugLog            bool   `json:"debug_log" mapstructure:"debug_log"`
	Host                string `json:"host" mapstructure:"host"`
	EpHost              string `json:"ep_host" mapstructure:"ep_host"`
	TvHost              string `json:"tv_host" mapstructure:"tv_host"`
	Area                string `json:"area" mapstructure:"area"`
	Wbi                 string `json:"wbi" mapstructure:"wbi"`
	SkipSslCheck        bool   `json:"skip_ssl_check" mapstructure:"skip_ssl_check"`
	MuxerTimeoutMinutes int    `json:"muxer_timeout_minutes" mapstructure:"muxer_timeout_minutes"`
	MaxRetryCount       int    `json:"max_retry_count" mapstructure:"max_retry_count"`
	RetryDelayMs        int    `json:"retry_delay_ms" mapstructure:"retry_delay_ms"`
	ThreadSegmentSizeMb int    `json:"thread_segment_size_mb" mapstructure:"thread_segment_size_mb"`
}

// DefaultAppSettings returns sensible defaults.
func DefaultAppSettings() AppSettings {
	return AppSettings{
		Host:                "api.bilibili.com",
		EpHost:              "api.bilibili.com",
		TvHost:              "api.snm0516.aisee.tv",
		MuxerTimeoutMinutes: 30,
		MaxRetryCount:       3,
		RetryDelayMs:        3000,
		ThreadSegmentSizeMb: 20,
	}
}

// MyOption holds CLI-level options for download tasks.
type MyOption struct {
	URL                    string `json:"url"`
	UseTvAPI               bool   `json:"use_tv_api" mapstructure:"use_tv_api"`
	UseAppAPI              bool   `json:"use_app_api" mapstructure:"use_app_api"`
	UseIntlAPI             bool   `json:"use_intl_api" mapstructure:"use_intl_api"`
	UseMP4box              bool   `json:"use_mp4box" mapstructure:"use_mp4box"`
	EncodingPriority       string `json:"encoding_priority" mapstructure:"encoding_priority"`
	DfnPriority            string `json:"dfn_priority" mapstructure:"dfn_priority"`
	OnlyShowInfo           bool   `json:"only_show_info" mapstructure:"only_show_info"`
	ShowAll                bool   `json:"show_all" mapstructure:"show_all"`
	UseAria2c              bool   `json:"use_aria2c" mapstructure:"use_aria2c"`
	Interactive            bool   `json:"interactive" mapstructure:"interactive"`
	HideStreams            bool   `json:"hide_streams" mapstructure:"hide_streams"`
	MultiThread            bool   `json:"multi_thread" mapstructure:"multi_thread"`
	SimplyMux              bool   `json:"simply_mux" mapstructure:"simply_mux"`
	VideoOnly              bool   `json:"video_only" mapstructure:"video_only"`
	AudioOnly              bool   `json:"audio_only" mapstructure:"audio_only"`
	DanmakuOnly            bool   `json:"danmaku_only" mapstructure:"danmaku_only"`
	CoverOnly              bool   `json:"cover_only" mapstructure:"cover_only"`
	SubOnly                bool   `json:"sub_only" mapstructure:"sub_only"`
	Debug                  bool   `json:"debug" mapstructure:"debug"`
	SkipMux                bool   `json:"skip_mux" mapstructure:"skip_mux"`
	Insecure               bool   `json:"insecure" mapstructure:"insecure"`
	DecryptDrm             bool   `json:"decrypt_drm" mapstructure:"decrypt_drm"`
	AllowPreview           bool   `json:"allow_preview" mapstructure:"allow_preview"`
	DrmKeyHex              string `json:"drm_key_hex" mapstructure:"drm_key_hex"`
	DrmKidHex              string `json:"drm_kid_hex" mapstructure:"drm_kid_hex"`
	Mp4decryptPath         string `json:"mp4decrypt_path" mapstructure:"mp4decrypt_path"`
	WvdPath                string `json:"wvd_path" mapstructure:"wvd_path"`
	SkipSubtitle           bool   `json:"skip_subtitle" mapstructure:"skip_subtitle"`
	SkipCover              bool   `json:"skip_cover" mapstructure:"skip_cover"`
	ForceHTTP              bool   `json:"force_http" mapstructure:"force_http"`
	DownloadDanmaku        bool   `json:"download_danmaku" mapstructure:"download_danmaku"`
	DownloadDanmakuFormats string `json:"download_danmaku_formats" mapstructure:"download_danmaku_formats"`
	DanmakuFilter          string `json:"danmaku_filter" mapstructure:"danmaku_filter"`
	DanmakuFilterUser      string `json:"danmaku_filter_user" mapstructure:"danmaku_filter_user"`
	DownloadComments       bool   `json:"download_comments" mapstructure:"download_comments"`
	NotifyWebhook          string `json:"notify_webhook" mapstructure:"notify_webhook"`
	SkipAi                 bool   `json:"skip_ai" mapstructure:"skip_ai"`
	VideoAscending         bool   `json:"video_ascending" mapstructure:"video_ascending"`
	AudioAscending         bool   `json:"audio_ascending" mapstructure:"audio_ascending"`
	AllowPcdn              bool   `json:"allow_pcdn" mapstructure:"allow_pcdn"`
	FilePattern            string `json:"file_pattern" mapstructure:"file_pattern"`
	MultiFilePattern       string `json:"multi_file_pattern" mapstructure:"multi_file_pattern"`
	SelectPage             string `json:"select_page" mapstructure:"select_page"`
	Language               string `json:"language" mapstructure:"language"`
	UserAgent              string `json:"user_agent" mapstructure:"user_agent"`
	Cookie                 string `json:"cookie" mapstructure:"cookie"`
	AccessToken            string `json:"access_token" mapstructure:"access_token"`
	Aria2cArgs             string `json:"aria2c_args" mapstructure:"aria2c_args"`
	WorkDir                string `json:"work_dir" mapstructure:"work_dir"`
	FFmpegPath             string `json:"ffmpeg_path" mapstructure:"ffmpeg_path"`
	Mp4boxPath             string `json:"mp4box_path" mapstructure:"mp4box_path"`
	Aria2cPath             string `json:"aria2c_path" mapstructure:"aria2c_path"`
	UposHost               string `json:"upos_host" mapstructure:"upos_host"`
	ForceReplaceHost       bool   `json:"force_replace_host" mapstructure:"force_replace_host"`
	SaveArchivesToFile     bool   `json:"save_archives_to_file" mapstructure:"save_archives_to_file"`
	DelayPerPage           int    `json:"delay_per_page" mapstructure:"delay_per_page"`
	Aria2cProxy            string `json:"aria2c_proxy" mapstructure:"aria2c_proxy"`
	AddDfnSuffix           bool   `json:"add_dfn_suffix" mapstructure:"add_dfn_suffix"`
	OnlyHevc               bool   `json:"only_hevc" mapstructure:"only_hevc"`
	OnlyAvc                bool   `json:"only_avc" mapstructure:"only_avc"`
	OnlyAv1                bool   `json:"only_av1" mapstructure:"only_av1"`
	NoPaddingPageNum       bool   `json:"no_padding_page_num" mapstructure:"no_padding_page_num"`
	BandwidthAscending     bool   `json:"bandwidth_ascending" mapstructure:"bandwidth_ascending"`
	MuxerTimeout           int    `json:"muxer_timeout" mapstructure:"muxer_timeout"`
	RetryCount             int    `json:"retry_count" mapstructure:"retry_count"`
	RetryDelay             int    `json:"retry_delay" mapstructure:"retry_delay"`
	ThreadSegmentSize      int    `json:"thread_segment_size" mapstructure:"thread_segment_size"`
	Host                   string `json:"host" mapstructure:"host"`
	EpHost                 string `json:"ep_host" mapstructure:"ep_host"`
	TvHost                 string `json:"tv_host" mapstructure:"tv_host"`
	Area                   string `json:"area" mapstructure:"area"`
	ConfigFile             string `json:"config_file" mapstructure:"config_file"`
	Wbi                    string `json:"wbi" mapstructure:"wbi"`
	ServeListenURL         string `json:"serve_listen_url" mapstructure:"serve_listen_url"`
	ServeMaxConcurrent     int    `json:"serve_max_concurrent" mapstructure:"serve_max_concurrent"`
	ServeToken             string `json:"serve_token" mapstructure:"serve_token"`
}

// DefaultMyOption returns sensible defaults for MyOption.
func DefaultMyOption() MyOption {
	return MyOption{
		MultiThread:       true,
		ForceHTTP:         false,
		SkipAi:            true,
		ForceReplaceHost:  true,
		Host:              "api.bilibili.com",
		EpHost:            "api.bilibili.com",
		TvHost:            "api.snm0516.aisee.tv",
		MuxerTimeout:      30,
		RetryCount:        3,
		RetryDelay:        3000,
		ThreadSegmentSize: 20,
	}
}
