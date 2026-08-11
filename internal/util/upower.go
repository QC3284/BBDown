package util

import (
	"fmt"
)

// PreviewVerdict is the result of upower charge preview detection.
type PreviewVerdict struct {
	IsPreview bool
	Reason    string
}

const (
	// previewRatioThreshold: actual duration must be below this ratio of declared to be considered preview.
	previewRatioThreshold = 0.9
	// minAbsoluteGapSec: minimum absolute gap in seconds to trigger detection.
	minAbsoluteGapSec = 30
)

// InspectUpower checks if the parsed content is a preview clip of a charge-exclusive video.
func InspectUpower(
	isUpowerExclusive bool,
	isUpowerPlay bool,
	declaredDurationSec int,
	actualDurationSec int,
) PreviewVerdict {
	// Duration cross-check: if actual is significantly shorter than declared, it's likely a preview.
	durationMismatch := declaredDurationSec > 0 &&
		actualDurationSec > 0 &&
		declaredDurationSec-actualDurationSec >= minAbsoluteGapSec &&
		float64(actualDurationSec) < float64(declaredDurationSec)*previewRatioThreshold

	if isUpowerExclusive && !isUpowerPlay {
		reason := "当前账号没有该UP主的充电权限，接口返回的可能只是试看片段"
		if durationMismatch {
			reason = fmt.Sprintf(
				"当前账号没有该UP主的充电权限，接口只返回了 %s 的试看片段（完整视频 %s）",
				formatSec(actualDurationSec), formatSec(declaredDurationSec))
		}
		return PreviewVerdict{IsPreview: true, Reason: reason}
	}

	if durationMismatch {
		return PreviewVerdict{
			IsPreview: true,
			Reason: fmt.Sprintf(
				"实际解析到的时长 %s 明显短于稿件时长 %s，很可能只是试看片段",
				formatSec(actualDurationSec), formatSec(declaredDurationSec)),
		}
	}

	return PreviewVerdict{IsPreview: false}
}

func formatSec(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
