package download

import (
	"encoding/json"
	"fmt"

	"github.com/QC3284/BBDown/internal/entity"
)

// InfoPayload 是 `--info-json` 的输出结构：把解析结果交给程序（脚本/GUI/调度器）而不是给人看。
//
// 与 `-I`（人看的流列表）、`--print-urls`（给下载器的直链）互补：这里是**元数据**——标题/分P/轨道参数，
// 调用方据此决定「下不下、下哪一档、要不要排队」。字段名用 snake_case，与仓库其它 JSON 输出（doctor --json、
// progress-json）保持一致。
type InfoPayload struct {
	Title       string         `json:"title"`
	Bvid        string         `json:"bvid"`
	Aid         string         `json:"aid"`
	Cid         string         `json:"cid"`
	PageIndex   int            `json:"page_index"`
	PageTitle   string         `json:"page_title"`
	DurationSec int            `json:"duration_sec"`
	Video       []entity.Video `json:"video"`
	Audio       []entity.Audio `json:"audio"`
	Clips       []string       `json:"clips,omitempty"`
}

// RenderInfoJSON 渲染元数据 JSON（带缩进，便于人肉排查；程序照样能解析）。
//
// 空轨道列表写 `[]` 而不是 null：调用方（jq/脚本/强类型客户端）不必再判空。
func RenderInfoJSON(p InfoPayload) (string, error) {
	if p.Video == nil {
		p.Video = []entity.Video{}
	}
	if p.Audio == nil {
		p.Audio = []entity.Audio{}
	}
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", fmt.Errorf("生成元数据 JSON 失败: %w", err)
	}
	return string(body) + "\n", nil
}
