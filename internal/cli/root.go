package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/util"
	"github.com/QC3284/BBDown/internal/workflow"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	debug bool

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
	optURL                string
	optUseTvAPI           bool
	optUseAppAPI          bool
	optUseIntlAPI         bool
	optUseMP4box          bool
	optEncodingPriority   string
	optDfnPriority        string
	optOnlyShowInfo       bool
	optShowAll            bool
	optUseAria2c          bool
	optInteractive        bool
	optHideStreams        bool
	optMultiThread        bool
	optSimplyMux          bool
	optVideoOnly          bool
	optAudioOnly          bool
	optDanmakuOnly        bool
	optCoverOnly          bool
	optSubOnly            bool
	optSkipMux            bool
	optDecryptDrm         bool
	optAllowPreview       bool
	optDrmKeyHex          string
	optDrmKidHex          string
	optMp4decryptPath     string
	optWvdPath            string
	optSkipSubtitle       bool
	optSkipCover          bool
	optForceHTTP          bool
	optAria2cProxy        string
	optAddDfnSuffix       bool
	optOnlyHevc           bool
	optOnlyAvc            bool
	optOnlyAv1            bool
	optNoPaddingPageNum   bool
	optBandwidthAscending bool
	optDownloadDanmaku    bool
	optDanmakuFormats     string
	optDanmakuFilter      string
	optDanmakuFilterUser  string
	optDownloadComments   bool
	optNotifyWebhook      string
	optSkipAi             bool
	optVideoAscending     bool
	optAudioAscending     bool
	optAllowPcdn          bool
	optFilePattern        string
	optMultiFilePattern   string
	optSelectPage         string
	optLanguage           string
	optAria2cArgs         string
	optUposHost           string
	optForceReplaceHost   bool
	optSaveArchives       bool
	optDelayPerPage       int
	optMuxerTimeout       int
	optRetryCount         int
	optRetryDelay         int
	optThreadSegmentSize  int

	// Serve options
	optServeListen        string
	optServeMaxConcurrent int
	optServeToken         string

	// Subcommand options
	optLiveOutput      string
	optArticleOutput   string
	optSubName         string
	optWatchLaterLimit int
)

// rootCmd represents the base command.
var rootCmd = &cobra.Command{
	Use:   "BBDown [URL]",
	Short: "BBDown - Bilibili Downloader",
	Long: `BBDown is a command-line Bilibili video downloader.
Supports regular videos, bangumi, courses, collections, playlists, and more.

Examples:
  BBDown https://www.bilibili.com/video/BV1xx411c7mD
  BBDown --use-tv-api --interactive BV1xx411c7mD
  BBDown login`,
	Version: "1.6.11-go",
	Args:    cobra.ArbitraryArgs,
	RunE:    runDownload,
}

// Execute adds all child commands and runs root.
func Execute() {
	util.SetDefaultDebugFn(func() bool { return debug })

	// Normalize legacy single-dash aliases (upstream NormalizeCliArgs), then
	// merge BBDown.config (line-based args) as option defaults.
	args := normalizeCliArgs(os.Args[1:])
	if merged, err := mergeConfigArgs(args); err == nil {
		rootCmd.SetArgs(merged)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// normalizeCliArgs maps "-help"/"-?" to "--help" and "-version" to "--version"
// (upstream NormalizeCliArgs).
func normalizeCliArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		switch a {
		case "-help", "-?":
			out[i] = "--help"
		case "-version":
			out[i] = "--version"
		}
	}
	return out
}

