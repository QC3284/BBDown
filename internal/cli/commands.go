package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/QC3284/BBDown/internal/article"
	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/fetcher"
	"github.com/QC3284/BBDown/internal/live"
	"github.com/QC3284/BBDown/internal/login"
	"github.com/QC3284/BBDown/internal/muxer"
	"github.com/QC3284/BBDown/internal/server"
	"github.com/QC3284/BBDown/internal/substore"
	"github.com/QC3284/BBDown/internal/util"
	"github.com/QC3284/BBDown/internal/workflow"
	"github.com/spf13/cobra"
)

// Batch-command WBI presets: fetched once per command, reused per item to
// avoid one nav request per video (upstream initializes the session once).
var (
	watchLaterWbi string
	subCheckWbi   string
)

// warnAppAPIWithoutToken 提示 APP 接口不识别 WEB 登录 Cookie（与上游行为一致）：
// 登录用户加 -a 看不到高清属于预期行为，需要 APP token 或改用默认 WEB 接口。
func warnAppAPIWithoutToken(useAppAPI bool, token string) {
	if useAppAPI && strings.TrimSpace(token) == "" {
		util.LogWarn("提示: APP 接口(-a)不识别 WEB 登录 Cookie，登录后也不会解锁更高画质；如需高清请使用默认 WEB 接口，或通过 --access-token 传入 APP token")
	}
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "通过APP扫描二维码以登录您的WEB账号",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		client := buildHTTPClient(config.MyOption{})
		if err := login.LoginWeb(ctx, client); err != nil {
			return err
		}
		util.Log("WEB 登录成功！")
		return nil
	},
}

var loginTVCmd = &cobra.Command{
	Use:   "logintv",
	Short: "通过APP扫描二维码以登录您的TV账号",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		client := buildHTTPClient(config.MyOption{})
		if err := login.LoginTV(ctx, client); err != nil {
			return err
		}
		util.Log("TV 登录成功！")
		return nil
	},
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "以服务器模式运行",
	RunE: func(cmd *cobra.Command, args []string) error {
		listen := optServeListen
		if listen == "" {
			listen = "http://127.0.0.1:23333"
		}
		// Upstream rejects max-concurrent < 1 instead of silently clamping.
		if optServeMaxConcurrent < 1 {
			return fmt.Errorf("参数有误：--max-concurrent 需 >= 1，当前为 %d", optServeMaxConcurrent)
		}

		srv := server.NewAPIServer(listen, optServeMaxConcurrent, optServeToken, optNotifyWebhook)

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		// Fire-and-forget update check (upstream ServeCommand).
		util.CheckUpdateAsync(ctx, buildHTTPClient(config.MyOption{}), "v1.6.11")

		err := srv.Run(ctx)
		if errors.Is(err, http.ErrServerClosed) {
			// Ctrl+C 正常关停，不算错误。
			return nil
		}
		return err
	},
}

var liveCmd = &cobra.Command{
	Use:   "live [room_id]",
	Short: "录制B站直播流",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return fmt.Errorf("请提供直播间ID")
		}
		roomID := args[0]

		// Segment merging at the end requires ffmpeg; fail fast (upstream).
		if _, err := os.Stat(muxer.FFMPEG); err != nil {
			if p, err := exec.LookPath("ffmpeg"); err == nil {
				muxer.FFMPEG = p
			} else {
				return fmt.Errorf("找不到可执行的ffmpeg文件，直播分段合成需要 ffmpeg")
			}
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		client := buildHTTPClient(config.MyOption{})
		util.Log("正在解析直播间 %s...", roomID)
		_, title, uname, err := live.ResolveLive(ctx, roomID, client)
		if err != nil {
			return err
		}

		util.Log("直播间: %s (UP: %s)", title, uname)
		outPath := optLiveOutput
		if outPath == "" {
			outPath = fmt.Sprintf("%s_直播录制_%s.flv", live.SanitizeFileName(title), time.Now().Format("20060102_150405"))
		}
		util.Log("开始录制直播流: %s (Ctrl+C 停止，断流自动重连)", outPath)

		recorded, err := live.DownloadToFile(ctx, roomID, outPath, client)
		if err != nil {
			return silenceOnCancel(cmd, err)
		}
		if !recorded {
			util.Log("未录制到任何内容")
		} else {
			util.Log("直播录制完成: %s", outPath)
		}
		return nil
	},
}

