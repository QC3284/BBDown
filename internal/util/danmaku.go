package util

import (
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	monitorWidth  = 1920
	monitorHeight = 1080
	fontSize      = 40
	moveSpendTime = 8.00
	topSpendTime  = 4.00
	protectLength = 50

	posMove   = 1
	posTop    = 2
	posBottom = 3
)

// DanmakuItem represents a single danmaku comment.
type DanmakuItem struct {
	Content     string
	StartTime   string
	Second      float64
	EndTime     string
	DanmakuMode int
	FontSize    string
	Color       string
	Timestamp   string
	MidHash     string
}

// DanmakuList is a sortable slice of DanmakuItem.
type DanmakuList []DanmakuItem

func (d DanmakuList) Len() int           { return len(d) }
func (d DanmakuList) Less(i, j int) bool { return d[i].Second < d[j].Second }
func (d DanmakuList) Swap(i, j int)      { d[i], d[j] = d[j], d[i] }

// ParseDanmakuXML parses a Bilibili danmaku XML file.
func ParseDanmakuXML(xmlPath string) (DanmakuList, error) {
	data, err := os.ReadFile(xmlPath)
	if err != nil {
		return nil, err
	}

	type D struct {
		P    string `xml:"p,attr"`
		Text string `xml:",chardata"`
	}
	type Root struct {
		Ds []D `xml:"d"`
	}
	var root Root
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse danmaku xml: %w", err)
	}

	var items DanmakuList
	for _, d := range root.Ds {
		parts := strings.Split(d.P, ",")
		if len(parts) < 8 {
			continue
		}

		mode := posMove
		switch parts[1] {
		case "4":
			mode = posBottom
		case "5":
			mode = posTop
		}

		second, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			continue
		}

		displayTime := topSpendTime
		if mode == posMove {
			displayTime = moveSpendTime
		}

		item := DanmakuItem{
			DanmakuMode: mode,
			Second:      second,
			StartTime:   computeDanmakuTime(second),
			EndTime:     computeDanmakuTime(second + displayTime),
			FontSize:    parts[2],
			Timestamp:   parts[4],
			MidHash:     parts[6],
			Content:     d.Text,
		}

		if colorD, err := strconv.ParseInt(parts[3], 10, 64); err == nil {
			item.Color = fmt.Sprintf("%06X", colorD)
		}

		items = append(items, item)
	}
	return items, nil
}

func computeDanmakuTime(second float64) string {
	h := int(second) / 3600
	m := (int(second) - h*3600) / 60
	s := second - float64(h*3600) - float64(m*60)
	return fmt.Sprintf("%d:%02d:%05.2f", h, m, s)
}

