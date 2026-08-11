package cli

import (
	"fmt"
	"os"

	"github.com/QC3284/BBDown/internal/workflow"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	debug   bool

	// Global option flags (mapped to MyOption fields)
	optCookie     string
	optToken      string
	optHost       string
	optEpHost     string
	optTvHost     string
	optArea       string
	optUserAgent  string
	optWorkDir    string
	optFFmpegPath string
	optMp4boxPath string
	optAria2cPath string
	optConfigFile string
	optInsecure   bool

	// Download options
	optURL              string
	optUseTvAPI         bool
	optUseAppAPI        bool
	optUseIntlAPI       bool
	optUseMP4box        bool
	optEncodingPriority string
	optDfnPriority      string
	optOnlyShowInfo     bool
	optShowAll          bool
	optUseAria2c        bool
	optInteractive      bool
	optHideStreams      bool
	optMultiThread      bool
	optSimplyMux        bool
	optVideoOnly        bool
	optAudioOnly        bool
	optDanmakuOnly      bool
	optCoverOnly        bool
	optSubOnly          bool
	optSkipMux          bool
	optDecryptDrm       bool
	optAllowPreview     bool
	optDrmKeyHex        string
	optDrmKidHex        string
	optMp4decryptPath   string
	optWvdPath          string
	optSkipSubtitle     bool
	optSkipCover        bool
	optForceHTTP        bool
	optDownloadDanmaku  bool
	optDanmakuFormats   string
	optDanmakuFilter    string
	optDanmakuFilterUser string
	optDownloadComments bool
	optNotifyWebhook    string
	optSkipAi           bool
	optVideoAscending   bool
	optAudioAscending   bool
	optAllowPcdn        bool
	optFilePattern      string
	optMultiFilePattern string
	optSelectPage       string
	optLanguage         string
	optAria2cArgs       string
	optUposHost         string
	optForceReplaceHost bool
	optSaveArchives     bool
	optDelayPerPage     int
	optMuxerTimeout     int
	optRetryCount       int
	optRetryDelay       int
	optThreadSegmentSize int

	// Serve options
	optServeListen    string
	optServeMaxConcurrent int
	optServeToken     string
)

// rootCmd represents the base command.
var rootCmd = &cobra.Command{
	Use:   "BBDown [URL]",
	Short: "BBDown - Bilibili Downloader (go)",
	Long: `BBDown is a command-line Bilibili video downloader.
Supports regular videos, bangumi, courses, collections, playlists, and more.

Examples:
  BBDown https://www.bilibili.com/video/BV1xx411c7mD
  BBDown --use-tv-api --interactive BV1xx411c7mD
  BBDown login`,
	Version: "1.0.0",
	Args:    cobra.ArbitraryArgs,
	RunE:    runDownload,
}

