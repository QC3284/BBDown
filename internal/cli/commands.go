package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/QC3284/BBDown/internal/config"
	"github.com/QC3284/BBDown/internal/live"
	"github.com/QC3284/BBDown/internal/login"
	"github.com/QC3284/BBDown/internal/server"
	"github.com/QC3284/BBDown/internal/util"
	"github.com/QC3284/BBDown/internal/workflow"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "通过APP扫描二维码以登录您的WEB账号",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := buildHTTPClient(config.MyOption{})
		if err := login.LoginWeb(client); err != nil {
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
		client := buildHTTPClient(config.MyOption{})
		if err := login.LoginTV(client); err != nil {
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
		maxConc := optServeMaxConcurrent
		if maxConc <= 0 {
			maxConc = 3
		}

		srv := server.NewAPIServer(listen, maxConc, optServeToken, optNotifyWebhook)

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		return srv.Run(ctx)
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

		client := buildHTTPClient(config.MyOption{})
		streamURL, title, uname, err := live.ResolveLive(roomID, client)
		if err != nil {
			return err
		}

		util.Log("直播间: %s (%s)", title, uname)
		util.Log("开始录制...按 Ctrl+C 停止")

		outPath := live.SanitizeFileName(fmt.Sprintf("%s_%s.flv", uname, title))
		_ = streamURL
		return live.DownloadToFile(roomID, outPath, client)
	},
}

var articleCmd = &cobra.Command{
	Use:   "article [cv_id]",
	Short: "下载B站专栏文章为 Markdown",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return fmt.Errorf("请提供文章cv号")
		}
		return fmt.Errorf("article not yet implemented in Go version")
	},
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

// blank imports to ensure packages are compiled
var _ = workflow.New
var _ = live.DownloadToFile
var _ = server.NewAPIServer
var _ = login.LoginWeb
