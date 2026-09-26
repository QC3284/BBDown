package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/QC3284/BBDown/internal/util"
	"github.com/spf13/cobra"
)

// 未完成任务清单（本仓特色功能 bbdown resume 的底座）。
//
// 为什么需要它：下载被中断（Ctrl+C / 关机 / 网络断）后，重跑同一 URL 虽然能靠稳定身份续传 .tmp、
// 并跳过已完成产物，但**用户得自己记得那些 URL**。resume 把失败/被取消的目标记在工作目录的
// .bbdown-pending.json 里，之后一条 `bbdown resume` 就能全部接着下。

const pendingFileName = ".bbdown-pending.json"

// resumeCmd 重试未完成任务清单里的目标（本仓特色功能）。
var resumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "重试未完成的任务（记录在工作目录的 .bbdown-pending.json）",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := buildMyOption()
		tasks, err := loadPending(cfg.WorkDir)
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			util.Log("没有未完成的任务")
			return nil
		}
		util.Log("待重试 %d 个任务：", len(tasks))
		targets := make([]string, 0, len(tasks))
		for _, t := range tasks {
			if t.LastErr != "" {
				util.Log("  %s（上次错误：%s）", t.URL, t.LastErr)
			} else {
				util.Log("  %s", t.URL)
			}
			targets = append(targets, t.URL)
		}

		client := buildHTTPClient(cfg)
		// 中断 ctx 来自 Execute 的统一安装（见 interrupt.go）：与 runDownload 共用同一条
		// 取消语义（downloadTargets 里那次 installInterrupts 会复用它，不重复注册信号）。
		return downloadTargets(commandContext(cmd), cmd, cfg, client, targets)
	},
}

// pendingTask 只记「重试需要什么」：URL 与最后一次错误（给用户看）。
type pendingTask struct {
	URL     string `json:"url"`
	AddedAt string `json:"added_at"`
	LastErr string `json:"last_error,omitempty"`
}

// pendingPath 返回清单文件路径（工作目录为空时用当前目录）。
func pendingPath(workDir string) string {
	if workDir == "" {
		workDir = "."
	}
	return filepath.Join(workDir, pendingFileName)
}

// loadPending 读取清单；文件不存在视为空清单（不是错误），内容损坏则报错（不静默丢任务）。
func loadPending(workDir string) ([]pendingTask, error) {
	data, err := os.ReadFile(pendingPath(workDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取未完成任务清单失败: %w", err)
	}
	var tasks []pendingTask
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("未完成任务清单 %s 内容损坏: %w", pendingPath(workDir), err)
	}
	return tasks, nil
}

// savePending 原子写清单（先写临时文件再 rename，避免中断留下半个文件）。
func savePending(workDir string, tasks []pendingTask) error {
	path := pendingPath(workDir)
	if len(tasks) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清理未完成任务清单失败: %w", err)
		}
		return nil
	}
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入未完成任务清单失败: %w", err)
	}
	return os.Rename(tmp, path)
}

// upsertPending 记录/更新一个未完成目标（同 URL 只留一条，错误信息刷新）。
func upsertPending(workDir, url, lastErr string) error {
	tasks, err := loadPending(workDir)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	for i := range tasks {
		if tasks[i].URL == url {
			tasks[i].LastErr = lastErr
			return savePending(workDir, tasks)
		}
	}
	tasks = append(tasks, pendingTask{URL: url, AddedAt: now, LastErr: lastErr})
	return savePending(workDir, tasks)
}

// removePending 把已完成的目标从清单里去掉。
func removePending(workDir, url string) error {
	tasks, err := loadPending(workDir)
	if err != nil {
		return err
	}
	kept := tasks[:0]
	for _, t := range tasks {
		if t.URL != url {
			kept = append(kept, t)
		}
	}
	return savePending(workDir, kept)
}