var articleCmd = &cobra.Command{
	Use:   "article [cv_id]",
	Short: "下载B站专栏文章为 Markdown",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return fmt.Errorf("请提供文章cv号")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		cvID, err := article.ExtractCvId(args[0])
		if err != nil {
			return err
		}
		util.Log("正在获取专栏 cv%s...", cvID)
		client := buildHTTPClient(config.MyOption{})
		a, err := article.Fetch(ctx, client, cvID)
		if err != nil {
			return silenceOnCancel(cmd, err)
		}
		path := optArticleOutput
		if path == "" {
			path = live.SanitizeFileName(a.Title) + ".md"
		}
		if err := article.SaveAsMarkdown(a, path); err != nil {
			return fmt.Errorf("专栏保存失败: %w", err)
		}
		util.Log("专栏已保存: %s", path)
		return nil
	},
}

var watchLaterCmd = &cobra.Command{
	Use:   "watchlater",
	Short: "下载稍后再看列表",
	RunE:  runWatchLater,
}

var subCmd = &cobra.Command{
	Use:   "sub",
	Short: "订阅管理 (add/list/remove/check)",
}

var subAddCmd = &cobra.Command{
	Use:   "add [target]",
	Short: "添加订阅",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := substore.Add(args[0], optSubName); err != nil {
			return err
		}
		util.Log("已添加订阅: %s", args[0])
		return nil
	},
}

var subListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出订阅",
	RunE: func(cmd *cobra.Command, args []string) error {
		subs, err := substore.ListSorted()
		if err != nil {
			return err
		}
		if len(subs) == 0 {
			util.Log("当前没有订阅，请先用 BBDown sub add <目标> 添加")
			return nil
		}
		util.Log("共 %d 个订阅:", len(subs))
		for _, s := range subs {
			util.Log("  %s  [%s]  (添加于 %s)", s.Target, s.Name, time.Unix(s.AddedAt, 0).Local().Format("2006-01-02 15:04"))
		}
		return nil
	},
}

var subRemoveCmd = &cobra.Command{
	Use:   "remove [target]",
	Short: "移除订阅",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := substore.Remove(args[0]); err != nil {
			return err
		}
		util.Log("已移除订阅: %s", args[0])
		return nil
	},
}

var subCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "检查订阅并下载新内容",
	RunE:  runSubCheck,
}

// runWatchLater downloads the watch-later list (upstream WatchLaterCommand).
func runWatchLater(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cfg := config.DefaultMyOption()
	cfg.Cookie = optCookie
	cfg.AccessToken = optToken
	cfg.UseTvAPI = optUseTvAPI
	cfg.UseAppAPI = optUseAppAPI
	cfg.UseIntlAPI = optUseIntlAPI
	cfg.WorkDir = optWorkDir
	warnAppAPIWithoutToken(cfg.UseAppAPI, cfg.AccessToken)

	client := buildHTTPClient(cfg)
	wbi, err := workflow.InitSession(ctx, &cfg, client)
	if err != nil {
		return err
	}
	watchLaterWbi = wbi

	util.Log("正在获取稍后再看列表...")
	list, err := fetchWatchLater(ctx, client)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		util.Log("稍后再看列表为空")
		return nil
	}

	targets := list
	if optWatchLaterLimit > 0 && len(targets) > optWatchLaterLimit {
		targets = targets[:optWatchLaterLimit]
	}
	util.Log("共 %d 个稍后再看，开始下载 %d 个...", len(list), len(targets))
	succeeded := 0
	failed := 0
	for _, t := range targets {
		util.Log("--- 下载 av%s %s ---", t.Aid, t.Title)
		opt := config.DefaultMyOption()
		opt.URL = "av" + t.Aid
		opt.Cookie = cfg.Cookie
		opt.AccessToken = cfg.AccessToken
		opt.EncodingPriority = optEncodingPriority
		opt.DfnPriority = optDfnPriority
		opt.UseAppAPI = cfg.UseAppAPI
		opt.UseTvAPI = cfg.UseTvAPI
		opt.UseIntlAPI = cfg.UseIntlAPI
		opt.WorkDir = cfg.WorkDir
		opt.Wbi = watchLaterWbi
		if err := workflow.New(opt, client).Run(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return silenceOnCancel(cmd, err)
			}
			failed++
			util.LogWarn("av%s 下载失败（继续下一个）: %v", t.Aid, err)
			continue
		}
		succeeded++
	}
	util.Log("稍后再看下载完成：成功 %d 个，失败 %d 个", succeeded, failed)
	if failed > 0 {
		return fmt.Errorf("%d 个下载失败", failed)
	}
	return nil
}

