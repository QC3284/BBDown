package download

import (
	"encoding/xml"
	"fmt"
	"time"
)

// 侧车元数据（NFO）：Kodi / Emby / Jellyfin 扫库时直接读同目录的 <产物>.nfo。
//
// 这是本仓的普通功能：把「下载」与「入库」之间的手工补齐（改文件名、填标题、贴海报）省掉。
type nfoMovie struct {
	XMLName  xml.Name      `xml:"movie"`
	Title    string        `xml:"title"`
	Plot     string        `xml:"plot,omitempty"`
	Studio   string        `xml:"studio,omitempty"`
	Aired    string        `xml:"aired,omitempty"`
	UniqueID []nfoUniqueID `xml:"uniqueid"`
	Episode  int           `xml:"episode,omitempty"`
}

type nfoUniqueID struct {
	Type    string `xml:"type,attr"`
	Default string `xml:"default,attr"`
	Value   string `xml:",chardata"`
}

// RenderNFO 生成 NFO 内容（含 XML 头）。
//
// title 是稿件标题，pageTitle 是分P标题（单P 时传空，用稿件标题即可）；bvid 写进 uniqueid，
// 便于媒体库去重与回查；pubTime 为发布时间戳（0 表示未知，则省略 aired）。
func RenderNFO(title, pageTitle, owner, bvid string, pageIndex int, pubTime int64) (string, error) {
	display := pageTitle
	if display == "" {
		display = title
	}
	m := nfoMovie{
		Title:    display,
		Plot:     title,
		Studio:   owner,
		UniqueID: []nfoUniqueID{{Type: "bilibili", Default: "true", Value: bvid}},
	}
	if pubTime > 0 {
		m.Aired = time.Unix(pubTime, 0).Format("2006-01-02")
	}
	if pageIndex > 1 {
		m.Episode = pageIndex
	}
	body, err := xml.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("生成 NFO 失败: %w", err)
	}
	return xml.Header + string(body) + "\n", nil
}
