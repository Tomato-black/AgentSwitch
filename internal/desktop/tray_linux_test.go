//go:build linux

package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMirrorPanelIconWritesPanelCopies 覆盖 Ubuntu GNOME 的图标名约定：
// ubuntu-appindicators 会把 IconName 拼成 `<name>-panel` 再查主题目录，
// MyGo 只写 icon-1.png，必须补写 icon-1-panel.png 与下一次 SetIcon 的
// icon-2-panel.png，图标才会被扩展接受。
func TestMirrorPanelIconWritesPanelCopies(t *testing.T) {
	dir := t.TempDir()
	want := []byte("fake-png-bytes")
	if err := mirrorPanelIcon(dir, want); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"icon-1-panel.png", "icon-2-panel.png"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestFindNewTrayDir 覆盖托盘临时目录定位：只认本次 NewTray 新建、且
// icon-1.png 内容与传入图标一致的目录；旧目录/内容不符的目录都不算。
func TestFindNewTrayDir(t *testing.T) {
	base := t.TempDir()
	want := []byte("brand-icon")
	before := trayTempDirs(base)

	// 旧目录（before 中已有）与内容不符的新目录都不能被选中。
	old := filepath.Join(base, "mygo-tray-old")
	mustMkdir(t, old)
	mustWrite(t, filepath.Join(old, "icon-1.png"), want)
	stale := filepath.Join(base, "mygo-tray-stale")
	mustMkdir(t, stale)
	mustWrite(t, filepath.Join(stale, "icon-1.png"), []byte("another-app"))

	fresh := filepath.Join(base, "mygo-tray-fresh")
	mustMkdir(t, fresh)
	mustWrite(t, filepath.Join(fresh, "icon-1.png"), want)

	if got := findNewTrayDir(base, before, want); got != fresh {
		t.Fatalf("findNewTrayDir = %q, want %q", got, fresh)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
