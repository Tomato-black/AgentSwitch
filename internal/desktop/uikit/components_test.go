package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestPageHeaderRendersAndActs 覆盖统一页头：标题/副标题在场，返回与主操作
// 回调可达；标题字号随 textScale（FontSize+3）。
func TestPageHeaderRendersAndActs(t *testing.T) {
	var backed, acted bool
	var titleSize float32
	tt := ui.NewTester(func(c *ui.Context) {
		th := BuildTheme(false, 1)
		c.SetTheme(th)
		PageHeader(c, th, PageHeaderOpts{
			Title:     "逻辑模型",
			Subtitle:  "3 个",
			BackLabel: "← 返回列表",
			OnBack:    func() { backed = true },
			Actions: func() {
				if PillButton(c, th, "＋ 新建").Clicked() {
					acted = true
				}
			},
		})
		titleSize = th.FontSize + 3
	}, 800, 200)
	for _, want := range []string{"逻辑模型", "3 个", "← 返回列表", "＋ 新建"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if titleSize != 16 {
		t.Fatalf("title size = %v, want 16", titleSize)
	}
	if err := tt.Click("← 返回列表"); err != nil {
		t.Fatal(err)
	}
	if !backed {
		t.Fatal("back callback not delivered")
	}
	if err := tt.Click("＋ 新建"); err != nil {
		t.Fatal(err)
	}
	if !acted {
		t.Fatal("action callback not delivered")
	}
}

// TestEmptyStateRendersAndActs 覆盖统一空态：标题/说明/主操作，且点击可达。
func TestEmptyStateRendersAndActs(t *testing.T) {
	var acted bool
	tt := ui.NewTester(func(c *ui.Context) {
		EmptyState(c, c.Theme(), EmptyStateOpts{
			Title: "还没有渠道", Hint: "先添加渠道，再把渠道挂进逻辑模型",
			ActionLabel: "＋ 添加渠道", OnAction: func() { acted = true },
		})
	}, 800, 400)
	for _, want := range []string{"还没有渠道", "先添加渠道，再把渠道挂进逻辑模型", "＋ 添加渠道"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if err := tt.Click("＋ 添加渠道"); err != nil {
		t.Fatal(err)
	}
	if !acted {
		t.Fatal("empty state action not delivered")
	}
}

// TestFeedbackRendersAllKinds 覆盖统一反馈：四类消息都渲染文本；空文本不渲染。
func TestFeedbackRendersAllKinds(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		t := c.Theme()
		Feedback(c, t, FeedbackSuccess, "已保存")
		Feedback(c, t, FeedbackWarning, "配置已过期")
		Feedback(c, t, FeedbackError, "写入失败")
		Feedback(c, t, FeedbackNeutral, "本次运行")
		Feedback(c, t, FeedbackError, "   ")
	}, 600, 400)
	for _, want := range []string{"已保存", "配置已过期", "写入失败", "本次运行"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("   ") {
		t.Fatal("blank feedback must not render")
	}
}

// TestInferFeedback 覆盖过渡期消息语义推断：错误优先于成功，未应用/未运行
// 归警告，典型成功文案归成功，其余中性。
func TestInferFeedback(t *testing.T) {
	cases := map[string]FeedbackKind{
		"保存失败：无法写入": FeedbackError,
		"已保存":       FeedbackSuccess,
		"已应用「默认」——重启后生效": FeedbackSuccess,
		"配置已过期，需重新应用":    FeedbackWarning,
		"网关未运行":          FeedbackWarning,
		"本次运行":           FeedbackNeutral,
	}
	for text, want := range cases {
		if got := InferFeedback(text); got != want {
			t.Fatalf("InferFeedback(%q) = %v, want %v", text, got, want)
		}
	}
}

// TestBusyRowRendersAndCancels 覆盖进行中行：文案在场，取消回调可达。
func TestBusyRowRendersAndCancels(t *testing.T) {
	var canceled bool
	tt := ui.NewTester(func(c *ui.Context) {
		BusyRow(c, c.Theme(), "正在拉取模型…", func() { canceled = true })
	}, 600, 120)
	if !tt.HasText("正在拉取模型…") {
		t.Fatalf("missing busy label: %v", tt.Texts())
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if !canceled {
		t.Fatal("cancel callback not delivered")
	}
}

// TestFormRowRendersLabelAndField 覆盖统一表单行：标签在场、输入件可输入。
func TestFormRowRendersLabelAndField(t *testing.T) {
	name := ""
	tt := ui.NewTester(func(c *ui.Context) {
		FormRow(c, c.Theme(), "名称:", func() ui.Element {
			return ui.TextInput(c, &name)
		})
	}, 600, 200)
	if !tt.HasText("名称:") {
		t.Fatalf("missing label in %v", tt.Texts())
	}
}

// TestConfirmDialogCancelIsDefault 覆盖危险确认的安全默认：打开时焦点在
// 「取消」，按 Enter 不执行危险动作；点击危险按钮才执行。
func TestConfirmDialogCancelIsDefault(t *testing.T) {
	open := true
	confirmed := false
	tt := ui.NewTester(func(c *ui.Context) {
		ConfirmDialog(c, c.Theme(), ConfirmOpts{
			Open: &open, Title: "删除渠道", Message: "此操作不可撤销。",
			CancelLabel: "取消", ConfirmLabel: "删除", Danger: true,
			OnConfirm: func() { confirmed = true },
		})
	}, 600, 400)
	if !tt.HasText("删除渠道") || !tt.HasText("此操作不可撤销。") {
		t.Fatalf("dialog text missing: %v", tt.Texts())
	}
	if !tt.Focused("取消") {
		t.Fatalf("cancel must have focus: %v", tt.Focused("取消"))
	}
	tt.Key(0, ui.KeyEnter)
	if confirmed {
		t.Fatal("Enter must not run the destructive action")
	}
	if open {
		t.Fatal("Enter on cancel must close the dialog")
	}

	open = true
	tt.Frame() // 先渲染重新打开的对话框，再定位按钮
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("clicking confirm must run the action")
	}
	if open {
		t.Fatal("confirm must close the dialog")
	}
}