// FilterDanmaku filters danmaku by keyword and/or user midHash blacklists.
func FilterDanmaku(items DanmakuList, keywordFilter, userFilter string) DanmakuList {
	if len(items) == 0 {
		return items
	}
	if keywordFilter == "" && userFilter == "" {
		return items
	}

	kws := splitTrim(keywordFilter)
	uids := splitTrim(userFilter)

	var result DanmakuList
	for _, d := range items {
		if len(uids) > 0 && d.MidHash != "" && contains(uids, d.MidHash) {
			continue
		}
		blocked := false
		for _, kw := range kws {
			if strings.Contains(d.Content, kw) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		result = append(result, d)
	}
	return result
}

func splitTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

type positionController struct {
	maxLine     int
	moveQueue   []float64
	topQueue    []float64
	bottomQueue []float64
}

func newPositionController() *positionController {
	maxLine := monitorHeight * protectLength / fontSize / 100
	pc := &positionController{maxLine: maxLine}
	for i := 0; i < maxLine; i++ {
		pc.moveQueue = append(pc.moveQueue, 0.0)
		pc.topQueue = append(pc.topQueue, 0.0)
		pc.bottomQueue = append(pc.bottomQueue, 0.0)
	}
	return pc
}

func (pc *positionController) updatePosition(mode int, time float64, length int) int {
	var queue []float64
	displayTime := topSpendTime
	switch mode {
	case posBottom:
		queue = pc.bottomQueue
	case posTop:
		queue = pc.topQueue
	default:
		queue = pc.moveQueue
		displayTime = moveSpendTime * float64(length+5) * fontSize / (monitorWidth + (float64(length) * moveSpendTime))
	}

	for i := 0; i < pc.maxLine; i++ {
		if time >= queue[i] {
			queue[i] = time + displayTime
			return i * fontSize
		}
	}
	return -1
}

// SaveDanmakuAsASS converts danmaku items to ASS subtitle format and saves.
func SaveDanmakuAsASS(items DanmakuList, outputPath string) error {
	sort.Sort(items)
	controller := newPositionController()

	var sb strings.Builder
	sb.WriteString("[Script Info]\n")
	sb.WriteString("Script Updated By: BBDown(https://github.com/QC3284/BBDown)\n")
	sb.WriteString("ScriptType: v4.00+\n")
	sb.WriteString(fmt.Sprintf("PlayResX: %d\n", monitorWidth))
	sb.WriteString(fmt.Sprintf("PlayResY: %d\n", monitorHeight))
	sb.WriteString(fmt.Sprintf("Aspect Ratio: %d:%d\n", monitorWidth, monitorHeight))
	sb.WriteString("Collisions: Normal\n")
	sb.WriteString("WrapStyle: 2\n")
	sb.WriteString("ScaledBorderAndShadow: yes\n")
	sb.WriteString("YCbCr Matrix: TV.601\n")
	sb.WriteString("[V4+ Styles]\n")
	sb.WriteString("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	sb.WriteString(fmt.Sprintf("Style: BBDOWN_Style, 黑体, %d, &H00FFFFFF, &H00FFFFFF, &H00000000, &H00000000, 0, 0, 0, 0, 100, 100, 0.00, 0.00, 1, 2, 0, 7, 0, 0, 0, 0\n", fontSize))
	sb.WriteString("[Events]\n")
	sb.WriteString("Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")

	for _, d := range items {
		height := controller.updatePosition(d.DanmakuMode, d.Second, len(d.Content))
		if height == -1 {
			continue
		}

		var effect string
		switch d.DanmakuMode {
		case posBottom:
			effect = fmt.Sprintf(`\an8\pos(%d, %d)`, monitorWidth/2, monitorHeight-fontSize-height)
		case posTop:
			effect = fmt.Sprintf(`\an8\pos(%d, %d)`, monitorWidth/2, height)
		default:
			effect = fmt.Sprintf(`\move(%d, %d, %d, %d)`, monitorWidth, height, -len(d.Content)*fontSize, height)
		}

		if len(d.Color) == 6 && d.Color != "FFFFFF" {
			bgr := d.Color[4:] + d.Color[2:4] + d.Color[:2]
			effect += fmt.Sprintf(`\c&H%s&`, bgr)
		}

		content := escapeAssText(d.Content)
		sb.WriteString(fmt.Sprintf("Dialogue: 2,%s,%s,BBDOWN_Style,,0000,0000,0000,,{%s}%s\n",
			d.StartTime, d.EndTime, effect, content))
	}

	return os.WriteFile(outputPath, []byte(sb.String()), 0o644)
}

func escapeAssText(content string) string {
	var sb strings.Builder
	sb.Grow(len(content))
	for i := 0; i < len(content); i++ {
		switch content[i] {
		case '{':
			sb.WriteString("｛")
		case '}':
			sb.WriteString("｝")
		case '\r':
			if i+1 < len(content) && content[i+1] == '\n' {
				i++
			}
			sb.WriteString("\\N")
		case '\n':
			sb.WriteString("\\N")
		default:
			sb.WriteByte(content[i])
		}
	}
	return sb.String()
}

// FormatFileSize formats a file size in human-readable form.
func FormatFileSize(size float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	unitIdx := 0
	for size >= 1024 && unitIdx < len(units)-1 {
		size /= 1024
		unitIdx++
	}
	return fmt.Sprintf("%.2f %s", size, units[unitIdx])
}

// FormatTime converts seconds to duration string.
// absolute=true: HH:MM:SS; absolute=false: HhMMmSSs or MMmSSs.
func FormatTime(seconds int, absolute bool) string {
	if absolute {
		h := seconds / 3600
		m := (seconds % 3600) / 60
		s := seconds % 60
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	return fmt.Sprintf("%02dm%02ds", m, s)
}

// CombineMultipleFilesIntoSingleFile concatenates files into one.
func CombineMultipleFilesIntoSingleFile(files []string, output string) error {
	out, err := os.Create(output)
	if err != nil {
		return err
	}
	defer out.Close()

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if _, err := out.Write(data); err != nil {
			return err
		}
	}
	return nil
}
