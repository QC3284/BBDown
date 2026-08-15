package substore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/QC3284/BBDown/internal/util"
)

// Subscription is a persisted subscription entry.
type Subscription struct {
	Target  string `json:"Target"`
	Name    string `json:"Name"`
	AddedAt int64  `json:"AddedAt"`
}

// CorruptError signals corrupted persistence data; callers must abort the whole
// batch instead of continuing with an untrustworthy history (upstream
// SubscriptionDataCorruptException).
type CorruptError struct{ msg string }

func (e *CorruptError) Error() string { return e.msg }

// StoreRoot is the directory holding the subscription files (exe dir by default).
var StoreRoot = exeDir()

var ioLock sync.Mutex

func exeDir() string {
	return util.ExecutableDir()
}

func subFile() string     { return filepath.Join(StoreRoot, "BBDownSubscriptions.json") }
func historyFile() string { return filepath.Join(StoreRoot, "BBDownSubscriptions.history.json") }

// atomicWrite replaces the file atomically (temp + rename).
func atomicWrite(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// isolateCorruptFile quarantines a corrupt file.
func isolateCorruptFile(path string) string {
	corrupt := fmt.Sprintf("%s.corrupt-%s-%d", path, time.Now().Format("20060102150405.000"), time.Now().UnixNano())
	if err := os.Rename(path, corrupt); err != nil {
		return ""
	}
	return corrupt
}

// Load returns the subscription list. Corruption aborts with CorruptError.
func Load() ([]Subscription, error) {
	data, err := os.ReadFile(subFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &CorruptError{fmt.Sprintf("读取订阅清单失败: %v", err)}
	}
	var subs []Subscription
	if err := json.Unmarshal(data, &subs); err != nil {
		corrupt := isolateCorruptFile(subFile())
		util.LogError("订阅清单损坏（%v），已隔离为 %s，中止操作以避免覆盖订阅", err, corrupt)
		return nil, &CorruptError{fmt.Sprintf("订阅清单损坏，已隔离为 %s，请检查后恢复", corrupt)}
	}
	return subs, nil
}

// Add appends a subscription (replacing an existing one with the same target).
func Add(target, name string) error {
	ioLock.Lock()
	defer ioLock.Unlock()
	subs, err := Load()
	if err != nil {
		return err
	}
	if name == "" {
		name = target
	}
	for i := range subs {
		if subs[i].Target == target {
			subs[i].Name = name
			return atomicWrite(subFile(), subs)
		}
	}
	subs = append(subs, Subscription{Target: target, Name: name, AddedAt: time.Now().Unix()})
	return atomicWrite(subFile(), subs)
}

// Remove deletes a subscription by target.
func Remove(target string) error {
	ioLock.Lock()
	defer ioLock.Unlock()
	subs, err := Load()
	if err != nil {
		return err
	}
	out := subs[:0]
	for _, s := range subs {
		if s.Target != target {
			out = append(out, s)
		}
	}
	return atomicWrite(subFile(), out)
}

// ListSorted returns subscriptions ordered by AddedAt.
func ListSorted() ([]Subscription, error) {
	subs, err := Load()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(subs, func(i, j int) bool { return subs[i].AddedAt < subs[j].AddedAt })
	return subs, nil
}

// LoadHistory returns the downloaded aid history for a target.
func LoadHistory(target string) ([]string, error) {
	data, err := os.ReadFile(historyFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &CorruptError{fmt.Sprintf("读取订阅历史失败: %v", err)}
	}
	var m map[string][]string
	if err := json.Unmarshal(data, &m); err != nil {
		corrupt := isolateCorruptFile(historyFile())
		util.LogError("订阅历史损坏（%v），已隔离为 %s，中止操作以避免重复下载", err, corrupt)
		return nil, &CorruptError{fmt.Sprintf("订阅历史损坏，已隔离为 %s，请检查后恢复", corrupt)}
	}
	return m[target], nil
}

// RecordDownloaded marks an aid as downloaded for a target.
func RecordDownloaded(target, aid string) error {
	ioLock.Lock()
	defer ioLock.Unlock()
	var m map[string][]string
	data, err := os.ReadFile(historyFile())
	if err == nil {
		if jerr := json.Unmarshal(data, &m); jerr != nil {
			corrupt := isolateCorruptFile(historyFile())
			util.LogError("订阅历史损坏（%v），已隔离为 %s，中止操作以避免重复下载", jerr, corrupt)
			return &CorruptError{fmt.Sprintf("订阅历史损坏，已隔离为 %s，请检查后恢复", corrupt)}
		}
	}
	if m == nil {
		m = make(map[string][]string)
	}
	list := m[target]
	for _, a := range list {
		if a == aid {
			return nil
		}
	}
	m[target] = append(list, aid)
	return atomicWrite(historyFile(), m)
}
