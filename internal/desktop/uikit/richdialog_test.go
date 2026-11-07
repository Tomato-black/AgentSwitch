package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRichDialogBodyAndSafeFocus 覆盖 RichDialog 的核心契约：标题/副标题/
// 任意 body 都渲染；N 个动作按传入顺序渲染；安全动作默认聚焦，Enter 不
// 触发危险动作；点击危险动作才执行。
func TestRichDialogBodyAndSafeFocus(t *testing.T) {
	open := true
	var canceled, reloaded, overwritten bool
	tt := ui.NewTester(func(c *ui.Context) {
		th := c.Theme()
		RichDialog(c, th, RichDialogOpts{
			Open:     &open,
			Title:    "文件已被外部修改",
			Subtitle: "保存前发现磁盘内容变化。",
			Body: func() {
				ui.Text(c, "/tmp/settings.json").Font(MonoFont)
			},
			Actions: []RichDialogAction{
				{Label: "取消", Default: true, OnClick: func() { canceled = true }},
				{Label: "重新加载", OnClick: func() { reloaded = true }},
				{Label: "覆盖写入", Danger: true, OnClick: func() { overwritten = true }},
			},
		})
	}, 600, 420)

	for _, want := range []string{"文件已被外部修改", "保存前发现磁盘内容变化。", "/tmp/settings.json", "取消", "重新加载", "覆盖写入"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if !tt.Focused("取消") {
		t.Fatal("safe action must have focus")
	}
	tt.Key(0, ui.KeyEnter)
	if overwritten || reloaded {
		t.Fatal("Enter must not run non-default actions")
	}
	if !canceled || open {
		t.Fatalf("default action must run and close: canceled=%v open=%v", canceled, open)
	}

	open = true
	tt.Frame()
	if err := tt.Click("覆盖写入"); err != nil {
		t.Fatal(err)
	}
	if !overwritten {
		t.Fatal("danger action click must run the callback")
	}
	if open {
		t.Fatal("action must close the dialog")
	}
}

// TestRichDialogFallsBackToFirstSafeAction 验证未显式标记 Default 时，焦点
// 落在第一个非危险动作上，而不是危险动作。
func TestRichDialogFallsBackToFirstSafeAction(t *testing.T) {
	open := true
	tt := ui.NewTester(func(c *ui.Context) {
		RichDialog(c, c.Theme(), RichDialogOpts{
			Open:  &open,
			Title: "确认",
			Actions: []RichDialogAction{
				{Label: "危险", Danger: true},
				{Label: "安全"},
			},
		})
	}, 500, 300)
	if !tt.Focused("安全") {
		t.Fatalf("focus must fall back to the first safe action: %v", tt.Texts())
	}
}