type watchLaterItem struct {
	Aid   string `json:"aid"`
	Title string `json:"title"`
}

func fetchWatchLater(ctx context.Context, client *util.HTTPClient) ([]watchLaterItem, error) {
	api := "https://api.bilibili.com/x/v2/history/toview"
	source, err := client.GetWebSource(ctx, api)
	if err != nil {
		return nil, err
	}
	var r struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			List []watchLaterItem `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(source), &r); err != nil {
		return nil, err
	}
	if r.Code != 0 {
		return nil, fmt.Errorf("获取稍后再看失败(code=%d): %s。该接口需要登录，请先运行 BBDown login 或传入 --cookie。", r.Code, r.Message)
	}
	var list []watchLaterItem
	for _, item := range r.Data.List {
		if item.Aid != "" {
			list = append(list, item)
		}
	}
	return list, nil
}

// runSubCheck checks all subscriptions and downloads new content (upstream).
func runSubCheck(cmd *cobra.Command, args []string) error {
	subs, err := substore.Load()
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		util.LogWarn("当前没有订阅，请先用 BBDown sub add <目标> 添加")
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cfg := config.DefaultMyOption()
	cfg.Cookie = optCookie
	cfg.AccessToken = optToken
	cfg.UseTvAPI = optUseTvAPI
	cfg.UseAppAPI = optUseAppAPI
	cfg.UseIntlAPI = optUseIntlAPI
	cfg.WorkDir = optWorkDir
	warnAppAPIWithoutToken(cfg.UseAppAPI, cfg.AccessToken)
	client := buildHTTPClient(cfg)
	wbi, err := workflow.InitSession(ctx, &cfg, client)
	if err != nil {
		return err
	}
	subCheckWbi = wbi

	factory := fetcher.NewFactory(client, cfg.UseIntlAPI, wbi, cfg.Cookie, cfg.Host, cfg.EpHost, cfg.AccessToken)
	for _, sub := range subs {
		if ctx.Err() != nil {
			return silenceOnCancel(cmd, ctx.Err())
		}
		util.Log("检查订阅: %s (%s)", sub.Name, sub.Target)
		resolved, err := workflow.ResolveURL(ctx, client, sub.Target)
		if err != nil {
			util.LogWarn("订阅解析失败（跳过）: %v", err)
			continue
		}
		if resolved == "" {
			continue
		}
		vInfo, err := factory.Create(resolved).Fetch(ctx, resolved)
		if err != nil {
			util.LogWarn("订阅拉取失败（跳过）: %v", err)
			continue
		}
		var allAids []string
		seen := make(map[string]bool)
		for _, p := range vInfo.PagesInfo {
			if p.Aid != "" && !seen[p.Aid] {
				seen[p.Aid] = true
				allAids = append(allAids, p.Aid)
			}
		}
		history, err := substore.LoadHistory(sub.Target)
		if err != nil {
			return err
		}
		var newAids []string
		for _, aid := range allAids {
			known := false
			for _, h := range history {
				if h == aid {
					known = true
					break
				}
			}
			if !known {
				newAids = append(newAids, aid)
			}
		}
		if len(newAids) == 0 {
			util.Log("  没有新增内容")
			continue
		}
		util.Log("  发现 %d 个新内容: av%s", len(newAids), joinAids(newAids))
		for _, aid := range newAids {
			opt := config.DefaultMyOption()
			opt.URL = "av" + aid
			opt.Cookie = cfg.Cookie
			opt.AccessToken = cfg.AccessToken
			opt.EncodingPriority = optEncodingPriority
			opt.DfnPriority = optDfnPriority
			opt.UseAppAPI = cfg.UseAppAPI
			opt.UseTvAPI = cfg.UseTvAPI
			opt.UseIntlAPI = cfg.UseIntlAPI
			opt.WorkDir = cfg.WorkDir
			opt.Wbi = subCheckWbi
			if err := workflow.New(opt, client).Run(ctx); err != nil {
				util.LogWarn("av%s 下载失败: %v", aid, err)
				continue
			}
			if err := substore.RecordDownloaded(sub.Target, aid); err != nil {
				return err
			}
		}
	}
	util.Log("订阅检查完成")
	return nil
}

func joinAids(aids []string) string {
	var sb []string
	for _, a := range aids {
		sb = append(sb, "av"+a)
	}
	return strings.Join(sb, ", ")
}

// buildMyOption constructs a MyOption from CLI flags.
func buildMyOption() config.MyOption {
	return config.MyOption{
		URL:                    optURL,
		UseTvAPI:               optUseTvAPI,
		UseAppAPI:              optUseAppAPI,
		UseIntlAPI:             optUseIntlAPI,
		UseMP4box:              optUseMP4box,
		EncodingPriority:       optEncodingPriority,
		DfnPriority:            optDfnPriority,
		OnlyShowInfo:           optOnlyShowInfo,
		ShowAll:                optShowAll,
		UseAria2c:              optUseAria2c,
		Interactive:            optInteractive,
		HideStreams:            optHideStreams,
		MultiThread:            optMultiThread,
		SimplyMux:              optSimplyMux,
		VideoOnly:              optVideoOnly,
		AudioOnly:              optAudioOnly,
		DanmakuOnly:            optDanmakuOnly,
		CoverOnly:              optCoverOnly,
		SubOnly:                optSubOnly,
		Debug:                  debug,
		SkipMux:                optSkipMux,
		Insecure:               optInsecure,
		DecryptDrm:             optDecryptDrm,
		AllowPreview:           optAllowPreview,
		DrmKeyHex:              optDrmKeyHex,
		DrmKidHex:              optDrmKidHex,
		Mp4decryptPath:         optMp4decryptPath,
		WvdPath:                optWvdPath,
		SkipSubtitle:           optSkipSubtitle,
		SkipCover:              optSkipCover,
		ForceHTTP:              optForceHTTP,
		DownloadDanmaku:        optDownloadDanmaku,
		DownloadDanmakuFormats: optDanmakuFormats,
		DanmakuFilter:          optDanmakuFilter,
		DanmakuFilterUser:      optDanmakuFilterUser,
		DownloadComments:       optDownloadComments,
		NotifyWebhook:          optNotifyWebhook,
		SkipAi:                 optSkipAi,
		VideoAscending:         optVideoAscending,
		AudioAscending:         optAudioAscending,
		AllowPcdn:              optAllowPcdn,
		FilePattern:            optFilePattern,
		MultiFilePattern:       optMultiFilePattern,
		SelectPage:             optSelectPage,
		Language:               optLanguage,
		UserAgent:              optUserAgent,
		Cookie:                 optCookie,
		AccessToken:            optToken,
		Aria2cArgs:             optAria2cArgs,
		Aria2cProxy:            optAria2cProxy,
		AddDfnSuffix:           optAddDfnSuffix,
		OnlyHevc:               optOnlyHevc,
		OnlyAvc:                optOnlyAvc,
		OnlyAv1:                optOnlyAv1,
		NoPaddingPageNum:       optNoPaddingPageNum,
		BandwidthAscending:     optBandwidthAscending,
		WorkDir:                optWorkDir,
		FFmpegPath:             optFFmpegPath,
		Mp4boxPath:             optMp4boxPath,
		Aria2cPath:             optAria2cPath,
		UposHost:               optUposHost,
		ForceReplaceHost:       optForceReplaceHost,
		SaveArchivesToFile:     optSaveArchives,
		DelayPerPage:           optDelayPerPage,
		MuxerTimeout:           optMuxerTimeout,
		RetryCount:             optRetryCount,
		RetryDelay:             optRetryDelay,
		ThreadSegmentSize:      optThreadSegmentSize,
		Host:                   optHost,
		EpHost:                 optEpHost,
		TvHost:                 optTvHost,
		Area:                   optArea,
		ConfigFile:             optConfigFile,
		ServeListenURL:         optServeListen,
		ServeMaxConcurrent:     optServeMaxConcurrent,
		ServeToken:             optServeToken,
	}
}

// buildHTTPClient creates an HTTPClient from CLI options.
func buildHTTPClient(cfg config.MyOption) *util.HTTPClient {
	client := util.NewHTTPClient(
		func() bool { return optInsecure || cfg.Insecure },
		func() string { return optCookie },
		func(format string, args ...interface{}) {
			if debug || cfg.Debug {
				util.LogDebug(format, args...)
			}
		},
	)
	if optUserAgent != "" {
		client.SetUserAgent(optUserAgent)
	}
	return client
}