// Execute adds all child commands and runs root.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVarP(&optCookie, "cookie", "c", "", "设置cookie用以下载会员内容")
	rootCmd.PersistentFlags().StringVar(&optToken, "access-token", "", "设置access_token用于TV/APP接口")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "输出调试日志")
	rootCmd.PersistentFlags().StringVar(&optHost, "host", "api.bilibili.com", "指定API host")
	rootCmd.PersistentFlags().StringVar(&optEpHost, "ep-host", "api.bilibili.com", "指定EP host")
	rootCmd.PersistentFlags().StringVar(&optTvHost, "tv-host", "api.snm0516.aisee.tv", "自定义TV端host")
	rootCmd.PersistentFlags().StringVar(&optArea, "area", "", "指定BiliPlus area")
	rootCmd.PersistentFlags().BoolVar(&optInsecure, "insecure", false, "跳过SSL证书验证")
	rootCmd.PersistentFlags().StringVarP(&optUserAgent, "user-agent", "u", "", "指定user-agent")
	rootCmd.PersistentFlags().StringVar(&optWorkDir, "work-dir", "", "设置工作目录")
	rootCmd.PersistentFlags().StringVar(&optFFmpegPath, "ffmpeg-path", "", "设置ffmpeg路径")
	rootCmd.PersistentFlags().StringVar(&optMp4boxPath, "mp4box-path", "", "设置mp4box路径")
	rootCmd.PersistentFlags().StringVar(&optAria2cPath, "aria2c-path", "", "设置aria2c路径")
	rootCmd.PersistentFlags().StringVar(&optConfigFile, "config-file", "", "指定配置文件")

	// Download flags
	rootCmd.Flags().BoolVarP(&optUseTvAPI, "use-tv-api", "t", false, "使用TV端解析模式")
	rootCmd.Flags().BoolVarP(&optUseAppAPI, "use-app-api", "a", false, "使用APP端解析模式")
	rootCmd.Flags().BoolVar(&optUseIntlAPI, "use-intl-api", false, "使用国际版解析模式")
	rootCmd.Flags().BoolVar(&optUseMP4box, "use-mp4box", false, "使用MP4Box混流")
	rootCmd.Flags().StringVarP(&optEncodingPriority, "encoding-priority", "e", "", "编码优先级, 逗号分隔")
	rootCmd.Flags().StringVarP(&optDfnPriority, "dfn-priority", "q", "", "画质优先级, 逗号分隔")
	rootCmd.Flags().BoolVarP(&optOnlyShowInfo, "only-show-info", "I", false, "仅解析不下载")
	rootCmd.Flags().BoolVar(&optShowAll, "show-all", false, "展示所有分P标题")
	rootCmd.Flags().BoolVar(&optUseAria2c, "use-aria2c", false, "调用aria2c下载")
	rootCmd.Flags().BoolVarP(&optInteractive, "interactive", "i", false, "交互式选择清晰度")
	rootCmd.Flags().BoolVar(&optHideStreams, "hide-streams", false, "不要显示所有可用流")
	rootCmd.Flags().BoolVar(&optMultiThread, "multi-thread", true, "使用多线程下载")
	rootCmd.Flags().BoolVar(&optSimplyMux, "simply-mux", false, "精简混流")
	rootCmd.Flags().BoolVar(&optVideoOnly, "video-only", false, "仅下载视频")
	rootCmd.Flags().BoolVar(&optAudioOnly, "audio-only", false, "仅下载音频")
	rootCmd.Flags().BoolVar(&optDanmakuOnly, "danmaku-only", false, "仅下载弹幕")
	rootCmd.Flags().BoolVar(&optCoverOnly, "cover-only", false, "仅下载封面")
	rootCmd.Flags().BoolVar(&optSubOnly, "sub-only", false, "仅下载字幕")
	rootCmd.Flags().BoolVar(&optSkipMux, "skip-mux", false, "跳过混流步骤")
	rootCmd.Flags().BoolVar(&optDecryptDrm, "decrypt-drm", false, "尝试解密DRM视频")
	rootCmd.Flags().BoolVar(&optAllowPreview, "allow-preview", false, "允许下载充电试看片段")
	rootCmd.Flags().StringVar(&optDrmKeyHex, "key", "", "DRM解密密钥(hex)")
	rootCmd.Flags().StringVar(&optDrmKidHex, "kid", "", "DRM密钥ID(hex)")
	rootCmd.Flags().StringVar(&optMp4decryptPath, "mp4decrypt-path", "", "mp4decrypt路径")
	rootCmd.Flags().StringVar(&optWvdPath, "wvd-path", "", "device.wvd路径")
	rootCmd.Flags().BoolVar(&optSkipSubtitle, "skip-subtitle", false, "跳过字幕下载")
	rootCmd.Flags().BoolVar(&optSkipCover, "skip-cover", false, "跳过封面下载")
	rootCmd.Flags().BoolVar(&optForceHTTP, "force-http", true, "强制HTTP协议")
	rootCmd.Flags().BoolVarP(&optDownloadDanmaku, "download-danmaku", "d", false, "下载弹幕")
	rootCmd.Flags().StringVar(&optDanmakuFormats, "download-danmaku-formats", "", "弹幕格式, 逗号分隔")
	rootCmd.Flags().StringVar(&optDanmakuFilter, "danmaku-filter", "", "弹幕关键词过滤")
	rootCmd.Flags().StringVar(&optDanmakuFilterUser, "danmaku-filter-user", "", "弹幕用户过滤")
	rootCmd.Flags().BoolVar(&optDownloadComments, "comments", false, "下载评论区")
	rootCmd.Flags().StringVar(&optNotifyWebhook, "notify-webhook", "", "下载完成通知URL")
	rootCmd.Flags().BoolVar(&optSkipAi, "skip-ai", true, "跳过AI字幕")
	rootCmd.Flags().BoolVar(&optVideoAscending, "video-ascending", false, "视频升序")
	rootCmd.Flags().BoolVar(&optAudioAscending, "audio-ascending", false, "音频升序")
	rootCmd.Flags().BoolVar(&optAllowPcdn, "allow-pcdn", false, "不替换PCDN域名")
	rootCmd.Flags().StringVarP(&optFilePattern, "file-pattern", "F", "", "单P文件名模板")
	rootCmd.Flags().StringVarP(&optMultiFilePattern, "multi-file-pattern", "M", "", "多P文件名模板")
	rootCmd.Flags().StringVarP(&optSelectPage, "select-page", "p", "", "选择分P")
	rootCmd.Flags().StringVar(&optLanguage, "language", "", "音频语言代码")
	rootCmd.Flags().StringVar(&optAria2cArgs, "aria2c-args", "", "aria2c附加参数")
	rootCmd.Flags().StringVar(&optUposHost, "upos-host", "", "自定义upos服务器")
	rootCmd.Flags().BoolVar(&optForceReplaceHost, "force-replace-host", true, "强制替换下载host")
	rootCmd.Flags().BoolVar(&optSaveArchives, "save-archives-to-file", false, "记录已下载视频")
	rootCmd.Flags().IntVar(&optDelayPerPage, "delay-per-page", 0, "分P下载间隔(秒)")
	rootCmd.Flags().IntVar(&optMuxerTimeout, "muxer-timeout", 30, "混流超时(分钟)")
	rootCmd.Flags().IntVar(&optRetryCount, "retry-count", 3, "重试次数")
	rootCmd.Flags().IntVar(&optRetryDelay, "retry-delay", 3000, "重试间隔(毫秒)")
	rootCmd.Flags().IntVar(&optThreadSegmentSize, "thread-segment-size", 20, "分片大小(MB)")

	// Serve flags
	serveCmd.Flags().StringVar(&optServeListen, "listen", "http://127.0.0.1:23333", "API服务器监听地址")
	serveCmd.Flags().IntVar(&optServeMaxConcurrent, "max-concurrent", 3, "最大并发下载数")
	serveCmd.Flags().StringVar(&optServeToken, "serve-token", "", "API认证Token")
	serveCmd.Flags().StringVar(&optNotifyWebhook, "notify-webhook", "", "任务完成通知URL")

	// Register subcommands
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(loginTVCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(liveCmd)
	rootCmd.AddCommand(articleCmd)
}

func runDownload(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		optURL = args[0]
	}

	if optURL == "" {
		return fmt.Errorf("请提供视频地址")
	}

	// Build MyOption from flags
	cfg := buildMyOption()

	// Run the workflow
	client := buildHTTPClient(cfg)
	wf := workflow.New(cfg, client)
	return wf.Run(cmd.Context())
}
