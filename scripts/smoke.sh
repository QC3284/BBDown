#!/usr/bin/env bash
# scripts/smoke.sh —— 真机冒烟测试（需要网络，不进 CI 常规 job）。
#
# 为什么需要它：1.6.20-go.3 起，--thread-segment-size 的默认值(0=自动)与参数校验(1~1024)冲突，
# 导致**默认参数下任何下载都直接失败**；而 16 个包、几十条用例、变异验证全绿——因为没有任何一条
# 走「CLI 默认值 → 校验 → 真实下载」这条路。这个脚本就是那条路。
#
# 用法：bash scripts/smoke.sh [BV号]        （默认用一个小体积老视频）
# 退出码：0 = 全过；非 0 = 有步骤失败
set -u

BV="${1:-BV1xx411c7mD}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
BIN="$WORK/BBDown"
fail=0

say() { printf "\n== %s ==\n" "$1"; }
check() { # check <描述> <命令...>
  local desc="$1"; shift
  if "$@" > "$WORK/step.log" 2>&1; then
    printf "[ok]   %s\n" "$desc"
  else
    printf "[FAIL] %s（最后几行见下）\n" "$desc"
    grep -v "^$" "$WORK/step.log" | tail -3
    fail=1
  fi
}

say "构建"
(cd "$ROOT" && go build -o "$BIN" ./cmd/bbdown) || { echo "构建失败"; exit 1; }
echo "[ok]   构建 $BIN"

say "自检（doctor）"
"$BIN" doctor > "$WORK/doctor.log" 2>&1 || true   # doctor 的 fail 项不阻塞 smoke：环境问题不等于程序坏
grep -E "^\[" "$WORK/doctor.log" | sed "s/^/       /"

say "解析（-I，web / APP / TV 三条路）"
check "web 解析" "$BIN" -I "$BV"
check "APP 解析" "$BIN" --use-app-api -I "$BV"
check "TV 解析"  "$BIN" -tv -I "$BV"

say "真实下载（默认参数，只取音频以缩短时间）+ 产物校验"
if "$BIN" --work-dir "$WORK/dl" --audio-only --skip-mux --skip-cover --skip-subtitle "$BV" > "$WORK/dl.log" 2>&1; then
  echo "[ok]   下载退出码 0"
else
  echo "[FAIL] 下载失败"
  grep -v "^$" "$WORK/dl.log" | tail -5
  fail=1
fi

ART="$(find "$WORK/dl" -type f \( -name "*.m4a" -o -name "*.mp4" -o -name "*.flv" \) 2>/dev/null | head -1)"
if [ -n "$ART" ] && [ -s "$ART" ]; then
  echo "[ok]   产物存在：$(basename "$ART")（$(stat -c %s "$ART") 字节）"
  if command -v ffprobe >/dev/null 2>&1; then
    if ffprobe -v error -show_entries format=duration -of csv=p=0 "$ART" > "$WORK/probe.log" 2>&1 && [ -s "$WORK/probe.log" ]; then
      echo "[ok]   ffprobe 可解析（时长 $(cat "$WORK/probe.log")）"
    else
      echo "[FAIL] ffprobe 无法解析产物"
      fail=1
    fi
  else
    echo "[warn] 机器上没有 ffprobe，跳过产物校验"
  fi
else
  echo "[FAIL] 下载成功但没有产物（静默空转）"
  fail=1
fi

say "进度 JSON"
if "$BIN" --work-dir "$WORK/dl2" --audio-only --skip-mux --skip-cover --skip-subtitle --progress-json "$BV" > "$WORK/pj.out" 2> "$WORK/pj.err"; then
  lines=$(grep -c "^{" "$WORK/pj.err" || true)
  if [ "${lines:-0}" -gt 0 ]; then
    echo "[ok]   进度 JSON 事件 $lines 行（首行：$(grep -m1 "^{" "$WORK/pj.err" | cut -c1-80)）"
  else
    echo "[FAIL] --progress-json 没有任何 JSON 事件"
    fail=1
  fi
else
  echo "[FAIL] --progress-json 下载失败"
  fail=1
fi

rm -rf "$WORK"
say "结果"
if [ "$fail" -eq 0 ]; then echo "smoke 全部通过"; else echo "smoke 存在失败项"; fi
exit "$fail"