// mergeConfigArgs builds the option alias map from the registered cobra flags
// and merges the config file (config defaults < CLI args).
func mergeConfigArgs(cliArgs []string) ([]string, error) {
	aliasMap := make(map[string]string)
	boolFlags := make(map[string]bool)
	add := func(f *pflag.Flag) {
		aliasMap["--"+f.Name] = f.Name
		if f.Shorthand != "" {
			aliasMap["-"+f.Shorthand] = f.Name
		}
		boolFlags[f.Name] = f.Value.Type() == "bool"
	}
	rootCmd.PersistentFlags().VisitAll(add)
	rootCmd.Flags().VisitAll(add)
	serveCmd.Flags().VisitAll(add)
	liveCmd.Flags().VisitAll(add)
	articleCmd.Flags().VisitAll(add)
	watchLaterCmd.Flags().VisitAll(add)
	subAddCmd.Flags().VisitAll(add)
	subCheckCmd.Flags().VisitAll(add)

	merged, err := config.MergeWithConfig(cliArgs, aliasMap, boolFlags)
	if err != nil {
		return cliArgs, nil
	}
	if len(merged) != len(cliArgs) {
		util.Log("加载配置文件完成（配置为默认值，命令行参数优先）")
	}
	return merged, nil
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
	rootCmd.Flags().BoolVar(&optForceHTTP, "force-http", false, "强制HTTP协议")
	// Deprecated compatibility options (upstream hidden flags).
	rootCmd.Flags().StringVar(&optAria2cProxy, "aria2c-proxy", "", "aria2c代理(已弃用)")
	rootCmd.Flags().BoolVar(&optAddDfnSuffix, "add-dfn-subfix", false, "添加画质后缀(已弃用)")
	rootCmd.Flags().BoolVar(&optOnlyHevc, "only-hevc", false, "仅HEVC(已弃用)")
	rootCmd.Flags().BoolVar(&optOnlyAvc, "only-avc", false, "仅AVC(已弃用)")
	rootCmd.Flags().BoolVar(&optOnlyAv1, "only-av1", false, "仅AV1(已弃用)")
	rootCmd.Flags().BoolVar(&optNoPaddingPageNum, "no-padding-page-num", false, "分P编号不补零(已弃用)")
	rootCmd.Flags().BoolVar(&optBandwidthAscending, "bandwith-ascending", false, "码率升序(已弃用)")
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
	serveCmd.Flags().StringVarP(&optServeListen, "listen", "l", "http://127.0.0.1:23333", "API服务器监听地址")
	serveCmd.Flags().IntVar(&optServeMaxConcurrent, "max-concurrent", 3, "最大并发下载数")
	serveCmd.Flags().StringVar(&optServeToken, "serve-token", "", "API认证Token")
	serveCmd.Flags().StringVar(&optNotifyWebhook, "notify-webhook", "", "任务完成通知URL")

	// Subcommand flags
	liveCmd.Flags().StringVarP(&optLiveOutput, "output", "o", "", "输出文件路径(默认: 直播间标题_直播录制_时间.flv)")
	articleCmd.Flags().StringVarP(&optArticleOutput, "output", "o", "", "输出 Markdown 文件路径(默认: 专栏标题.md)")
	watchLaterCmd.Flags().IntVar(&optWatchLaterLimit, "limit", 0, "最多下载前 N 个稍后再看视频(默认 0=全部)")
	subAddCmd.Flags().StringVar(&optSubName, "name", "", "订阅显示名称(默认使用目标字符串)")

	// watchlater / sub check inherit the download option semantics (upstream).
	for _, c := range []*cobra.Command{watchLaterCmd, subCheckCmd} {
		c.Flags().StringVarP(&optEncodingPriority, "encoding-priority", "e", "", "视频编码优先级, 如 hevc,avc,av1")
		c.Flags().StringVarP(&optDfnPriority, "dfn-priority", "q", "", "视频清晰度优先级, 如 8K 4K 1080P 高清 720P 高清")
		c.Flags().BoolVarP(&optUseAppAPI, "use-app-api", "a", false, "使用APP端解析模式")
		c.Flags().BoolVarP(&optUseTvAPI, "use-tv-api", "t", false, "使用TV端解析模式")
		c.Flags().BoolVar(&optUseIntlAPI, "use-intl-api", false, "使用国际版解析模式")
		c.Flags().StringVarP(&optWorkDir, "work-dir", "w", "", "设置工作目录(所有相对路径的根目录)")
	}

	// Register subcommands
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(loginTVCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(liveCmd)
	rootCmd.AddCommand(articleCmd)
	rootCmd.AddCommand(watchLaterCmd)
	rootCmd.AddCommand(subCmd)
	subCmd.AddCommand(subAddCmd)
	subCmd.AddCommand(subListCmd)
	subCmd.AddCommand(subRemoveCmd)
	subCmd.AddCommand(subCheckCmd)
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

	// Fire-and-forget update check (upstream DefaultCommand).
	util.CheckUpdateAsync(context.Background(), client, "v1.6.11")

	wf := workflow.New(cfg, client)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	return wf.Run(ctx)
}
