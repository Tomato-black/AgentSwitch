package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSplash(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Gap(12).Children(func() {
			ui.Text(c, "AgentSwitch")
		})
	}, 400, 300)
	if !tt.HasText("AgentSwitch") {
		t.Fatalf("texts %q missing title", tt.Texts())
	}
}
